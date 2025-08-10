//go:build !integration
// +build !integration

package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
)

//--- CLI behavior tests (Cobra) ---//

// captureOutput runs f(), capturing all output writen to stdout/stderr.
func captureOutput(f func()) (string, string) {
	oldOut := os.Stdout
	oldErr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	outC := make(chan string)
	errC := make(chan string)

	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, rOut)
		outC <- buf.String()
	}()
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, rErr)
		errC <- buf.String()
	}()

	f()

	wOut.Close()
	wErr.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr

	outStr := <-outC
	errStr := <-errC

	return outStr, errStr
}

// Must be run single-threaded: Cobra uses global state!
func TestCLI_Convert_Successful(t *testing.T) {
	// Set up a mock HTTP server
	htmlContent := `<!DOCTYPE html>
<html>
<body>
    <main>
        <h1>Hello World</h1>
        <p>This is a test paragraph.</p>
    </main>
</body>
</html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(htmlContent))
	}))
	defer server.Close()

	urlsPath := "testurls.txt"
	content := []byte(server.URL + "\n")
	err := os.WriteFile(urlsPath, content, 0644)
	assert.NoError(t, err, "could not create temp urls file")
	t.Cleanup(func() { os.Remove(urlsPath) })

	// Define a temporary output directory for this test
	outputDir := "test_output_success"
	t.Cleanup(func() { os.RemoveAll(outputDir) })

	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{
		"doc-converter",
		"convert",
		"--file", urlsPath,
		"--selector", "main",
		"--output", outputDir, // Specify output directory
	}

	// Reset package-global flags before CLI use
	filePath = ""
	selector = ""
	output = ""

	_, stderr := captureOutput(func() { // Ignore stdout as it's not asserted
		defer func() {
			_ = recover() // Catch potential panics from log.Fatalf
		}()
		Execute()
	})

	// Assertions for successful execution
	assert.NotContains(t, stderr, "Error", "expected no errors in stderr")

	// Verify a timestamped run directory is created
	var runDir string
	newDirs, err := os.ReadDir(outputDir)
	assert.NoError(t, err, "failed to read output directory after conversion")
	assert.Len(t, newDirs, 1, "expected exactly one run directory in output")
	runDir = filepath.Join(outputDir, newDirs[0].Name())
	assert.DirExists(t, runDir, "run directory should exist")

	// Verify the correct number of .md files are created
	files, err := os.ReadDir(runDir)
	assert.NoError(t, err, "failed to read run directory")
	assert.Len(t, files, 1, "expected exactly one markdown file")
	assert.True(t, filepath.Ext(files[0].Name()) == ".md", "expected file to have .md extension")

	// Verify the content of the sample output file
	outputFilePath := filepath.Join(runDir, files[0].Name())
	outputContent, err := os.ReadFile(outputFilePath)
	assert.NoError(t, err, "failed to read output markdown file")

	// Split content into frontmatter and body
	parts := bytes.SplitN(outputContent, []byte("---"), 3)
	assert.Len(t, parts, 3, "expected content to have YAML frontmatter delimited by '---'")

	frontmatterRaw := parts[1]
	body := string(bytes.TrimSpace(parts[2]))

	// Parse and verify frontmatter
	var metadata map[string]interface{}
	err = yaml.Unmarshal(frontmatterRaw, &metadata)
	assert.NoError(t, err, "failed to parse YAML frontmatter")

	assert.Contains(t, metadata, "source", "frontmatter should contain source URL")
	assert.Equal(t, server.URL, metadata["source"], "frontmatter source URL mismatch")
	assert.Contains(t, metadata, "retrieved_at", "frontmatter should contain retrieval timestamp")

	// Verify body content
	expectedBody := `# Hello World

This is a test paragraph.`
	assert.Equal(t, expectedBody, body, "markdown body content mismatch")
}

func TestCLI_Convert_MissingFlag_Error(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	testCases := []struct {
		name string
		args []string
	}{
		{
			name: "missing file flag",
			args: []string{"doc-converter", "convert", "--selector", "main"},
		},
		{
			name: "missing selector flag",
			args: []string{"doc-converter", "convert", "--file", "urls.txt"},
		},
		{
			name: "missing both flags",
			args: []string{"doc-converter", "convert"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = tc.args
			filePath = ""
			selector = ""
			output = "" // Reset output flag

			originalExitFunc := exitFunc
			var actualExitCode int
			var exitCalled bool
			mockExit := func(code int) {
				actualExitCode = code
				exitCalled = true
				// In a real scenario, os.Exit() would terminate.
				// For testing, we just capture the code and prevent exit.
			}
			exitFunc = mockExit
			defer func() { exitFunc = originalExitFunc }()

			stdout, stderr := captureOutput(Execute)

			assert.True(t, exitCalled, "exitFunc should have been called")
			assert.Equal(t, 1, actualExitCode, "exit code should be 1")

			assert.Contains(t, stdout, "Usage:", "should print help for missing flag to stdout")
			assert.Contains(t, stderr, "Error: Both --file and --selector must be provided", "should report missing flags to stderr")
		})
	}
}

func TestCLI_Convert_InvalidFilePath_Error(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{
		"doc-converter",
		"convert",
		"--file", "/nonexistent/path/to/urls.txt",
		"--selector", "main",
	}

	filePath = ""
	selector = ""
	output = "" // Reset output flag

	originalExitFunc := exitFunc
	var actualExitCode int
	var exitCalled bool
	mockExit := func(code int) {
		actualExitCode = code
		exitCalled = true
	}
	exitFunc = mockExit
	defer func() { exitFunc = originalExitFunc }()

	_, stderr := captureOutput(Execute)

	assert.True(t, exitCalled, "exitFunc should have been called")
	assert.Equal(t, 1, actualExitCode, "exit code should be 1")
	assert.Contains(t, stderr, "Error: Input file not found at '/nonexistent/path/to/urls.txt'", "should report file not found error to stderr")
}

//--- Markdown output tests ---//

func TestCLI_Convert_MarkdownOutput_Successful(t *testing.T) {
	// 1. Set up a mock HTTP server
	htmlContent := `<!DOCTYPE html>
<html>
<head>
    <title>Test Page</title>
    <meta name="description" content="This is a test description.">
    <meta name="keywords" content="test, html, mock">
</head>
<body>
    <main>
        <h1>Test Title</h1>
        <p>This is some test content.</p>
        <a href="https://example.com/link">A link</a>
    </main>
</body>
</html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(htmlContent))
	}))
	defer server.Close()

	// 2. Create a temporary urls.txt file
	urlsPath := "testurls_markdown.txt"
	content := []byte(server.URL + "\n")
	err := os.WriteFile(urlsPath, content, 0644)
	assert.NoError(t, err, "could not create temp urls file")
	t.Cleanup(func() { os.Remove(urlsPath) })

	// 3. Define a temporary output directory
	outputDir := "test_output_markdown"
	t.Cleanup(func() { os.RemoveAll(outputDir) }) // Ensure cleanup of the output directory

	// Capture initial directories to find the new run directory later
	initialDirs, err := os.ReadDir(outputDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("failed to read initial output directory: %v", err)
	}
	initialDirNames := make(map[string]bool)
	for _, d := range initialDirs {
		if d.IsDir() {
			initialDirNames[d.Name()] = true
		}
	}

	// 4. Execute the convert command
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{
		"doc-converter",
		"convert",
		"--file", urlsPath,
		"--selector", "main",
		"--output", outputDir,
	}

	// Reset package-global flags before CLI use
	filePath = ""
	selector = ""
	output = ""

	_, stderr := captureOutput(func() { // Ignore stdout as it's not asserted
		defer func() {
			_ = recover() // Catch potential panics from log.Fatalf
		}()
		Execute()
	})

	// 5. Verify command execution
	assert.NotContains(t, stderr, "Error", "expected no errors in stderr")

	// 6. Verify a timestamped run directory is created
	var runDir string
	newDirs, err := os.ReadDir(outputDir)
	assert.NoError(t, err, "failed to read output directory after conversion")
	assert.NotEmpty(t, newDirs, "expected new directories in output")

	foundRunDir := false
	for _, d := range newDirs {
		if d.IsDir() && !initialDirNames[d.Name()] {
			// Check if the directory name looks like a timestamp (YYYYMMDDHHMMSS)
			_, parseErr := time.Parse("20060102150405", d.Name())
			if parseErr == nil {
				runDir = filepath.Join(outputDir, d.Name())
				foundRunDir = true
				break
			}
		}
	}
	assert.True(t, foundRunDir, "expected a timestamped run directory to be created")
	assert.DirExists(t, runDir, "run directory should exist")

	// 7. Verify the correct number of .md files are created
	files, err := os.ReadDir(runDir)
	assert.NoError(t, err, "failed to read run directory")
	assert.Len(t, files, 1, "expected exactly one markdown file")
	assert.True(t, filepath.Ext(files[0].Name()) == ".md", "expected file to have .md extension")

	// 8. Verify the content of the sample output file
	outputFilePath := filepath.Join(runDir, files[0].Name())
	outputContent, err := os.ReadFile(outputFilePath)
	assert.NoError(t, err, "failed to read output markdown file")

	// Split content into frontmatter and body
	parts := bytes.SplitN(outputContent, []byte("---"), 3)
	assert.Len(t, parts, 3, "expected content to have YAML frontmatter delimited by '---'")

	frontmatterRaw := parts[1]
	body := string(bytes.TrimSpace(parts[2]))

	// Parse and verify frontmatter
	var metadata map[string]interface{}
	err = yaml.Unmarshal(frontmatterRaw, &metadata)
	assert.NoError(t, err, "failed to parse YAML frontmatter")

	assert.Equal(t, "Test Page", metadata["title"], "frontmatter title mismatch")
	assert.Equal(t, "This is a test description.", metadata["description"], "frontmatter description mismatch")
	assert.Equal(t, "test, html, mock", metadata["keywords"], "frontmatter keywords mismatch")
	assert.Contains(t, metadata, "source", "frontmatter should contain source URL")
	assert.Equal(t, server.URL, metadata["source"], "frontmatter source URL mismatch")

	// Verify body content
	expectedBody := `# Test Title

This is some test content.

[A link](https://example.com/link)`
	assert.Equal(t, expectedBody, body, "markdown body content mismatch")
}
