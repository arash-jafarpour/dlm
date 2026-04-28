package cmd

import (
	"errors"
	"fmt"

	apperrors "dlm/errors"
	"dlm/ui"
)

func logError(err error) {
	var httpErr *apperrors.HTTPError
	var fileErr *apperrors.FileError
	var chunkErr *apperrors.ChunkError

	switch {
	case errors.As(err, &chunkErr):
		if chunkErr.Index == -1 {
			fmt.Printf("%s %v\n", ui.Red("✗ merge failed:"), chunkErr.Err)
		} else {
			fmt.Printf("%s %v\n", ui.Red(fmt.Sprintf("✗ chunk %d failed:", chunkErr.Index)), chunkErr.Err)
		}
	case errors.As(err, &httpErr):
		if httpErr.StatusCode > 0 {
			fmt.Printf(
				"%s %s: %s\n",
				ui.Red(fmt.Sprintf("✗ HTTP %d for", httpErr.StatusCode)),
				httpErr.URL,
				httpErr.Message,
			)
		} else {
			fmt.Printf("%s %s: %s\n", ui.Red("✗ network error for"), httpErr.URL, httpErr.Message)
		}
	case errors.As(err, &fileErr):
		fmt.Printf(
			"%s %q: %v\n",
			ui.Red(fmt.Sprintf("✗ file %s error on", fileErr.Op)),
			fileErr.Path,
			fileErr.Err,
		)
	default:
		fmt.Printf("%s %v\n", ui.Red("✗ failed:"), err)
	}
}
