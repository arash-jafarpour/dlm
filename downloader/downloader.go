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

	"modules/ui"
)

const (
	numChunks  = 8
	maxRetries = 2
)

type Downloader struct {
	OutputDir string
	Client    *http.Client
}

func New(outputDir string) *Downloader {
	return &Downloader{
		OutputDir: outputDir,
		Client: &http.Client{
			Timeout: 0,
			Transport: &http.Transport{
				DisableCompression:    false,
				MaxIdleConns:          50,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, // TODO: remove before production
				},
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
			},
		},
	}
}

func (d *Downloader) Download(urlStr string) error {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		lastErr = d.doDownload(urlStr)
		if lastErr == nil {
			return nil
		}
		fmt.Printf("  attempt %d failed: %v — retrying...\n", attempt, lastErr)
		time.Sleep(time.Duration(attempt) * 2 * time.Second) // backoff: 2s, 4s
	}
	return lastErr
}

func (d *Downloader) doDownload(urlStr string) error {
	if err := os.MkdirAll(d.OutputDir, 0o755); err != nil {
		return err
	}

	// HEAD to check size + range support
	head, err := d.newRequest("HEAD", urlStr)
	if err != nil {
		return err
	}
	headResp, err := d.Client.Do(head)
	if err == nil {
		headResp.Body.Close()
	}

	supportsRanges := err == nil &&
		headResp.Header.Get("Accept-Ranges") == "bytes" &&
		headResp.ContentLength > 0

	// resolve filename from HEAD response (or fall back to GET later)
	var fileName string
	if err == nil {
		finalURL := headResp.Request.URL.String()
		fileName = resolveFilename(headResp, finalURL)
	}

	if supportsRanges {
		return d.chunkedDownload(urlStr, fileName, headResp.ContentLength)
	}
	return d.streamDownload(urlStr)
}

// chunkedDownload splits the file into numChunks parallel range requests
func (d *Downloader) chunkedDownload(urlStr, fileName string, totalSize int64) error {
	fmt.Println("method chunkedDownload")
	output := filepath.Join(d.OutputDir, fileName)
	bar := ui.NewBar(totalSize, fileName)

	chunkSize := totalSize / numChunks
	type result struct {
		index int
		err   error
	}

	tmpFiles := make([]string, numChunks)
	results := make(chan result, numChunks)
	var wg sync.WaitGroup

	for i := range numChunks {
		start := int64(i) * chunkSize
		end := start + chunkSize - 1
		if i == numChunks-1 {
			end = totalSize - 1 // last chunk gets the remainder
		}

		tmpPath := filepath.Join(d.OutputDir, fmt.Sprintf(".tmp_%s_%d", fileName, i))
		tmpFiles[i] = tmpPath

		wg.Add(1)
		go func(idx int, from, to int64, tmp string) {
			defer wg.Done()
			results <- result{idx, d.downloadChunk(urlStr, from, to, tmp, bar)}
		}(i, start, end, tmpPath)
	}

	// close results channel once all goroutines finish
	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		if r.err != nil {
			return fmt.Errorf("chunk %d failed: %w", r.index, r.err)
		}
	}

	// merge temp files in order
	if err := mergeFiles(output, tmpFiles); err != nil {
		return fmt.Errorf("merge failed: %w", err)
	}

	fmt.Printf("✓ saved → %s\n", output)
	return nil
}

// downloadChunk fetches a byte range and writes it to a temp file
func (d *Downloader) downloadChunk(
	urlStr string,
	from, to int64,
	tmpPath string,
	bar *ui.Bar,
) error {
	req, err := d.newRequest("GET", urlStr)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))

	resp, err := d.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("expected 206, got %s", resp.Status)
	}

	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return fmt.Errorf("write failed: %w", werr)
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
func (d *Downloader) streamDownload(urlStr string) error {
	fmt.Println("method streamDownload")
	req, err := d.newRequest("GET", urlStr)
	if err != nil {
		return err
	}

	resp, err := d.Client.Do(req)
	if err != nil {
		return fmt.Errorf("GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	finalURL := resp.Request.URL.String()
	fileName := resolveFilename(resp, finalURL)
	output := filepath.Join(d.OutputDir, fileName)

	f, err := os.Create(output)
	if err != nil {
		return err
	}
	defer f.Close()

	bar := ui.NewBar(resp.ContentLength, fileName)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return fmt.Errorf("write failed: %w", werr)
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

	fmt.Printf("✓ saved → %s\n", output)
	return nil
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

// func (d *Downloader) doDownload(urlStr string) error {
// 	// ensure directory exists
// 	if err := os.MkdirAll(d.OutputDir, 0o755); err != nil {
// 		return err
// 	}
//
// 	req, err := http.NewRequest("GET", urlStr, nil)
// 	if err != nil {
// 		return fmt.Errorf("initial GET failed: %w", err)
// 	}
// 	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; dlm/1.0)")
//
// 	// direct GET (follows redirects)
// 	resp, err := d.Client.Do(req)
// 	if err != nil {
// 		return fmt.Errorf("GET failed: %w", err)
// 	}
// 	defer resp.Body.Close()
//
// 	if resp.StatusCode != 200 {
// 		return fmt.Errorf("bad status: %s", resp.Status)
// 	}
//
// 	// final URL after redirects
// 	finalURL := resp.Request.URL.String()
//
// 	totalSize := resp.ContentLength
// 	fileName := resolveFilename(resp, finalURL)
// 	output := filepath.Join(d.OutputDir, fileName)
//
// 	f, err := os.Create(output)
// 	if err != nil {
// 		return err
// 	}
// 	defer f.Close()
//
// 	bar := ui.NewBar(totalSize, fileName)
//
// 	buf := make([]byte, 32*1024)
// 	for {
// 		n, err := resp.Body.Read(buf)
// 		if n > 0 {
// 			if _, werr := f.Write(buf[:n]); werr != nil {
// 				return fmt.Errorf("write failed: %w", werr)
// 			}
// 			bar.Add(n)
// 		}
// 		if err == io.EOF {
// 			break
// 		}
// 		if err != nil {
// 			return err
// 		}
// 	}
//
// 	fmt.Printf("✓ saved → %s\n", output)
// 	return nil
// }

// // resolveFilename tries Content-Disposition first, falls back to URL path
// func resolveFilename(resp *http.Response, urlStr string) string {
// 	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
// 		for _, part := range splitHeader(cd) {
// 			if len(part) > 9 && part[:9] == "filename=" {
// 				return strings.Trim(part[9:], `"`)
// 			}
// 		}
// 	}
//
// 	// fallback: last segment of the URL path
// 	segments := strings.Split(urlStr, "/")
// 	for i := len(segments) - 1; i >= 0; i-- {
// 		if segments[i] != "" {
// 			// decode URL-encoded filename
// 			if decoded, err := url.PathUnescape(segments[i]); err == nil && decoded != "" {
// 				return decoded
// 			}
// 			return segments[i] // fallback if decode fails
// 		}
// 	}
//
// 	return fmt.Sprintf("download_%d", time.Now().Unix())
// }

// func splitHeader(s string) []string {
// 	var parts []string
// 	for _, p := range strings.Split(s, ";") {
// 		parts = append(parts, strings.TrimSpace(p))
// 	}
// 	return parts
// }
