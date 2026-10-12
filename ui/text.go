package ui

import (
	"net/url"
	"strings"
)

// CleanFileName renders percent-encoded URL names as human names, dropping
// any query string ("FAB%20-%2012" → "FAB - 12.mkv").
func CleanFileName(name string) string {
	if i := strings.IndexByte(name, '?'); i >= 0 {
		name = name[:i]
	}
	if decoded, err := url.PathUnescape(name); err == nil && decoded != "" {
		name = decoded
	}
	return strings.TrimSpace(name)
}
