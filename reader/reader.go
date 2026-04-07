package reader

import (
	"bufio"
	"os"
	"strings"

	apperrors "dlm/errors"
)

type LinkFile struct {
	Links []string
}

// ReadLinks reads a .txt file and returns a list of URLs
// Skips empty lines and comments (lines starting with #)
func ReadLinks(filePath string) (*LinkFile, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, apperrors.WrapFileError(filePath, "open", err)
	}
	// Schedule the close to happen
	// at the very end of this file
	defer file.Close()

	var links []string
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		links = append(links, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, apperrors.WrapFileError(filePath, "read", err)
	}

	return &LinkFile{Links: links}, nil
}
