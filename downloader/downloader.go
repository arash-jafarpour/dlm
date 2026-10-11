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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/arash-jafarpour/dlm/config"
	apperrors "github.com/arash-jafarpour/dlm/errors"
)

type Downloader struct {
	OutputDir  string
	NumChunks  int
	MaxRetries int
	Client     *http.Client
	// Reporter receives lifecycle notifications and progress callbacks. New
	// installs a no-op default when it is nil.
	Reporter Reporter
	// Backoff is the base delay between retry attempts: attempt n waits n *
	// Backoff before retrying. Zero disables sleeping (useful in tests).
	Backoff time.Duration
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

func New(cfg *config.Config, rep Reporter) *Downloader {
	if rep == nil {
		rep = noopReporter{}
	}
	return &Downloader{
		OutputDir:  cfg.OutputDir,
		NumChunks:  cfg.NumChunks,
		MaxRetries: cfg.MaxRetries,
		Client:     buildHTTPClient(cfg),
		Reporter:   rep,
		Backoff:    2 * time.Second,
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
		if !apperrors.IsRetryable(lastErr) {
			return false, lastErr // permanent error, stop immediately
		}
		d.Reporter.Retry(attempt, lastErr)
		time.Sleep(time.Duration(attempt) * d.Backoff)
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

	// A server supports ranges if it advertises "Accept-Ranges: bytes", or
	// if it already answered with a ranged response ("Content-Range").
	// Without range support the whole file must be streamed in one piece.
	rangeSupport := resp.Header.Get("Accept-Ranges") == "bytes" ||
		resp.Header.Get("Content-Range") != ""
	supportsRanges := rangeSupport && totalSize > 0

	if supportsRanges {
		return d.chunkedDownload(urlStr, fileName, totalSize)
	}
	return d.streamDownload(urlStr, fileName, totalSize)
}

func checkExistingFile(path string, expectedSize int64) (exists bool, size int64, complete bool) {
	info, err := os.Stat(path)
	if err != nil {
		return false, 0, false
	}
	return true, info.Size(), info.Size() == expectedSize
}

// verifyContentRange checks that a "bytes S-E/T" Content-Range header matches
// the chunk bounds that were requested. Unparseable headers are tolerated:
// the header has no strict grammar guarantee, and a mismatch is what actually
// proves corruption.
func verifyContentRange(cr string, start, end int64) error {
	body := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(cr), "bytes"))
	parts := strings.Split(body, "/")
	if len(parts) < 2 {
		return nil
	}
	bounds := strings.Split(parts[0], "-")
	if len(bounds) != 2 {
		return nil
	}
	gotStart, errStart := strconv.ParseInt(strings.TrimSpace(bounds[0]), 10, 64)
	gotEnd, errEnd := strconv.ParseInt(strings.TrimSpace(bounds[1]), 10, 64)
	if errStart != nil || errEnd != nil {
		return nil
	}
	if gotStart != start || gotEnd != end {
		return fmt.Errorf("server returned bytes %d-%d, want %d-%d", gotStart, gotEnd, start, end)
	}
	return nil
}

// chunkedDownload splits the file into numChunks parallel range requests
func (d *Downloader) chunkedDownload(urlStr, fileName string, totalSize int64) (bool, error) {
	output := filepath.Join(d.OutputDir, fileName)

	_, currentSize, complete := checkExistingFile(output, totalSize)
	if complete {
		d.Reporter.AlreadyComplete(fileName)
		return false, nil
	}

	bar := d.Reporter.Progress(totalSize, fileName, currentSize)
	defer bar.Done()

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

	// Close the bar before reporting the saved path so the final frame and
	// its newline land before the follow-up message.
	bar.Done()
	d.Reporter.Saved(output)
	return true, nil // actually completed
}

// download a single chunk
func (d *Downloader) downloadChunk(
	urlStr string,
	start, end int64,
	path string,
	bar ProgressBar,
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

	// A Range request must be answered with 206 Partial Content. A 200
	// response means the server ignored the header and sent the full body;
	// writing or appending it into the part file would corrupt the merged
	// file. Fail instead and leave any existing parts untouched.
	if resp.StatusCode == http.StatusOK {
		return apperrors.WrapHTTPError(
			urlStr,
			"server ignored Range request (got 200, want 206)",
			resp.StatusCode,
			nil,
		)
	}
	if resp.StatusCode != http.StatusPartialContent {
		return apperrors.NewStatusError(urlStr, resp)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if err := verifyContentRange(cr, start, end); err != nil {
			return apperrors.WrapHTTPError(
				urlStr,
				"unexpected Content-Range",
				resp.StatusCode,
				err,
			)
		}
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
	output := filepath.Join(d.OutputDir, fileName)

	_, existingSize, complete := checkExistingFile(output, totalSize)
	if complete {
		d.Reporter.AlreadyComplete(fileName)
		return false, nil
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

	// If we asked for a Range and got a plain 200, the server ignored the
	// header and sent the full body again. Appending it would duplicate the
	// already-downloaded bytes, so a resume is impossible without losing
	// progress. Fail with a clear message instead of corrupting the file.
	if existingSize > 0 && resp.StatusCode == http.StatusOK {
		return false, apperrors.WrapHTTPError(
			urlStr,
			"cannot resume: server ignored Range request (got full 200 body)",
			resp.StatusCode,
			nil,
		)
	}

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

	bar := d.Reporter.Progress(totalSize, fileName, existingSize)
	defer bar.Done()

	if err := copyToFile(resp.Body, f, bar, urlStr, output); err != nil {
		return false, err
	}

	// Close the bar before reporting the saved path so the final frame and
	// its newline land before the follow-up message.
	bar.Done()
	d.Reporter.Saved(output)
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
func copyToFile(r io.Reader, f *os.File, bar ProgressBar, urlStr, path string) error {
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

// resolveFilename tries Content-Disposition first, falls back to the last
// non-empty path segment of the URL's path, and finally to a timestamped
// name. The segment loop only ever sees u.Path, so a path-less or malformed
// URL (e.g. "https://example.com") can no longer resolve to the host or
// scheme, which previously produced files named like "127.0.0.1:8080" or
// "https:".
func resolveFilename(resp *http.Response, urlStr string) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		for _, part := range splitHeader(cd) {
			if len(part) > 9 && part[:9] == "filename=" {
				return strings.Trim(part[9:], `"`)
			}
		}
	}

	if u, err := url.Parse(urlStr); err == nil {
		segments := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i := len(segments) - 1; i >= 0; i-- {
			if segments[i] == "" {
				continue
			}
			if decoded, derr := url.PathUnescape(segments[i]); derr == nil && decoded != "" {
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
