package cmd

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/blang/semver"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
	"github.com/spf13/cobra"
)

// repoSlug is the "owner/name" GitHub repository releases are downloaded from.
const repoSlug = "arash-jafarpour/dlm"

// semverRe extracts the base semantic version from a version string, ignoring a
// leading "v" and any git-describe suffix (e.g. "v0.3.0-2-gabc123-dirty").
var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

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

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator: &selfupdate.SHA2Validator{},
	})
	if err != nil {
		return fmt.Errorf("failed to initialize updater: %w", err)
	}

	latest, found, err := updater.DetectLatest(repoSlug)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}
	if !found {
		fmt.Printf("no releases found for %s\n", repoSlug)
		return nil
	}

	if !latest.Version.GT(current) {
		fmt.Printf("dlm is already up to date (v%s)\n", current)
		return nil
	}

	if checkOnly {
		fmt.Printf("new version available: v%s (current v%s)\n", latest.Version, current)
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not locate the dlm executable: %w", err)
	}

	fmt.Printf("updating dlm v%s -> v%s ...\n", current, latest.Version)
	if err := updater.UpdateTo(latest, exe); err != nil {
		return fmt.Errorf(
			"update failed: %w\n"+
				"if dlm lives in a system directory, run with elevated permissions or reinstall",
			err,
		)
	}

	fmt.Printf("successfully updated dlm to v%s\n", latest.Version)
	if latest.ReleaseNotes != "" {
		fmt.Printf("\n%s\n", latest.ReleaseNotes)
	}

	return nil
}

// currentSemver returns the base semantic version of the running build, or an
// error if it is a development build that cannot be compared or updated.
func currentSemver() (semver.Version, error) {
	base := semverRe.FindString(version)
	if base == "" {
		return semver.Version{}, errors.New(
			"this is a development build; reinstall from a release to enable self-update",
		)
	}
	return semver.Parse(base)
}
