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

func (d *Downloader) Download(urlStr string) (bool, error) {
	var lastErr error
	var completed bool
	for attempt := 1; attempt <= maxRetries; attempt++ {
		completed, lastErr = d.doDownload(urlStr)
		if lastErr == nil {
			return completed, nil
		}
		fmt.Printf("  attempt %d failed: %v — retrying...\n", attempt, lastErr)
		time.Sleep(time.Duration(attempt) * 2 * time.Second) // backoff: 2s, 4s, ...
	}
	return false, lastErr
}

func (d *Downloader) doDownload(urlStr string) (bool, error) {
	if err := os.MkdirAll(d.OutputDir, 0o755); err != nil {
		return false, err
	}

	// HEAD to check size + range support
	head, err := d.newRequest("HEAD", urlStr)
	if err != nil {
		return false, err
	}
	headResp, err := d.Client.Do(head)
	if err == nil {
		defer headResp.Body.Close()
	}

	supportsRanges := err == nil &&
		headResp.Header.Get("Accept-Ranges") == "bytes" &&
		headResp.ContentLength > 0

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
func (d *Downloader) chunkedDownload(urlStr, fileName string, totalSize int64) (bool, error) {
	fmt.Println("method chunkedDownload")
	output := filepath.Join(d.OutputDir, fileName)

	// Check if file already exists and is complete
	if info, err := os.Stat(output); err == nil {
		if info.Size() == totalSize {
			fmt.Printf("✓ file already complete: %s\n", fileName)
			return false, nil // skipped
		}
	}

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
			// cleanup temp files on error
			for _, f := range tmpFiles {
				_ = os.Remove(f)
			}
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
func (d *Downloader) streamDownload(urlStr string) (bool, error) {
	fmt.Println("method streamDownload")

	// First, do a HEAD to get content length
	headReq, err := d.newRequest("HEAD", urlStr)
	if err != nil {
		return false, err
	}
	headResp, err := d.Client.Do(headReq)
	if err != nil {
		return false, fmt.Errorf("HEAD failed: %w", err)
	}
	headResp.Body.Close()

	finalURL := headResp.Request.URL.String()
	fileName := resolveFilename(headResp, finalURL)
	output := filepath.Join(d.OutputDir, fileName)
	contentLength := headResp.ContentLength

	// Check for existing partial download
	existingSize := int64(0)
	if info, err := os.Stat(output); err == nil {
		if info.Size() == contentLength {
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

	bar := ui.NewBarWithOffset(contentLength, fileName, existingSize)
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

// func (d *Downloader) Download(urlStr string) error {
// 	var lastErr error
// 	for attempt := 1; attempt <= maxRetries; attempt++ {
// 		lastErr = d.doDownload(urlStr)
// 		if lastErr == nil {
// 			return nil
// 		}
// 		fmt.Printf("  attempt %d failed: %v — retrying...\n", attempt, lastErr)
// 		time.Sleep(time.Duration(attempt) * 2 * time.Second) // backoff: 2s, 4s
// 	}
// 	return lastErr
// }
//
// func (d *Downloader) doDownload(urlStr string) error {
// 	if err := os.MkdirAll(d.OutputDir, 0o755); err != nil {
// 		return err
// 	}
//
// 	// HEAD to check size + range support
// 	head, err := d.newRequest("HEAD", urlStr)
// 	if err != nil {
// 		return err
// 	}
// 	headResp, err := d.Client.Do(head)
// 	if err == nil {
// 		headResp.Body.Close()
// 	}
//
// 	supportsRanges := err == nil &&
// 		headResp.Header.Get("Accept-Ranges") == "bytes" &&
// 		headResp.ContentLength > 0
//
// 	// resolve filename from HEAD response (or fall back to GET later)
// 	var fileName string
// 	if err == nil {
// 		finalURL := headResp.Request.URL.String()
// 		fileName = resolveFilename(headResp, finalURL)
// 	}
//
// 	if supportsRanges {
// 		return d.chunkedDownload(urlStr, fileName, headResp.ContentLength)
// 	}
// 	return d.streamDownload(urlStr)
// }
//
// // chunkedDownload splits the file into numChunks parallel range requests
// func (d *Downloader) chunkedDownload(urlStr, fileName string, totalSize int64) error {
// 	fmt.Println("method chunkedDownload")
// 	output := filepath.Join(d.OutputDir, fileName)
//
// 	// skip if already complete
// 	if info, err := os.Stat(output); err == nil && info.Size() == totalSize {
// 		fmt.Printf("  %s already complete, skipping\n", fileName)
// 		return nil
// 	}
//
// 	chunkSize := totalSize / numChunks
// 	tmpFiles := make([]string, numChunks)
// 	for i := range numChunks {
// 		tmpFiles[i] = fmt.Sprintf("%s.part%d", output, i)
// 	}
//
// 	// calculate already-downloaded bytes across all chunks
// 	var alreadyDownloaded int64
// 	for _, tmp := range tmpFiles {
// 		if info, err := os.Stat(tmp); err == nil {
// 			alreadyDownloaded += info.Size()
// 		}
// 	}
//
// 	bar := ui.NewBarWithOffset(totalSize, fileName, alreadyDownloaded)
//
// 	type result struct {
// 		index int
// 		err   error
// 	}
// 	results := make(chan result, numChunks)
// 	var wg sync.WaitGroup
//
// 	for i := range numChunks {
// 		expectedStart := int64(i) * chunkSize
// 		expectedEnd := expectedStart + chunkSize - 1
// 		if i == numChunks-1 {
// 			expectedEnd = totalSize - 1
// 		}
// 		expectedChunkSize := expectedEnd - expectedStart + 1
//
// 		// check existing partial chunk
// 		var existingSize int64
// 		if info, err := os.Stat(tmpFiles[i]); err == nil {
// 			existingSize = info.Size()
// 		}
//
// 		// chunk already complete - skip it
// 		if existingSize == expectedChunkSize {
// 			continue
// 		}
//
// 		wg.Add(1)
// 		go func(idx int, start, end, existing int64, tmpPath string) {
// 			defer wg.Done()
//
// 			resumeStart := start + existing
// 			req, err := d.newRequest("GET", urlStr)
// 			if err != nil {
// 				results <- result{idx, err}
// 				return
// 			}
// 			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", resumeStart, end))
//
// 			resp, err := d.Client.Do(req)
// 			if err != nil {
// 				results <- result{idx, err}
// 				return
// 			}
// 			defer resp.Body.Close()
//
// 			// open in append mode if resuming, create otherwise
// 			flag := os.O_CREATE | os.O_WRONLY
// 			if existing > 0 {
// 				flag |= os.O_APPEND
// 			}
// 			f, err := os.OpenFile(tmpPath, flag, 0o644)
// 			if err != nil {
// 				results <- result{idx, err}
// 				return
// 			}
// 			defer f.Close()
//
// 			buf := make([]byte, 32*1024)
// 			for {
// 				n, err := resp.Body.Read(buf)
// 				if n > 0 {
// 					f.Write(buf[:n])
// 					bar.Add(n)
// 				}
// 				if err != nil {
// 					if err == io.EOF {
// 						break
// 					}
// 					results <- result{idx, err}
// 					return
// 				}
// 			}
// 			results <- result{idx, nil}
// 		}(i, expectedStart, expectedEnd, existingSize, tmpFiles[i])
// 	}
//
// 	wg.Wait()
// 	close(results)
//
// 	for r := range results {
// 		if r.err != nil {
// 			return fmt.Errorf("chunk %d failed: %w", r.index, r.err)
// 		}
// 	}
//
// 	// merge chunks
// 	if err := mergeFiles(output, tmpFiles); err != nil {
// 		return fmt.Errorf("merge failed: %w", err)
// 	}
//
// 	fmt.Printf("✓ saved → %s\n", output)
// 	return nil
// }
//
// // streamDownload is the fallback single-stream path
// func (d *Downloader) streamDownload(urlStr string) error {
// 	fmt.Println("method streamDownload")
//
// 	// First, do a HEAD request to get content length and check range support
// 	headReq, err := d.newRequest("HEAD", urlStr)
// 	if err != nil {
// 		return err
// 	}
//
// 	headResp, err := d.Client.Do(headReq)
// 	if err != nil {
// 		return fmt.Errorf("HEAD failed: %w", err)
// 	}
// 	headResp.Body.Close()
//
// 	finalURL := headResp.Request.URL.String()
// 	fileName := resolveFilename(headResp, finalURL)
// 	output := filepath.Join(d.OutputDir, fileName)
// 	contentLength := headResp.ContentLength
// 	supportsRanges := headResp.Header.Get("Accept-Ranges") == "bytes"
//
// 	// Check for existing partial file
// 	var existingSize int64
// 	if info, err := os.Stat(output); err == nil {
// 		existingSize = info.Size()
//
// 		// Already complete?
// 		if contentLength > 0 && existingSize == contentLength {
// 			fmt.Printf(
// 				"  %s already complete (%s), skipping\n",
// 				fileName,
// 				formatBytes(existingSize),
// 			)
// 			return nil
// 		}
//
// 		// File larger than expected? Restart
// 		if contentLength > 0 && existingSize > contentLength {
// 			fmt.Printf("  Warning: %s is larger than expected, restarting\n", fileName)
// 			existingSize = 0
// 		}
//
// 		// Can't resume if server doesn't support ranges
// 		if !supportsRanges && existingSize > 0 {
// 			fmt.Printf("  Warning: server doesn't support ranges, restarting %s\n", fileName)
// 			existingSize = 0
// 		}
// 	}
//
// 	// Create GET request
// 	req, err := d.newRequest("GET", urlStr)
// 	if err != nil {
// 		return err
// 	}
//
// 	// Add Range header if resuming
// 	if existingSize > 0 {
// 		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
// 		fmt.Printf("  Resuming %s from %s\n", fileName, formatBytes(existingSize))
// 	}
//
// 	resp, err := d.Client.Do(req)
// 	if err != nil {
// 		return fmt.Errorf("GET failed: %w", err)
// 	}
// 	defer resp.Body.Close()
//
// 	// Accept both 200 (full) and 206 (partial content)
// 	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
// 		return fmt.Errorf("bad status: %s", resp.Status)
// 	}
//
// 	// Open file in append mode if resuming, create otherwise
// 	flag := os.O_CREATE | os.O_WRONLY
// 	if existingSize > 0 {
// 		flag |= os.O_APPEND
// 	}
//
// 	f, err := os.OpenFile(output, flag, 0o644)
// 	if err != nil {
// 		return err
// 	}
// 	defer f.Close()
//
// 	// Initialize progress bar with existing bytes
// 	bar := ui.NewBarWithOffset(contentLength, fileName, existingSize)
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
//
// // Helper to format bytes
// func formatBytes(bytes int64) string {
// 	const unit = 1024
// 	if bytes < unit {
// 		return fmt.Sprintf("%d B", bytes)
// 	}
// 	div, exp := int64(unit), 0
// 	for n := bytes / unit; n >= unit; n /= unit {
// 		div *= unit
// 		exp++
// 	}
// 	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
// }

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
