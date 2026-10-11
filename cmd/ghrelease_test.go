package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// hexSHA256 returns the lowercase hex digest of a blob.
func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// swapGHAPI points the release-lookup base at url for the duration of the
// test.
func swapGHAPI(t *testing.T, url string) {
	t.Helper()
	prev := ghAPIBase
	ghAPIBase = url
	t.Cleanup(func() { ghAPIBase = prev })
}

// archiveName is the GoReleaser asset name for the running platform, with
// the requested archive extension.
func archiveName(ext string) string {
	return fmt.Sprintf("dlm_%s_%s.%s", runtime.GOOS, runtime.GOARCH, ext)
}

// makeTarGz builds a tar.gz archive containing a single "dlm" entry.
func makeTarGz(t *testing.T, binary []byte) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "dlm",
		Mode: 0o755,
		Size: int64(len(binary)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// makeZip builds a zip archive containing a single "dlm" entry.
func makeZip(t *testing.T, binary []byte) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	zf, err := zw.Create("dlm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zf.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeReleaseServer serves the /releases/latest JSON for a release with one
// archive asset and its .sha256 sidecar, both locally hosted. Sidecar
// content can be overridden for tamper tests.
type fakeReleaseServer struct {
	archive        []byte
	archiveName    string
	tag            string
	sidecarContent string

	srv *httptest.Server
}

func newFakeReleaseServer(t *testing.T, archiveName string, tag string, archive []byte, sidecar string) *fakeReleaseServer {
	t.Helper()
	if sidecar == "" {
		sidecar = hexSHA256(archive) + "  " + archiveName + "\n"
	}
	f := &fakeReleaseServer{archive: archive, archiveName: archiveName, tag: tag, sidecarContent: sidecar}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + repoSlug + "/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ghRelease{
				TagName: f.tag,
				Name:    "dlm " + f.tag,
				Body:    "release notes",
				Assets: []ghAsset{
					{
						Name:               f.archiveName,
						Size:               int64(len(f.archive)),
						BrowserDownloadURL: f.srv.URL + "/" + f.archiveName,
					},
					{
						Name:               f.archiveName + ".sha256",
						Size:               int64(len(f.sidecarContent)),
						BrowserDownloadURL: f.srv.URL + "/" + f.archiveName + ".sha256",
					},
				},
			})
		case "/" + f.archiveName + ".sha256":
			_, _ = w.Write([]byte(f.sidecarContent))
		case "/" + f.archiveName:
			_, _ = w.Write(f.archive)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// fetchFake is a shorthand: swap the API base to the fake server and fetch.
func fetchFake(t *testing.T, f *fakeReleaseServer) *ghRelease {
	t.Helper()
	swapGHAPI(t, f.srv.URL)
	rel, err := fetchLatestRelease(repoSlug)
	if err != nil {
		t.Fatalf("fetchLatestRelease: %v", err)
	}
	return rel
}

func TestFetchLatestReleaseParses(t *testing.T) {
	f := newFakeReleaseServer(t, archiveName("tar.gz"), "v1.2.3", []byte{1}, "")
	rel := fetchFake(t, f)
	if rel.TagName != "v1.2.3" {
		t.Errorf("TagName = %q, want v1.2.3", rel.TagName)
	}
	if len(rel.Assets) != 2 {
		t.Errorf("assets = %d, want 2 (archive + checksum sidecar)", len(rel.Assets))
	}
}

func TestFetchLatestReleaseRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	swapGHAPI(t, srv.URL)

	_, err := fetchLatestRelease(repoSlug)
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("expected a rate-limit error, got %v", err)
	}
}

func TestAssetForPlatformMatches(t *testing.T) {
	release := &ghRelease{
		TagName: "v1.0.0",
		Assets: []ghAsset{
			{Name: "dlm_darwin_arm64.tar.gz"},
			{Name: "dlm_windows_amd64.zip"},
		},
	}
	if _, err := release.assetForPlatform(); err != nil {
		// This host's platform is not in the fixture; install it and retry.
		release.Assets = append(release.Assets, ghAsset{Name: archiveName("tar.gz")})
		if _, err := release.assetForPlatform(); err != nil {
			t.Fatalf("assetForPlatform: %v", err)
		}
	}
}

func TestChecksumForFormats(t *testing.T) {
	payloadSum := hexSHA256([]byte("payload"))
	layouts := []string{
		payloadSum + "  target.tar.gz\n", // "hex  name" list format
		payloadSum + "\n",                // bare single-line file
		"# comment\n" + payloadSum + "  target.tar.gz\n",
		payloadSum + "  other.tar.gz\n" + payloadSum + "  target.tar.gz\n",
	}
	for i, contents := range layouts {
		contents := contents
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(contents))
		}))
		release := &ghRelease{
			TagName: "v1",
			Assets: []ghAsset{{
				Name:               "target.tar.gz.sha256",
				BrowserDownloadURL: srv.URL + "/sums",
			}},
		}
		got, err := release.checksumFor("target.tar.gz")
		if err != nil {
			t.Errorf("case %d: checksumFor: %v", i, err)
			srv.Close()
			continue
		}
		if got != payloadSum {
			t.Errorf("case %d: checksum = %q, want %q", i, got, payloadSum)
		}
		srv.Close()
	}
}

func TestInstallAssetReplacesBinary(t *testing.T) {
	newBin := []byte("new binary v1")
	archive := makeTarGz(t, newBin)
	f := newFakeReleaseServer(t, archiveName("tar.gz"), "v1.0.0", archive, "")

	rel := fetchFake(t, f)
	exe := exeWithContent(t, []byte("old binary v0"))

	if err := installAsset(rel, exe); err != nil {
		t.Fatalf("installAsset: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newBin) {
		t.Errorf("binary = %q, want %q", got, newBin)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %o, want 755", info.Mode().Perm())
	}

	// No staging files may be left behind.
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Errorf("leftover files after update: %v", entries)
	}
}

func TestInstallAssetHandlesZip(t *testing.T) {
	newBin := []byte("zipped new binary")
	archive := makeZip(t, newBin)
	f := newFakeReleaseServer(t, archiveName("zip"), "v9.9.9", archive, "")

	rel := fetchFake(t, f)
	exe := exeWithContent(t, []byte("old"))

	if err := installAsset(rel, exe); err != nil {
		t.Fatalf("installAsset: %v", err)
	}
	if got, _ := os.ReadFile(exe); !bytes.Equal(got, newBin) {
		t.Errorf("binary = %q, want %q", got, newBin)
	}
}

func TestInstallAssetRejectsChecksumMismatch(t *testing.T) {
	archive := makeTarGz(t, []byte("tamper target"))
	f := newFakeReleaseServer(t, archiveName("tar.gz"), "v1.0.0", archive,
		hexSHA256([]byte("wrong payload"))+"  "+archiveName("tar.gz")+"\n")

	rel := fetchFake(t, f)
	exe := exeWithContent(t, []byte("old binary"))

	err := installAsset(rel, exe)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected a sha256 mismatch error, got %v", err)
	}
	// The existing binary must be untouched, no staging leftovers.
	if got, _ := os.ReadFile(exe); !bytes.Equal(got, []byte("old binary")) {
		t.Errorf("running binary was modified: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Errorf("leftover files after failed update: %v", entries)
	}
}

// exeWithContent writes a fake installed binary and returns its path.
func exeWithContent(t *testing.T, content []byte) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "dlm")
	if err := os.WriteFile(exe, content, 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}
