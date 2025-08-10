package main

import (
	"doc-converter/cmd"
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- Existing utility test, unchanged ---
func TestSanitizeFilename(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{"Normal with spaces", "My Document Title", "my_document_title"},
		{"With special characters", "File*With/Illegal\\Chars", "filewithillegalchars"},
		{"Mixed case", "Another Document", "another_document"},
		{"With underscores", "a_b_c", "a_b_c"},
		{"Empty string", "", ""},
		{"Just spaces", "   ", "___"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := cmd.SanitizeFilename(tc.input)
			assert.Equal(t, tc.expected, actual, "For input '%s'", tc.input)
		})
	}
}
