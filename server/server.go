package server

import (
	"archive/zip"
	"doc-converter/pkg/queue"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// ConversionRequest is the structure of the JSON request from the client
type ConversionRequest struct {
	URLs     []string `json:"urls"`
	Selector string   `json:"selector"`
}

var (
	upgrader    = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			// TODO: Restrict this to your frontend's origin in production
			return true
		},
	}
	rabbitMQClient *queue.RabbitMQClient
)

func conversionHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("INFO: Received new conversion request")
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
		conn.WriteMessage(websocket.TextMessage, []byte("Error: Invalid request format"))
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInvalidFramePayloadData, "Invalid JSON format"))
		return
	}

	if len(req.URLs) == 0 || req.Selector == "" {
		log.Printf("ERROR: Missing URLs or selector in request")
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInvalidFramePayloadData, "URLs and selector are required"))
		return
	}

	// Generate a unique ID for this conversion job
	downloadID := uuid.New().String()

	// Create the job payload
	job := &queue.ConversionJob{
		URLs:       req.URLs,
		Selector:   req.Selector,
		DownloadID: downloadID,
	}

	// Publish the job to RabbitMQ
	if err := rabbitMQClient.PublishJob(job); err != nil {
		log.Printf("ERROR: Failed to publish job to queue: %v", err)
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Failed to queue job"))
		return
	}

	log.Printf("INFO: Job %s queued successfully", downloadID)

	// Create a response map to inform the client
	response := map[string]interface{}{
		"status":       "queued",
		"message":      "Your conversion job has been queued successfully.",
		"download_id":  downloadID,
		"download_url": fmt.Sprintf("/api/download/%s", downloadID),
	}

	if err := conn.WriteJSON(response); err != nil {
		log.Printf("ERROR: Failed to write queue confirmation to WebSocket: %v", err)
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
	// defer os.RemoveAll(dirPath) // TODO: Temporary solution to Premature Directory Deletion
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
	amqpURL := os.Getenv("AMQP_URL")
	if amqpURL == "" {
		amqpURL = "amqp://guest:guest@rabbitmq:5672/"
	}

	var err error
	rabbitMQClient, err = queue.NewRabbitMQClient(amqpURL)
	if err != nil {
		log.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer rabbitMQClient.Close()

	// The frontend is now served by Caddy, so we only need the API handlers here.
	// The Caddy reverse proxy will route requests to these handlers.
	http.HandleFunc("/api/convert-ws", conversionHandler)
	http.HandleFunc("/api/download/", downloadHandler)

	log.Println("Starting Go backend server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
