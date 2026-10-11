package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/arash-jafarpour/dlm/ui"
	"github.com/spf13/cobra"
)

// repoSlug is the "owner/name" GitHub repository releases are downloaded from.
const repoSlug = "arash-jafarpour/dlm"

// semverRe extracts the base semantic version from a version string, ignoring a
// leading "v" and any git-describe suffix (e.g. "v0.3.0-2-gabc123-dirty").
var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// updateHTTPClient downloads release assets. No client-side timeout: a slow
// link may legitimately take minutes to pull a release archive; canceling
// the command cancels the download.
var updateHTTPClient = &http.Client{Timeout: 0}

func newUpdateCmd() *cobra.Command {
	var check bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update dlm to the latest release",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(check)
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "only check for a newer version")

	return cmd
}

func runUpdate(checkOnly bool) error {
	current, err := currentSemver()
	if err != nil {
		return err
	}

	release, err := fetchLatestRelease(repoSlug)
	if err != nil {
		return err
	}

	latest, err := semver.NewVersion(release.TagName)
	if err != nil {
		return fmt.Errorf("could not parse release tag %q: %w", release.TagName, err)
	}

	if !latest.GreaterThan(current) {
		fmt.Printf("dlm is already up to date (v%s)\n", current)
		return nil
	}

	if checkOnly {
		fmt.Printf("new version available: v%s (current v%s)\n", latest, current)
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not locate the dlm executable: %w", err)
	}

	fmt.Printf("updating dlm v%s -> v%s ...\n", current, latest)
	if err := installAsset(release, exe); err != nil {
		return fmt.Errorf(
			"update failed: %w\n"+
				"if dlm lives in a system directory, run with elevated permissions or reinstall",
			err,
		)
	}

	fmt.Printf("successfully updated dlm to v%s\n", latest)
	if release.Body != "" {
		fmt.Printf("\n%s\n", release.Body)
	}

	return nil
}

// currentSemver returns the base semantic version of the running build, or an
// error if it is a development build that cannot be updated or compared.
func currentSemver() (*semver.Version, error) {
	base := semverRe.FindString(version)
	if base == "" {
		return nil, errors.New(
			"this is a development build; reinstall from a release to enable self-update",
		)
	}
	v, err := semver.NewVersion(base)
	if err != nil {
		return nil, fmt.Errorf("could not parse version %q: %w", version, err)
	}
	return v, nil
}

// progressHash hashes the raw artifact stream on the way through and feeds
// the progress bar, so every byte is counted exactly where it is read.
type progressHash struct {
	hash hash.Hash
	bar  *ui.Bar // nil when there is nothing displayable
}

func (p *progressHash) Write(b []byte) (int, error) {
	n, err := p.hash.Write(b)
	if p.bar != nil {
		p.bar.Add(n)
	}
	return n, err
}

// installAsset fetches the release asset for the running platform, verifies
// its SHA256 against the GoReleaser sidecar, extracts the `dlm` binary and
// atomically replaces exe. The existing binary is never touched before the
// new archive has passed its checksum.
func installAsset(release *ghRelease, exe string) error {
	asset, err := release.assetForPlatform()
	if err != nil {
		return err
	}
	want, err := release.checksumFor(asset.Name)
	if err != nil {
		return err
	}

	resp, err := updateHTTPClient.Get(asset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", asset.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download %s: GitHub returned %s", asset.Name, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(exe), ".dlm-update-*")
	if err != nil {
		return fmt.Errorf("failed to create update staging file: %w", err)
	}
	tmpName := tmp.Name()

	var bar *ui.Bar
	if asset.Size > 0 {
		bar = ui.NewBarWithOffset(asset.Size, asset.Name, 0)
	}

	downloaded := &progressHash{hash: sha256.New(), bar: bar}
	buf := &bytes.Buffer{}
	_, copyErr := io.Copy(buf, io.TeeReader(resp.Body, downloaded))
	if bar != nil {
		bar.Done()
	}
	if copyErr != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to download %s: %w", asset.Name, copyErr)
	}

	if got := hex.EncodeToString(downloaded.hash.Sum(nil)); got != want {
		_ = os.Remove(tmpName)
		return fmt.Errorf(
			"sha256 mismatch for %s (got %s, want %s); possible tampering or partial download",
			asset.Name, got, want,
		)
	}

	if err := extractBinary(buf, asset.Name, tmp); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to set permissions on the new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to finalize the new binary: %w", err)
	}

	return replaceBinary(tmpName, exe)
}

// replaceBinary moves the staged binary in place. POSIX allows rename to
// replace a running executable; Windows does not, so there the old binary
// is renamed away first and best-effort deleted afterwards.
func replaceBinary(tmpPath, exe string) error {
	var oldPath string
	if runtime.GOOS == "windows" {
		oldPath = exe + ".old"
		if err := os.Rename(exe, oldPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("could not move the running executable out of the way: %w", err)
		}
	}
	if err := os.Rename(tmpPath, exe); err != nil {
		if oldPath != "" {
			_ = os.Rename(oldPath, exe) // restore the old binary
		}
		_ = os.Remove(tmpPath)
		return fmt.Errorf("could not replace the executable: %w", err)
	}
	if oldPath != "" {
		_ = os.Remove(oldPath)
	}
	return nil
}

// extractBinary pulls the "dlm" file entry out of the downloaded archive
// into dst. The format handler is selected from the asset name so tests can
// exercise either archive type from any OS.
func extractBinary(buf *bytes.Buffer, assetName string, dst *os.File) error {
	if strings.HasSuffix(assetName, ".zip") {
		return extractBinaryFromZip(buf, dst)
	}
	return extractBinaryFromTarGz(buf, dst)
}

func extractBinaryFromTarGz(buf *bytes.Buffer, dst *os.File) error {
	gz, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return fmt.Errorf("asset is not a valid tar.gz: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("archive contains no dlm binary")
		}
		if err != nil {
			return fmt.Errorf("asset is not a valid tar.gz: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "dlm" {
			if _, err := io.Copy(dst, tr); err != nil {
				return fmt.Errorf("failed to extract the dlm binary: %w", err)
			}
			return nil
		}
	}
}

func extractBinaryFromZip(buf *bytes.Buffer, dst *os.File) error {
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		return fmt.Errorf("asset is not a valid zip: %w", err)
	}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || filepath.Base(zf.Name) != "dlm" {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("failed to read the dlm entry: %w", err)
		}
		_, copyErr := io.Copy(dst, rc)
		_ = rc.Close()
		if copyErr != nil {
			return fmt.Errorf("failed to extract the dlm binary: %w", copyErr)
		}
		return nil
	}
	return errors.New("archive contains no dlm binary")
}
