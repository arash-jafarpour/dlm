//go:build windows

package cmd

import "os"

// Windows has no flock(2); single-instance locking is best-effort and
// unsupported here, so these are no-ops.
func flockExclusive(f *os.File) error { return nil }

func funlock(f *os.File) error { return nil }
