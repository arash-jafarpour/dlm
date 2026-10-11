package downloader

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arash-jafarpour/dlm/config"
	apperrors "github.com/arash-jafarpour/dlm/errors"
)

// ---------------------------------------------------------------------------
// Fakes and helpers
// ---------------------------------------------------------------------------

const testFileName = "file"

// recordBar is a ProgressBar that forwards Add/Done counts to its recorder.
// Its Done mirrors the real ui.Bar: idempotent, so a second call records
// nothing.
type recordBar struct {
	rec    *eventRecorder
	finish sync.Once
}

func (b *recordBar) Add(n int) { b.rec.progressAdd(n) }
func (b *recordBar) Done()     { b.finish.Do(b.rec.progressDone) }

// eventRecorder captures everything the downloader reports. It is safe for
// concurrent use because chunk goroutines call Add in parallel.
type eventRecorder struct {
	mu       sync.Mutex
	added    int
	done     bool
	complete string
	saved    []string
	retries  []int
	// events keeps the ordering between Done and Saved observable.
	events []string
}

func (e *eventRecorder) Retry(attempt int, _ error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.retries = append(e.retries, attempt)
}

func (e *eventRecorder) AlreadyComplete(fileName string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.complete = fileName
}

func (e *eventRecorder) Saved(path string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.saved = append(e.saved, path)
	e.events = append(e.events, "saved:"+filepath.Base(path))
}

func (e *eventRecorder) Progress(int64, string, int64) ProgressBar {
	return &recordBar{rec: e}
}

func (e *eventRecorder) progressAdd(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.added += n
}

func (e *eventRecorder) progressDone() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.done = true
	e.events = append(e.events, "done")
}

// recorderSnapshot is a lock-free copy of eventRecorder's state so tests can
// read recorded values after the fact without handing out the mutex.
type recorderSnapshot struct {
	added    int
	done     bool
	complete string
	saved    []string
	retries  []int
	events   []string
}

func (e *eventRecorder) snapshot() recorderSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := recorderSnapshot{
		added: e.added, done: e.done, complete: e.complete,
		saved: e.saved, retries: e.retries,
	}
	s.events = append(s.events, e.events...)
	return s
}

// testDownloader builds a Downloader pointed at a temp directory with fast,
// silent behavior suitable for tests.
func testDownloader(t *testing.T, rep Reporter, numChunks, maxRetries int) *Downloader {
	t.Helper()
	cfg := config.Default()
	cfg.OutputDir = t.TempDir()
	cfg.NumChunks = numChunks
	cfg.MaxRetries = maxRetries
	d := New(cfg, rep)
	d.Backoff = 0 // no sleeping between retries
	return d
}

// rangeServer serves data with full Range support via http.ServeContent
// (HEAD and GET, 206 responses, Content-Length, Content-Range) and advertises
// "Accept-Ranges: bytes". routed through the chunked path.
func rangeServer(data []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", testFileName))
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(w, r, testFileName, time.Time{}, bytes.NewReader(data))
	}))
}

// quietRangeServer serves ranges via http.ServeContent but does NOT advertise
// "Accept-Ranges", so the downloader's metadata probe treats it as
// range-less and takes the single-stream path (which still resumes fine
// because ServeContent honors open-ended Range requests).
func quietRangeServer(data []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", testFileName))
		http.ServeContent(w, r, testFileName, time.Time{}, bytes.NewReader(data))
	}))
}

// noRangeServer serves data as a plain GET/HEAD body without any range
// support, forcing the single-stream path.
func noRangeServer(data []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", testFileName))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(data)
	}))
}

// ignoreRangeServer advertises range support in its metadata response but
// answers every download GET with a full 200 body, simulating a server that
// ignores Range requests (the corruption scenario).
func ignoreRangeServer(data []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", testFileName))
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
}

// fragServer breaks the first chunk GET with a 503 once, then recovers. Used
// to test the retry loop across full doDownload attempts.
func fragServer(data []byte) *httptest.Server {
	var mu sync.Mutex
	chunkGets := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", testFileName))
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Header.Get("Range") == "" {
			// Metadata-fallback GET (no Range): serve full metadata body.
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data)
			return
		}
		mu.Lock()
		chunkGets++
		fail := chunkGets == 1
		mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		http.ServeContent(w, r, testFileName, time.Time{}, bytes.NewReader(data))
	}))
}

// chunkBounds splits totalSize into chunks like chunkedDownload does and
// returns the [start, end] byte range of the given chunk index.
func chunkBounds(totalSize int64, numChunks, index int) (int64, int64) {
	chunkSize := totalSize / int64(numChunks)
	start := int64(index) * chunkSize
	end := start + chunkSize - 1
	if index == numChunks-1 {
		end = totalSize - 1
	}
	return start, end
}

// mustReadFile reads a file and fails the test on error.
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// ---------------------------------------------------------------------------
// Chunked downloads
// ---------------------------------------------------------------------------

func TestChunkedDownloadMergesChunks(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 64*1024) // 1 MiB
	srv := rangeServer(data)
	defer srv.Close()

	rec := &eventRecorder{}
	d := testDownloader(t, rec, 4, 1)

	completed, err := d.Download(srv.URL)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	if !completed {
		t.Fatal("expected completed=true for a fresh download")
	}

	got := mustReadFile(t, filepath.Join(d.OutputDir, testFileName))
	if !bytes.Equal(got, data) {
		t.Fatalf("merged file differs: got %d bytes, want %d", len(got), len(data))
	}

	// All .part files must have been removed after the merge.
	entries, _ := os.ReadDir(d.OutputDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".part") {
			t.Errorf("leftover part file: %s", e.Name())
		}
	}

	ev := rec.snapshot()
	if ev.added != len(data) {
		t.Errorf("progress bytes %d, want %d", ev.added, len(data))
	}
	if !ev.done {
		t.Error("progress bar Done() was not called")
	}
	if len(ev.saved) != 1 || !strings.HasSuffix(ev.saved[0], testFileName) {
		t.Errorf("unexpected Saved events: %v", ev.saved)
	}

	// The bar must finish before the saved message is printed so the
	// final frame and its newline precede the follow-up output and can
	// never be duplicated underneath it (the double-bar-line bug).
	doneIdx, savedIdx := -1, -1
	for i, evName := range ev.events {
		switch evName {
		case "done":
			doneIdx = i
		case "saved:" + testFileName:
			savedIdx = i
		}
	}
	if doneIdx == -1 || savedIdx == -1 {
		t.Fatalf("missing events: %v", ev.events)
	}
	if doneIdx > savedIdx {
		t.Errorf("bar Done() must run before Saved: events %v", ev.events)
	}
}

func TestChunkedDownloadResumesPartialChunk(t *testing.T) {
	data := bytes.Repeat([]byte("resume!"), 48*1024)
	srv := rangeServer(data)
	defer srv.Close()

	rec := &eventRecorder{}
	d := testDownloader(t, rec, 4, 1)
	output := filepath.Join(d.OutputDir, testFileName)

	// Pre-download half of chunk 2 as if a previous run was interrupted.
	start, _ := chunkBounds(int64(len(data)), 4, 2)
	partial := data[start : start+int64(len(data))/8]
	if err := os.WriteFile(output+".part2", partial, 0o644); err != nil {
		t.Fatal(err)
	}

	completed, err := d.Download(srv.URL)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	if !completed {
		t.Fatal("expected completed=true after resume")
	}
	if got := mustReadFile(t, output); !bytes.Equal(got, data) {
		t.Fatalf("resumed file corrupt: got %d bytes, want %d", len(got), len(data))
	}
}

func TestChunkedDownloadRejectsFullBodyForRange(t *testing.T) {
	// Server advertises ranges but serves a full 200 body for Range GETs:
	// the old behavior would merge corrupted chunks.
	data := bytes.Repeat([]byte("x"), 512*1024)
	srv := ignoreRangeServer(data)
	defer srv.Close()

	d := testDownloader(t, &eventRecorder{}, 4, 1)

	_, err := d.Download(srv.URL)
	if err == nil {
		t.Fatal("expected an error when a server answers 200 to a Range request")
	}
	var httpErr *apperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTPError with status 200, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(d.OutputDir, testFileName)); !os.IsNotExist(statErr) {
		t.Error("output file must not exist after a rejected range response")
	}
}

func TestChunkedDownloadAcceptsMatchingContentRange(t *testing.T) {
	data := bytes.Repeat([]byte("y"), 768*1024)
	srv := rangeServer(data) // ServeContent emits correct Content-Range
	defer srv.Close()

	d := testDownloader(t, &eventRecorder{}, 4, 1)
	if _, err := d.Download(srv.URL); err != nil {
		t.Fatalf("Download failed on matching Content-Range: %v", err)
	}
}

func TestAlreadyCompleteChunked(t *testing.T) {
	data := bytes.Repeat([]byte("z"), 256*1024)
	srv := rangeServer(data)
	defer srv.Close()

	rec := &eventRecorder{}
	d := testDownloader(t, rec, 4, 1)

	if _, err := d.Download(srv.URL); err != nil {
		t.Fatalf("first Download failed: %v", err)
	}
	completed, err := d.Download(srv.URL)
	if err != nil {
		t.Fatalf("second Download failed: %v", err)
	}
	if completed {
		t.Error("expected completed=false when the file is already complete")
	}
	ev := rec.snapshot()
	if ev.complete != testFileName {
		t.Errorf("AlreadyComplete got %q, want %q", ev.complete, testFileName)
	}
	if len(ev.saved) != 1 {
		t.Errorf("Saved must be reported only by the first run: %v", ev.saved)
	}
}

func TestChunkDownloadRetriesTransientFailure(t *testing.T) {
	data := bytes.Repeat([]byte("r"), 1024*1024)
	srv := fragServer(data)
	defer srv.Close()

	rec := &eventRecorder{}
	d := testDownloader(t, rec, 4, 2)

	completed, err := d.Download(srv.URL)
	if err != nil {
		t.Fatalf("Download should recover after a 503 chunk: %v", err)
	}
	if !completed {
		t.Fatal("expected completed=true")
	}
	if got := mustReadFile(t, filepath.Join(d.OutputDir, testFileName)); !bytes.Equal(got, data) {
		t.Fatal("file corrupt after retry recovery")
	}
	ev := rec.snapshot()
	if len(ev.retries) == 0 {
		t.Error("expected a Retry report")
	}
}

// ---------------------------------------------------------------------------
// Single-stream downloads
// ---------------------------------------------------------------------------

func TestStreamDownload(t *testing.T) {
	data := bytes.Repeat([]byte("stream data; "), 5000)
	srv := noRangeServer(data)
	defer srv.Close()

	rec := &eventRecorder{}
	d := testDownloader(t, rec, 4, 1)

	completed, err := d.Download(srv.URL)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	if !completed {
		t.Fatal("expected completed=true")
	}
	if got := mustReadFile(t, filepath.Join(d.OutputDir, testFileName)); !bytes.Equal(got, data) {
		t.Fatalf("streamed file differs: got %d bytes, want %d", len(got), len(data))
	}
}

func TestStreamDownloadResumesWithRange(t *testing.T) {
	// The server honors open-ended Range requests but does not advertise
	// Accept-Ranges, so the downloader streams single-stream and resumes.
	data := bytes.Repeat([]byte("S"), 300*1024)
	srv := quietRangeServer(data)
	defer srv.Close()

	rec := &eventRecorder{}
	d := testDownloader(t, rec, 4, 1)
	output := filepath.Join(d.OutputDir, testFileName)

	// Pretend a previous run saved the first half.
	if err := os.WriteFile(output, data[:len(data)/2], 0o644); err != nil {
		t.Fatal(err)
	}

	completed, err := d.Download(srv.URL)
	if err != nil {
		t.Fatalf("resumed stream Download failed: %v", err)
	}
	if !completed {
		t.Fatal("expected completed=true after resume")
	}
	if got := mustReadFile(t, output); !bytes.Equal(got, data) {
		t.Fatalf("resumed stream corrupt: got %d bytes, want %d", len(got), len(data))
	}
}

func TestStreamDownloadRejectsResumeWithoutRangeSupport(t *testing.T) {
	data := bytes.Repeat([]byte("N"), 100*1024)
	srv := noRangeServer(data)
	defer srv.Close()

	d := testDownloader(t, &eventRecorder{}, 4, 1)
	output := filepath.Join(d.OutputDir, testFileName)

	// A partial file from a previous run: the server cannot serve ranges,
	// so a resume would duplicate bytes. The downloader must refuse.
	if err := os.WriteFile(output, data[:len(data)/2], 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := d.Download(srv.URL)
	if err == nil {
		t.Fatal("expected an error when resuming against a non-range server")
	}
	var httpErr *apperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTPError with status 200, got: %v", err)
	}
	// The partial file must be left untouched (no append corruption).
	if got := mustReadFile(t, output); !bytes.Equal(got, data[:len(data)/2]) {
		t.Error("partial file was modified after a refused resume")
	}
}

// ---------------------------------------------------------------------------
// Unit tests: helpers
// ---------------------------------------------------------------------------

func TestVerifyContentRange(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		start   int64
		end     int64
		wantErr bool
	}{
		{"match", "bytes 100-199/1000", 100, 199, false},
		{"wrong start", "bytes 200-299/1000", 100, 199, true},
		{"wrong end", "bytes 100-399/1000", 100, 199, true},
		{"no total", "bytes 100-199/*", 100, 199, false},
		{"garbage tolerated", "unit test", 100, 199, false},
		{"missing slash tolerated", "bytes 100-199", 100, 199, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyContentRange(tc.header, tc.start, tc.end)
			if (err != nil) != tc.wantErr {
				t.Errorf("verifyContentRange(%q) err=%v, wantErr=%v", tc.header, err, tc.wantErr)
			}
		})
	}
}

func TestResolveFilename(t *testing.T) {
	mkResp := func(disposition string) *http.Response {
		h := http.Header{}
		if disposition != "" {
			h.Set("Content-Disposition", disposition)
		}
		return &http.Response{Header: h}
	}

	cases := []struct {
		resp *http.Response
		url  string
		want string
	}{
		{mkResp(`attachment; filename="archive.tar.gz"`), "https://x/y/ignored", "archive.tar.gz"},
		{mkResp(""), "https://example.com/dir/file.zip", "file.zip"},
		{mkResp(""), "https://example.com/dir/%20space%20file.bin", " space file.bin"},
		{mkResp(""), "https://example.com/dir/trailing/", "trailing"},
		// Path-less URLs now fall back to download_<timestamp> instead of
		// resolving to the scheme or host ("https:", "example.com:8080").
		{mkResp(""), "https:///", "download_"},
		{mkResp(""), "https://example.com", "download_"},
		{mkResp(""), "", "download_"},
	}
	for i, tc := range cases {
		got := resolveFilename(tc.resp, tc.url)
		if tc.want == "download_" {
			if !strings.HasPrefix(got, "download_") {
				t.Errorf("case %d: got %q, want a fallback download_N name", i, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("case %d: got %q, want %q", i, got, tc.want)
		}
	}
}

func TestMergeFiles(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "merged.out")
	parts := []string{"alpha\n", "bravo\n", "charlie\n"}
	var tmp []string
	for i, p := range parts {
		name := filepath.Join(dir, fmt.Sprintf("part%d", i))
		if err := os.WriteFile(name, []byte(p), 0o644); err != nil {
			t.Fatal(err)
		}
		tmp = append(tmp, name)
	}

	if err := mergeFiles(dst, tmp); err != nil {
		t.Fatalf("mergeFiles: %v", err)
	}
	if got := mustReadFile(t, dst); string(got) != strings.Join(parts, "") {
		t.Errorf("merged content wrong: %q", got)
	}
	for _, name := range tmp {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Errorf("part file not removed: %s", name)
		}
	}
}
