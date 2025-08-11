// Package converter provides the core logic for the doc-converter tool.
package converter

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"gopkg.in/yaml.v2"
)

const (
	maxBodySize = 5 * 1024 * 1024 // 5MB
	httpTimeout = 5 * time.Second
)

// Result holds the outcome of a single URL conversion.
type Result struct {
	URL       string `json:"url"`
	FileName  string `json:"fileName"`
	Content   []byte `json:"-"` // Exclude raw content from logs. Kept for CLI compatibility.
	Error     string `json:"error,omitempty"`
	IsSuccess bool   `json:"isSuccess"`
}

// Summary provides a final overview of the batch conversion.
type Summary struct {
	TotalURLs      int      `json:"totalUrls"`
	Successful     int      `json:"successful"`
	Failed         int      `json:"failed"`
	FailedURLs     []string `json:"failedUrls"`
	ProcessingTime string   `json:"processingTime"`
	DownloadID     string   `json:"downloadId,omitempty"` // ID for the final zip file
}

// Converter holds the configuration and methods for conversion.
type Converter struct {
	Client     *http.Client
	OutputDir  string
	DownloadID string
}

// NewConverterForJob creates a new Converter for a background job.
// It uses the downloadID to create a unique, predictable directory for output files.
func NewConverterForJob(downloadID string) (*Converter, error) {
	if downloadID == "" {
		return nil, fmt.Errorf("downloadID cannot be empty for a job-based conversion")
	}

	outputDir := filepath.Join("tmp", "downloads", downloadID)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	return &Converter{
		Client: &http.Client{
			Timeout: httpTimeout,
		},
		OutputDir:  outputDir,
		DownloadID: downloadID,
	}, nil
}

// NewConverterForCLI creates a new Converter for a command-line execution.
// It uses a user-provided directory path for the output.
func NewConverterForCLI(outputDir string) (*Converter, error) {
	if outputDir == "" {
		return nil, fmt.Errorf("output directory must be specified for CLI conversion")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	return &Converter{
		Client: &http.Client{
			Timeout: httpTimeout,
		},
		OutputDir: outputDir,
		// DownloadID is not relevant for CLI runs.
	}, nil
}

// Convert orchestrates the fetching, parsing, and conversion of multiple URLs concurrently.
func (c *Converter) Convert(urls []string, selector string) (<-chan Result, <-chan Summary) {
	resultsChan := make(chan Result)
	summaryChan := make(chan Summary)

	go func() {
		startTime := time.Now()
		var wg sync.WaitGroup
		var successCount, errorCount int
		var failedURLs []string
		var mu sync.Mutex // To protect shared summary variables

		for _, u := range urls {
			wg.Add(1)
			go func(u string) {
				defer wg.Done()

				// URL Validation
				isPublic, err := c.isPublicURL(u)
				if err != nil {
					mu.Lock()
					errorCount++
					failedURLs = append(failedURLs, u)
					mu.Unlock()
					resultsChan <- Result{URL: u, Error: fmt.Sprintf("URL validation failed: %v", err), IsSuccess: false}
					return
				}
				if !isPublic {
					mu.Lock()
					errorCount++
					failedURLs = append(failedURLs, u)
					mu.Unlock()
					resultsChan <- Result{URL: u, Error: "SSRF attack suspected: URL resolves to a non-public IP", IsSuccess: false}
					return
				}

				content, err := c.processURL(u, selector)
				if err != nil {
					log.Printf("ERROR: Failed to process %s: %v", u, err)
					mu.Lock()
					errorCount++
					failedURLs = append(failedURLs, u)
					mu.Unlock()
					resultsChan <- Result{URL: u, Error: err.Error(), IsSuccess: false}
					return
				}

				// Fetch the document again to get the title and metadata
				resp, err := c.Client.Get(u)
				if err != nil {
					log.Printf("ERROR: Failed to fetch URL for metadata %s: %v", u, err)
					mu.Lock()
					errorCount++
					failedURLs = append(failedURLs, u)
					mu.Unlock()
					resultsChan <- Result{URL: u, Error: fmt.Sprintf("failed to fetch URL for metadata: %v", err), IsSuccess: false}
					return
				}
				defer resp.Body.Close()

				// Limit response body for metadata parsing as well
				resp.Body = http.MaxBytesReader(nil, resp.Body, maxBodySize)
				doc, err := goquery.NewDocumentFromReader(resp.Body)
				if err != nil {
					log.Printf("ERROR: Failed to parse HTML for metadata %s: %v", u, err)
					mu.Lock()
					errorCount++
					failedURLs = append(failedURLs, u)
					mu.Unlock()
					resultsChan <- Result{URL: u, Error: fmt.Sprintf("failed to parse HTML for metadata: %v", err), IsSuccess: false}
					return
				}

				// Extract metadata
				pageMetadata := c.getMetadata(doc, u)
				pageMetadata["retrieved_at"] = time.Now().Format(time.RFC3339)

				// Convert content to Markdown
				markdownContent := c.htmlToMarkdown(content)

				// Marshal metadata to YAML
				yamlBytes, err := yaml.Marshal(pageMetadata)
				if err != nil {
					log.Printf("ERROR: Failed to marshal YAML for %s: %v", u, err)
					mu.Lock()
					errorCount++
					failedURLs = append(failedURLs, u)
					mu.Unlock()
					resultsChan <- Result{URL: u, Error: fmt.Sprintf("failed to marshal YAML: %v", err), IsSuccess: false}
					return
				}

				// Combine frontmatter and markdown content
				var buf bytes.Buffer
				buf.WriteString("---\n")
				buf.Write(yamlBytes)
				buf.WriteString("---\n\n")
				buf.WriteString(markdownContent)
				finalContent := buf.Bytes()
				filename := c.getSanitizedTitle(doc, u) + ".md"

				// Write the file to the configured output directory
				filePath := filepath.Join(c.OutputDir, filename)
				if err := os.WriteFile(filePath, finalContent, 0644); err != nil {
					mu.Lock()
					errorCount++
					failedURLs = append(failedURLs, u)
					mu.Unlock()
					resultsChan <- Result{URL: u, Error: fmt.Sprintf("failed to write file: %v", err), IsSuccess: false}
					return
				}

				mu.Lock()
				successCount++
				mu.Unlock()
				resultsChan <- Result{
					URL:       u,
					FileName:  filename,
					Content:   finalContent, // Keep for CLI compatibility for now
					IsSuccess: true,
				}
			}(u)
		}

		wg.Wait()

		close(resultsChan) // Close results channel before sending summary

		summary := Summary{
			TotalURLs:      len(urls),
			Successful:     successCount,
			Failed:         errorCount,
			FailedURLs:     failedURLs,
			ProcessingTime: time.Since(startTime).String(),
			DownloadID:     c.DownloadID,
		}
		summaryChan <- summary
		close(summaryChan)
	}()

	return resultsChan, summaryChan
}

// processURL fetches the HTML content at the given URL and extracts elements matching the provided selector.
// On error or if no selection is found, returns a descriptive error including the URL and selector.
func (c *Converter) processURL(urlStr string, selector string) (string, error) {
	resp, err := c.Client.Get(urlStr)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL %s: %v", urlStr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch URL %s: HTTP status %d", urlStr, resp.StatusCode)
	}

	// Limit response body to 5MB
	resp.Body = http.MaxBytesReader(nil, resp.Body, maxBodySize)

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read HTML for %s: %v", urlStr, err)
	}

	content := doc.Find(selector)
	if content.Length() == 0 {
		return "", fmt.Errorf("could not find content in %s using selector '%s'", urlStr, selector)
	}

	htmlContent, err := content.Html()
	if err != nil {
		return "", fmt.Errorf("failed to get HTML content for selector '%s': %v", selector, err)
	}
	return htmlContent, nil
}

// isPublicURL checks if a URL resolves to a public IP address to prevent SSRF attacks.
// func (c *Converter) isPublicURL(urlStr string) (bool, error) {
// 	parsedURL, err := url.Parse(urlStr)
// 	if err != nil {
// 		return false, err
// 	}

// 	ips, err := net.LookupIP(parsedURL.Hostname())
// 	if err != nil {
// 		return false, err
// 	}

// 	for _, ip := range ips {
// 		if ip.IsLoopback() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
// 			return false, nil // Found a non-public IP
// 		}
// 	}

// 	return true, nil
// }

// getSanitizedTitle extracts the title from the document or uses the fallback URL
// to create a valid filename
func (c *Converter) getSanitizedTitle(doc *goquery.Document, fallbackURL string) string {
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

// getMetadata extracts relevant metadata from the goquery document.
func (c *Converter) getMetadata(doc *goquery.Document, url string) map[string]interface{} {
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
func (c *Converter) htmlToMarkdown(htmlContent string) string {
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
