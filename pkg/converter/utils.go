package converter

import (
	"regexp"
	"strings"
)

// SanitizeFilename converts a string to a valid filename by:
// 1. Converting to lowercase
// 2. Replacing spaces with underscores
// 3. Removing any characters that aren't alphanumeric or underscores
func SanitizeFilename(s string) string {
	// Convert to lowercase
	s = strings.ToLower(s)

	// Replace spaces with underscores
	s = strings.ReplaceAll(s, " ", "_")

	// Remove any character that is not alphanumeric or underscore
	reg := regexp.MustCompile("[^a-z0-9_]+")
	s = reg.ReplaceAllString(s, "")

	return s
}
