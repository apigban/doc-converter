package server

import (
	"archive/zip"
	"doc-converter/pkg/converter"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/websocket"
)

// ConversionRequest is the structure of the JSON request from the client
type ConversionRequest struct {
	URLs     []string `json:"urls"`
	Selector string   `json:"selector"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// TODO: Restrict this to your frontend's origin in production
		return true
	},
}

func conversionHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ERROR: Failed to upgrade connection: %v", err)
		return
	}
	defer conn.Close()

	// Read the initial request from the client
	_, msg, err := conn.ReadMessage()
	if err != nil {
		log.Printf("ERROR: Failed to read message from client: %v", err)
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Failed to read request"))
		return
	}

	var req ConversionRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		log.Printf("ERROR: Invalid request format: %v", err)
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInvalidFramePayloadData, "Invalid JSON format"))
		return
	}

	if len(req.URLs) == 0 || req.Selector == "" {
		log.Printf("ERROR: Missing URLs or selector in request")
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInvalidFramePayloadData, "URLs and selector are required"))
		return
	}

	// Instantiate the converter. Passing an empty string for outputDir triggers
	// the creation of a temporary directory for this conversion.
	c, err := converter.NewConverter("")
	if err != nil {
		log.Printf("ERROR: Failed to create new converter: %v", err)
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Failed to initialize converter"))
		return
	}

	resultsChan, summaryChan := c.Convert(req.URLs, req.Selector)

	// Stream results back to the client
	// The converter is now handling the file writing. The server just relays the status.
	for result := range resultsChan {
		if err := conn.WriteJSON(result); err != nil {
			log.Printf("ERROR: Failed to write result to WebSocket: %v", err)
			break // Stop if we can't write to the client
		}
	}

	// Send the final summary, which includes the DownloadID
	summary := <-summaryChan
	if err := conn.WriteJSON(summary); err != nil {
		log.Printf("ERROR: Failed to write summary to WebSocket: %v", err)
	}

	// Send a close message to the client
	err = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	if err != nil {
		log.Printf("ERROR: Failed to write close message: %v", err)
	}
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Extract ID from URL
	id := strings.TrimPrefix(r.URL.Path, "/api/download/")
	if id == "" {
		http.Error(w, "Missing download ID", http.StatusBadRequest)
		return
	}

	// 2. Locate temporary directory
	dirPath := filepath.Join("tmp", "downloads", id)
	defer os.RemoveAll(dirPath) // Defer cleanup
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}

	// 3. Set headers
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"", id))

	// 4. Create zip archive and stream it
	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Create a new file in the zip archive
		// The path in the zip should be relative to the base directory
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return err
		}
		zipFile, err := zipWriter.Create(relPath)
		if err != nil {
			return err
		}

		// Open the file to be zipped
		fsFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fsFile.Close()

		// Copy the file content to the zip archive
		_, err = io.Copy(zipFile, fsFile)
		return err
	})

	if err != nil {
		log.Printf("ERROR: Failed to create zip archive for %s: %v", id, err)
		// Can't set headers anymore, but can try to write an error to the body
		// This may or may not be seen by the client.
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Failed to create zip archive"))
	}
}

// Run starts the web server.
func Run() {
	http.HandleFunc("/api/convert-ws", conversionHandler)
	http.HandleFunc("/api/download/", downloadHandler)

	log.Println("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
