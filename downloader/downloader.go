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

	"dlm/config"
	apperrors "dlm/errors"
	"dlm/ui"
)

type Downloader struct {
	OutputDir  string
	NumChunks  int
	MaxRetries int
	Client     *http.Client
}

func buildHTTPClient(cfg *config.Config) *http.Client {
	dialer := &net.Dialer{
		Timeout:   cfg.DialTimeout,
		KeepAlive: cfg.KeepAlive,
	}
	transport := &http.Transport{
		MaxIdleConns:          cfg.MaxIdleConns,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: cfg.ExpectContinueTimeout,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify},
		DialContext:           dialer.DialContext,
	}
	return &http.Client{Timeout: 0, Transport: transport}
}

func New(cfg *config.Config) *Downloader {
	return &Downloader{
		OutputDir:  cfg.OutputDir,
		NumChunks:  cfg.NumChunks,
		MaxRetries: cfg.MaxRetries,
		Client:     buildHTTPClient(cfg),
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
		return false, apperrors.WrapFileError(d.OutputDir, "mkdir", err)
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
			return false, apperrors.WrapHTTPError(
				urlStr,
				"metadata fetch failed (HEAD and GET both failed)",
				-1,
				err,
			)
		}
	}
	defer resp.Body.Close()

	// check if resp is nil before using it
	if resp == nil {
		return false, apperrors.ErrNilResponse
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
	fmt.Println("with method chunkedDownload")
	output := filepath.Join(d.OutputDir, fileName)

	// Check if file already exists and is complete
	info, err := os.Stat(output)
	if err == nil {
		if info.Size() == totalSize {
			fmt.Printf("%s %s\n", ui.Green("✓ file already complete:"), fileName)
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
			return false, apperrors.WrapChunkError(urlStr, res.index, res.err)
		}
	}

	// merge chunks
	if err := mergeFiles(output, tmpFiles); err != nil {
		return false, apperrors.WrapChunkError(urlStr, -1, err)
	}

	fmt.Printf("%s %s\n", ui.Green("✓ saved →"), output)
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
		return apperrors.WrapHTTPError(urlStr, "chunk request failed", -1, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return apperrors.NewStatusError(urlStr, resp)
	}

	// Open file in append mode for resume
	flag := os.O_CREATE | os.O_WRONLY
	if existingSize > 0 {
		flag |= os.O_APPEND
		bar.Add(int(existingSize)) // Update bar with existing progress
	}

	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return apperrors.WrapFileError(path, "open", err)
	}
	defer f.Close()

	if err := copyToFile(resp.Body, f, bar, urlStr, path); err != nil {
		return err
	}

	return nil
}

// streamDownload is the fallback single-stream path
func (d *Downloader) streamDownload(urlStr, fileName string, totalSize int64) (bool, error) {
	fmt.Println("with method streamDownload")

	output := filepath.Join(d.OutputDir, fileName)

	// Check for existing partial download
	existingSize := int64(0)
	if info, err := os.Stat(output); err == nil {
		if info.Size() == totalSize {
			fmt.Printf("%s %s\n", ui.Green("✓ file already complete:"), fileName)
			return false, nil // skipped
		}
		existingSize = info.Size()
	}

	// Now make the GET request
	req, err := d.newRequest("GET", urlStr)
	if err != nil {
		return false, apperrors.WrapDownloadError(urlStr, "creating GET request", err)
	}

	// Add Range header if resuming
	if existingSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
	}

	resp, err := d.Client.Do(req)
	if err != nil {
		return false, apperrors.WrapHTTPError(urlStr, "GET request failed", -1, err)
	}
	defer resp.Body.Close()

	// Accept both 200 (full) and 206 (partial content)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return false, apperrors.NewStatusError(urlStr, resp)
	}

	// Open in append mode if resuming
	flag := os.O_CREATE | os.O_WRONLY
	if existingSize > 0 {
		flag |= os.O_APPEND
	}

	f, err := os.OpenFile(output, flag, 0o644)
	if err != nil {
		return false, apperrors.WrapFileError(output, "open", err)
	}
	defer f.Close()

	bar := ui.NewBarWithOffset(totalSize, fileName, existingSize)

	if err := copyToFile(resp.Body, f, bar, urlStr, output); err != nil {
		return false, err
	}

	fmt.Printf("%s %s\n", ui.Green("✓ saved →"), output)
	return true, nil
}

// mergeFiles concatenates tmpFiles into dst in order, then removes them
func mergeFiles(dst string, tmpFiles []string) error {
	out, err := os.Create(dst)
	if err != nil {
		return apperrors.WrapFileError(dst, "create", err)
	}
	defer out.Close()

	for _, tmp := range tmpFiles {
		f, err := os.Open(tmp)
		if err != nil {
			return apperrors.WrapFileError(tmp, "open", err)
		}
		if _, err := io.Copy(out, f); err != nil {
			f.Close()
			return apperrors.WrapFileError(tmp, "copy", err)
		}
		f.Close()
		os.Remove(tmp)
	}
	return nil
}

// copyToFile reads from r and writes to f, updating bar. Returns any read/write error.
func copyToFile(r io.Reader, f *os.File, bar *ui.Bar, urlStr, path string) error {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return apperrors.WrapFileError(path, "write", werr)
			}
			bar.Add(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return apperrors.WrapHTTPError(urlStr, "reading body", -1, err)
		}
	}
	return nil
}

func (d *Downloader) newRequest(method, urlStr string) (*http.Request, error) {
	req, err := http.NewRequest(method, urlStr, nil)
	if err != nil {
		return nil, apperrors.WrapDownloadError(
			urlStr,
			fmt.Sprintf("creating %s request failed", method),
			err,
		)
	}
	// req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; dlm/1.0)")
	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	)
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
