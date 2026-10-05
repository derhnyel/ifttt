package comments

import (
	"path/filepath"
	"strings"
)

var commentFormats = map[string]commentDelimiter{}

func registerCommentFormat(ext string, delimiter commentDelimiter) {
	registryMu.Lock()
	commentFormats[strings.ToLower(ext)] = delimiter
	registryMu.Unlock()
}

// FormatComment uses the same registered language delimiters as extraction.
// Explicit line-comment registrations take precedence over built-in styles.
func FormatComment(path, text string) string {
	registryMu.RLock()
	delimiter, ok := commentFormats[strings.ToLower(filepath.Ext(path))]
	registryMu.RUnlock()
	if !ok {
		delimiter.open = "//"
		if filenameExtractor(path) != nil {
			delimiter.open = "#"
		}
	}
	line := delimiter.open + " " + text
	if delimiter.close != "" {
		line += " " + delimiter.close
	}
	return line
}
