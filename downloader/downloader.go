package downloader

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"modules/config"
	"modules/ui"
)

type Downloader struct {
	OutputDir  string
	NumChunks  int
	MaxRetries int
	Client     *http.Client
}

func New(cfg *config.Config) *Downloader {
	return &Downloader{
		OutputDir:  cfg.OutputDir,
		NumChunks:  cfg.NumChunks,
		MaxRetries: cfg.MaxRetries,
		Client: &http.Client{
			Timeout: 0,
			Transport: &http.Transport{
				MaxIdleConns:          cfg.MaxIdleConns,
				IdleConnTimeout:       cfg.IdleConnTimeout,
				TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
				ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
				ExpectContinueTimeout: cfg.ExpectContinueTimeout,
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: cfg.InsecureSkipVerify,
				},
				DialContext: (&net.Dialer{
					Timeout:   cfg.DialTimeout,
					KeepAlive: cfg.KeepAlive,
				}).DialContext,
			},
		},
	}
}

func (d *Downloader) Download(urlStr string) (bool, error) {
	var lastErr error
	var completed bool
	for attempt := 1; attempt <= d.MaxRetries; attempt++ {
		completed, lastErr = d.doDownload(urlStr)
		if lastErr == nil {
			return completed, nil
		}
		fmt.Printf("  attempt %d failed: %v\n", attempt, lastErr)
		fmt.Println("retrying...")
		time.Sleep(time.Duration(attempt) * 2 * time.Second) // backoff: 2s, 4s, ...
	}
	return false, lastErr
}

func (d *Downloader) doDownload(urlStr string) (bool, error) {
	if err := os.MkdirAll(d.OutputDir, 0o755); err != nil {
		return false, err
	}

	// Try HEAD first
	req, err := d.newRequest("HEAD", urlStr)
	if err != nil {
		return false, err
	}
	resp, err := d.Client.Do(req)

	// If HEAD fails or returns 405/403 (Method Not Allowed), try GET
	if err != nil || (resp != nil && resp.StatusCode >= 400) {
		if resp != nil {
			resp.Body.Close()
		}
		// Fallback: Try a GET request but close the body immediately
		// just to get the headers/metadata.
		req, _ = d.newRequest("GET", urlStr)
		resp, err = d.Client.Do(req)
		if err != nil {
			return false, fmt.Errorf("metadata fetch failed (HEAD and GET both failed): %w", err)
		}
	}
	defer resp.Body.Close()

	// check if resp is nil before using it
	if resp == nil {
		return false, fmt.Errorf("received nil response from server")
	}

	finalURL := resp.Request.URL.String()
	fileName := resolveFilename(resp, finalURL)
	totalSize := resp.ContentLength

	supportsRanges := resp.Header.Get("Accept-Ranges") == "bytes" && totalSize > 0

	if supportsRanges {
		return d.chunkedDownload(urlStr, fileName, totalSize)
	}
	return d.streamDownload(urlStr, fileName, totalSize)
}

// chunkedDownload splits the file into numChunks parallel range requests
func (d *Downloader) chunkedDownload(urlStr, fileName string, totalSize int64) (bool, error) {
	fmt.Println("method chunkedDownload")
	output := filepath.Join(d.OutputDir, fileName)

	// Check if file already exists and is complete
	info, err := os.Stat(output)
	if err == nil {
		if info.Size() == totalSize {
			fmt.Printf("✓ file already complete: %s\n", fileName)
			return false, nil // skipped
		}
	}

	var currentSize int64
	if info != nil {
		currentSize = info.Size()
	}
	bar := ui.NewBarWithOffset(totalSize, fileName, currentSize)

	chunkSize := totalSize / int64(d.NumChunks)
	type result struct {
		index int
		err   error
	}

	tmpFiles := make([]string, d.NumChunks)
	results := make(chan result, d.NumChunks)
	var wg sync.WaitGroup

	for i := range d.NumChunks {
		start := int64(i) * chunkSize
		end := start + chunkSize - 1
		if i == d.NumChunks-1 {
			end = totalSize - 1 // last chunk gets the remainder
		}

		tmpPath := fmt.Sprintf("%s.part%d", output, i)
		tmpFiles[i] = tmpPath

		wg.Add(1)
		go func(index int, s, e int64, path string) {
			defer wg.Done()
			err := d.downloadChunk(urlStr, s, e, path, bar)
			results <- result{index: index, err: err}
		}(i, start, end, tmpPath)
	}

	wg.Wait()
	close(results)

	for res := range results {
		if res.err != nil {
			return false, fmt.Errorf("chunk %d failed: %w", res.index, res.err)
		}
	}

	// merge chunks
	if err := mergeFiles(output, tmpFiles); err != nil {
		return false, fmt.Errorf("merge failed: %w", err)
	}

	fmt.Printf("✓ saved → %s\n", output)
	return true, nil // actually completed
}

// download a single chunk
func (d *Downloader) downloadChunk(
	urlStr string,
	start, end int64,
	path string,
	bar *ui.Bar,
) error {
	// Check if chunk already exists and adjust range
	existingSize := int64(0)
	if info, err := os.Stat(path); err == nil {
		existingSize = info.Size()
		if existingSize == (end - start + 1) {
			// Chunk already complete
			bar.Add(int(existingSize))
			return nil
		}
		// Resume from where we left off
		start += existingSize
	}

	req, err := d.newRequest("GET", urlStr)
	if err != nil {
		return err
	}

	// Set Range header
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := d.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}

	// Open file in append mode for resume
	flag := os.O_CREATE | os.O_WRONLY
	if existingSize > 0 {
		flag |= os.O_APPEND
		bar.Add(int(existingSize)) // Update bar with existing progress
	}

	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			bar.Add(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	return nil
}

// streamDownload is the fallback single-stream path
func (d *Downloader) streamDownload(urlStr, fileName string, totalSize int64) (bool, error) {
	fmt.Println("method streamDownload")

	output := filepath.Join(d.OutputDir, fileName)

	// Check for existing partial download
	existingSize := int64(0)
	if info, err := os.Stat(output); err == nil {
		if info.Size() == totalSize {
			fmt.Printf("✓ file already complete: %s\n", fileName)
			return false, nil // skipped
		}
		existingSize = info.Size()
	}

	// Now make the GET request
	req, err := d.newRequest("GET", urlStr)
	if err != nil {
		return false, err
	}

	// Add Range header if resuming
	if existingSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
	}

	resp, err := d.Client.Do(req)
	if err != nil {
		return false, fmt.Errorf("GET failed: %w", err)
	}
	defer resp.Body.Close()

	// Accept both 200 (full) and 206 (partial content)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return false, fmt.Errorf("bad status: %s", resp.Status)
	}

	// Open in append mode if resuming
	flag := os.O_CREATE | os.O_WRONLY
	if existingSize > 0 {
		flag |= os.O_APPEND
	}

	f, err := os.OpenFile(output, flag, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()

	bar := ui.NewBarWithOffset(totalSize, fileName, existingSize)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return false, fmt.Errorf("write failed: %w", werr)
			}
			bar.Add(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, err
		}
	}

	fmt.Printf("✓ saved → %s\n", output)
	return true, nil
}

// mergeFiles concatenates tmpFiles into dst in order, then removes them
func mergeFiles(dst string, tmpFiles []string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	for _, tmp := range tmpFiles {
		f, err := os.Open(tmp)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, f); err != nil {
			f.Close()
			return err
		}
		f.Close()
		os.Remove(tmp)
	}
	return nil
}

func (d *Downloader) newRequest(method, urlStr string) (*http.Request, error) {
	req, err := http.NewRequest(method, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("%s request failed: %w", method, err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; dlm/1.0)")
	return req, nil
}

// resolveFilename tries Content-Disposition first, falls back to URL path
func resolveFilename(resp *http.Response, urlStr string) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		for _, part := range splitHeader(cd) {
			if len(part) > 9 && part[:9] == "filename=" {
				return strings.Trim(part[9:], `"`)
			}
		}
	}

	segments := strings.Split(urlStr, "/")
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i] != "" {
			if decoded, err := url.PathUnescape(segments[i]); err == nil && decoded != "" {
				return decoded
			}
			return segments[i]
		}
	}

	return fmt.Sprintf("download_%d", time.Now().Unix())
}

func splitHeader(s string) []string {
	var parts []string
	for _, p := range strings.Split(s, ";") {
		parts = append(parts, strings.TrimSpace(p))
	}
	return parts
}
