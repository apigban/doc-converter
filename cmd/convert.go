package cmd

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2" // Added for YAML marshalling
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
	selector := viper.GetString("selector")

	var urls []string
	for _, line := range lines {
		url := string(bytes.TrimSpace(line))
		if url != "" {
			urls = append(urls, url)
		}
	}
	log.Printf("INFO: Loaded %d URLs for processing from %s", len(urls), file)

	successCount := 0
	errorCount := 0

	for _, url := range urls {

		log.Printf("INFO: Fetching URL: %s", url)
		content, err := processURL(url, selector)
		if err != nil {
			log.Printf("ERROR: Failed to process %s: %v", url, err)
			errorCount++
			continue
		}

		// Fetch the document again to get the title and metadata
		resp, err := http.Get(url)
		if err != nil {
			log.Printf("ERROR: Failed to fetch URL for metadata %s: %v", url, err)
			errorCount++
			continue
		}
		defer resp.Body.Close()

		doc, err := goquery.NewDocumentFromReader(resp.Body)
		if err != nil {
			log.Printf("ERROR: Failed to parse HTML for metadata %s: %v", url, err)
			errorCount++
			continue
		}

		// Extract metadata
		pageMetadata := getMetadata(doc, url)
		pageMetadata["retrieved_at"] = time.Now().Format(time.RFC3339)

		// Convert content to Markdown
		markdownContent := htmlToMarkdown(content)

		// Marshal metadata to YAML
		yamlBytes, yamlErr := yaml.Marshal(pageMetadata)
		if yamlErr != nil {
			log.Printf("ERROR: Failed to marshal YAML for %s: %v", url, yamlErr)
			errorCount++
			continue
		}

		// Combine frontmatter and markdown content
		var buf bytes.Buffer
		buf.WriteString("---\n")
		buf.Write(yamlBytes)
		buf.WriteString("---\n\n")
		buf.WriteString(markdownContent)
		finalContent := buf.Bytes()
		filename := getSanitizedTitle(doc, url) + ".md"

		filePath := filepath.Join(outputDir, filename)
		err = os.WriteFile(filePath, finalContent, 0644)
		if err != nil {
			log.Printf("ERROR: Failed to write file %s: %v", filePath, err)
			errorCount++
			continue
		}

		log.Printf("INFO: Successfully converted: %s -> %s", url, filePath)
		successCount++
	}
}

// processURL fetches the HTML content at the given URL and extracts elements matching the provided selector.
// On error or if no selection is found, returns a descriptive error including the URL and selector.
func processURL(url string, selector string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch URL %s: HTTP status %d", url, resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read HTML for %s: %v", url, err)
	}

	content := doc.Find(selector)
	if content.Length() == 0 {
		return "", fmt.Errorf("could not find content in %s using selector '%s'", url, selector)
	}

	htmlContent, err := content.Html()
	if err != nil {
		return "", fmt.Errorf("failed to get HTML content for selector '%s': %v", selector, err)
	}
	return htmlContent, nil
}

// getSanitizedTitle extracts the title from the document or uses the fallback URL
// to create a valid filename
func getSanitizedTitle(doc *goquery.Document, fallbackURL string) string {
	title := strings.TrimSpace(doc.Find("title").Text())
	if title == "" {
		// Use the last part of the URL as fallback
		parts := strings.Split(fallbackURL, "/")
		if len(parts) > 0 {
			title = parts[len(parts)-1]
			if title == "" && len(parts) > 1 {
				title = parts[len(parts)-2]
			}
		}
		if title == "" {
			title = "untitled"
		}
	}
	return SanitizeFilename(title)
}

// sanitizeFilename converts a string to a valid filename by:
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

// getMetadata extracts relevant metadata from the goquery document.
func getMetadata(doc *goquery.Document, url string) map[string]interface{} {
	metadata := make(map[string]interface{})

	// Source URL
	metadata["source"] = url

	// Title
	title := strings.TrimSpace(doc.Find("title").Text())
	if title != "" {
		metadata["title"] = title
	}

	// Description from meta tag
	doc.Find("meta[name='description']").Each(func(i int, s *goquery.Selection) {
		if desc, exists := s.Attr("content"); exists {
			metadata["description"] = desc
		}
	})

	// Keywords from meta tag
	doc.Find("meta[name='keywords']").Each(func(i int, s *goquery.Selection) {
		if keywords, exists := s.Attr("content"); exists {
			metadata["keywords"] = keywords
		}
	})

	return metadata
}

// htmlToMarkdown converts a given HTML string to Markdown.
// This is a simplified conversion and might need a more robust library for complex HTML.
func htmlToMarkdown(htmlContent string) string {
	// This is a simplified conversion. For robust conversion, a dedicated library like
	// "github.com/JohannesKaufmann/html-to-markdown" would be used.
	// For the purpose of this task, we'll implement basic conversions.

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		log.Printf("ERROR: Failed to parse HTML for markdown conversion: %v", err)
		return ""
	}

	var markdownBuilder strings.Builder

	// Create a selection from the document
	var selection *goquery.Selection
	body := doc.Find("body")
	if body.Length() > 0 {
		selection = body
	} else {
		selection = doc.Selection
	}

	// Find all relevant elements and process them
	selection.Find("h1, h2, h3, h4, h5, h6, p, a").Each(func(i int, s *goquery.Selection) {
		tagName := goquery.NodeName(s)
		text := strings.TrimSpace(s.Text())

		if text == "" {
			return
		}

		switch tagName {
		case "h1":
			markdownBuilder.WriteString("# " + text + "\n\n")
		case "h2":
			markdownBuilder.WriteString("## " + text + "\n\n")
		case "h3":
			markdownBuilder.WriteString("### " + text + "\n\n")
		case "h4":
			markdownBuilder.WriteString("#### " + text + "\n\n")
		case "h5":
			markdownBuilder.WriteString("##### " + text + "\n\n")
		case "h6":
			markdownBuilder.WriteString("###### " + text + "\n\n")
		case "p":
			markdownBuilder.WriteString(text + "\n\n")
		case "a":
			href, exists := s.Attr("href")
			if exists {
				markdownBuilder.WriteString(fmt.Sprintf("[%s](%s)", text, href))
			} else {
				markdownBuilder.WriteString(text)
			}
		}
	})

	// If no specific tags found, just use the text content
	if markdownBuilder.Len() == 0 {
		text := strings.TrimSpace(selection.Text())
		if text != "" {
			markdownBuilder.WriteString(text)
		}
	}

	// Clean up multiple newlines and trim overall whitespace
	result := regexp.MustCompile(`\n\n+`).ReplaceAllString(markdownBuilder.String(), "\n\n")
	return strings.TrimSpace(result)
}
