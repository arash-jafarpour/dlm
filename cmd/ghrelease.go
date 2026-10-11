package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// ghAPIBase can be swapped in tests to point at a fake GitHub server.
var ghAPIBase = "https://api.github.com"

// ghHTTPClient is the HTTP client for GitHub queries; the timeout keeps a
// flaky network from hanging `dlm update` forever.
var ghHTTPClient = &http.Client{Timeout: 30 * time.Second}

type ghAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Name    string    `json:"name"`
	Body    string    `json:"body"`
	Assets  []ghAsset `json:"assets"`
}

// fetchLatestRelease queries the GitHub REST API for the most recent
// non-draft release of repoSlug. GITHUB_TOKEN is honored when set, which
// raises the API rate limit for heavy users.
func fetchLatestRelease(repoSlug string) (*ghRelease, error) {
	req, err := http.NewRequest(http.MethodGet, ghAPIBase+"/repos/"+repoSlug+"/releases/latest", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "dlm/"+version)
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach %s: %w", ghAPIBase, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		limited := resp.Header.Get("X-RateLimit-Remaining") == "0"
		if resp.StatusCode == http.StatusForbidden && limited {
			return nil, fmt.Errorf(
				"GitHub API rate limit reached; retry later or set GITHUB_TOKEN",
			)
		}
		return nil, fmt.Errorf("GitHub API returned %s for %s", resp.Status, repoSlug)
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode GitHub release: %w", err)
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("no releases found for %s", repoSlug)
	}
	return &release, nil
}

// assetForPlatform returns the release asset GoReleaser built for the
// running platform: "dlm_<os>_<arch>.tar.gz", or "dlm_<os>_<arch>.zip" on
// Windows (archive format_overrides in .goreleaser.yaml keep this naming).
func (r *ghRelease) assetForPlatform() (ghAsset, error) {
	prefix := fmt.Sprintf("dlm_%s_%s.", runtime.GOOS, runtime.GOARCH)
	for _, a := range r.Assets {
		if strings.HasPrefix(a.Name, prefix) {
			return a, nil
		}
	}
	return ghAsset{}, fmt.Errorf(
		"release %s has no asset for %s/%s", r.TagName, runtime.GOOS, runtime.GOARCH,
	)
}

// validSHA256 reports whether hexStr is a 64-char hex digest (either case).
func validSHA256(hexStr string) bool {
	if len(hexStr) != 64 {
		return false
	}
	for i := 0; i < len(hexStr); i++ {
		c := hexStr[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// checksumFor resolves the GoReleaser split-checksum sidecar
// ("<asset>.sha256") and returns the expected digest for assetName. The
// sidecar uses digests in either "<hex>  <name> (<tool>)" style or as bare
// single-line hex; both are handled.
func (r *ghRelease) checksumFor(assetName string) (string, error) {
	sidecar := assetName + ".sha256"
	var sum *ghAsset
	for i := range r.Assets {
		if r.Assets[i].Name == sidecar {
			sum = &r.Assets[i]
			break
		}
	}
	if sum == nil {
		return "", fmt.Errorf(
			"release %s has no %s sidecar (checksum.split must stay enabled in .goreleaser.yaml)",
			r.TagName, sidecar,
		)
	}

	resp, err := ghHTTPClient.Get(sum.BrowserDownloadURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch %s: %w", sidecar, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch %s: GitHub returned %s", sidecar, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", sidecar, err)
	}

	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		// Lines like "<hex>  <asset>" must name our asset; bare-hex
		// single-line sidecars apply to it by construction.
		if len(fields) >= 2 && !strings.HasSuffix(line, assetName) {
			continue
		}
		if validSHA256(fields[0]) {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf(
		"could not find a valid digest for %s in %s", assetName, sidecar,
	)
}
