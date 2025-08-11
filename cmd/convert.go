package cmd

import (
	"bytes"
	"doc-converter/pkg/converter"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// exitFunc allows os.Exit to be replaced for testing
var exitFunc = os.Exit

// convertCmd represents the convert command
var convertCmd = &cobra.Command{
	Use:   "convert",
	Short: "Extract content from web pages using CSS selectors",
	Long: `doc-converter is a command-line tool that fetches web pages and extracts clean, readable text content using CSS selectors.

It supports batch processing of URLs from files and provides configurable content selection via CSS selectors.
The tool is designed to help developers and content creators easily extract web content for documentation, archiving, or further processing.

Example usage:
  doc-converter convert --file urls.txt --selector "#main-content"
  doc-converter convert --file urls.txt --selector ".content"`,
	Run: runConvert,
}

// Wire up flags for --file and --selector, bind to viper
var (
	filePath string
	selector string
	output   string
)

func init() {
	rootCmd.AddCommand(convertCmd)

	convertCmd.Flags().StringVarP(&filePath, "file", "f", "", "Path to the text file containing URLs")
	convertCmd.Flags().StringVarP(&selector, "selector", "s", "", "CSS selector for the main content")
	convertCmd.Flags().StringVarP(&output, "output", "o", "output", "Custom parent directory for output files")

	viper.BindPFlag("file", convertCmd.Flags().Lookup("file"))
	viper.BindPFlag("selector", convertCmd.Flags().Lookup("selector"))
	viper.BindPFlag("output", convertCmd.Flags().Lookup("output"))
}

func runConvert(cmd *cobra.Command, args []string) {
	// Validate required inputs
	file := viper.GetString("file")
	sel := viper.GetString("selector")

	if file == "" || sel == "" {
		cmd.Help()
		fmt.Fprintln(os.Stderr, "Error: Both --file and --selector must be provided (via flag or config)")
		exitFunc(1)
		return // return after exitFunc for testability, though exitFunc will terminate
	}

	// File existence and readability check
	if stat, err := os.Stat(file); err != nil || stat.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: Input file not found at '%s'\n", file)
		exitFunc(1)
		return // return after exitFunc for testability, though exitFunc will terminate
	}

	// Create unique, timestamped directory for this execution run
	parentOutput := viper.GetString("output")
	outputDir, err := createRunOutputDir(parentOutput)
	if err != nil {
		log.Fatalf("Error creating output directory: %v", err)
	}
	log.Printf("INFO: Created output directory: %s", outputDir)

	data, err := os.ReadFile(file)
	if err != nil {
		log.Fatalf("Error reading file: %v", err)
	}

	lines := bytes.Split(data, []byte{'\n'})

	var urls []string
	for _, line := range lines {
		url := string(bytes.TrimSpace(line))
		if url != "" {
			urls = append(urls, url)
		}
	}
	log.Printf("INFO: Loaded %d URLs for processing from %s", len(urls), file)

	c, err := converter.NewConverter(outputDir)
	if err != nil {
		log.Fatalf("Error creating converter: %v", err)
	}
	resultsChan, summaryChan := c.Convert(urls, sel)

	// Process results as they come in
	for result := range resultsChan {
		if result.IsSuccess {
			// The file is already written by the converter. We just log it.
			log.Printf("INFO: Successfully converted: %s -> %s", result.URL, filepath.Join(c.OutputDir, result.FileName))
		} else {
			log.Printf("ERROR: Failed to process %s: %s", result.URL, result.Error)
		}
	}

	// Wait for and print the final summary
	summary := <-summaryChan
	log.Printf("INFO: Conversion complete.")
	log.Printf("INFO: Total URLs: %d", summary.TotalURLs)
	log.Printf("INFO: Successful: %d", summary.Successful)
	log.Printf("INFO: Failed: %d", summary.Failed)
	if summary.Failed > 0 {
		log.Printf("INFO: Failed URLs: %s", strings.Join(summary.FailedURLs, ", "))
	}
	log.Printf("INFO: Total processing time: %s", summary.ProcessingTime)

}

// createRunOutputDir creates a unique, timestamped directory for each execution run
// with format YYYYMMDDHHMMSS. If directory exists, it removes and recreates it.
func createRunOutputDir(parentDir string) (string, error) {
	// Generate timestamp in format: 20060102150405
	timestamp := time.Now().Format("20060102150405")
	dirName := timestamp
	fullPath := filepath.Join(parentDir, dirName)

	// Ensure parent directory exists
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create parent directory: %w", err)
	}

	// If directory already exists, remove it to ensure clean state
	if _, err := os.Stat(fullPath); err == nil {
		if err := os.RemoveAll(fullPath); err != nil {
			return "", fmt.Errorf("failed to remove existing directory: %w", err)
		}
	}

	// Create the directory
	if err := os.MkdirAll(fullPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create run directory: %w", err)
	}

	return fullPath, nil
}
