package server

import (
	"archive/zip"
	"doc-converter/pkg/converter" // <-- Add this import
	"doc-converter/pkg/queue"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync" // <-- Add this import

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type ConversionRequest struct {
	URLs     []string `json:"urls"`
	Selector string   `json:"selector"`
}

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	rabbitMQClient *queue.RabbitMQClient
	// This map will store active WebSocket connections, keyed by job ID.
	clients      = make(map[string]*websocket.Conn)
	clientsMutex = &sync.Mutex{}
)

// This function listens for results from the workers
func listenForResults() {
	amqpURL := os.Getenv("AMQP_URL")
	if amqpURL == "" {
		amqpURL = "amqp://guest:guest@rabbitmq:5672/"
	}

	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		log.Fatalf("Result listener failed to connect to RabbitMQ: %v", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("Result listener failed to open a channel: %v", err)
	}
	defer ch.Close()

	resultsExchange := "results_fanout"
	err = ch.ExchangeDeclare(
		resultsExchange, // name
		"fanout",        // type
		true,            // durable
		false,           // auto-deleted
		false,           // internal
		false,           // no-wait
		nil,             // arguments
	)
	if err != nil {
		log.Fatalf("Result listener failed to declare exchange: %v", err)
	}

	q, err := ch.QueueDeclare(
		"",    // name (let RabbitMQ generate a random, temporary name)
		false, // durable
		true,  // delete when unused
		true,  // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		log.Fatalf("Result listener failed to declare queue: %v", err)
	}

	err = ch.QueueBind(
		q.Name,          // queue name
		"",              // routing key
		resultsExchange, // exchange
		false,
		nil,
	)
	if err != nil {
		log.Fatalf("Result listener failed to bind queue: %v", err)
	}

	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		true,   // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	if err != nil {
		log.Fatalf("Result listener failed to register a consumer: %v", err)
	}

	log.Println("INFO: Result listener is running...")
	for d := range msgs {
		var summary converter.Summary
		if err := json.Unmarshal(d.Body, &summary); err != nil {
			log.Printf("ERROR: Failed to unmarshal result summary: %v", err)
			continue
		}

		log.Printf("INFO: Received completion summary for job %s", summary.DownloadID)

		clientsMutex.Lock()
		// Find the client associated with this job ID
		if conn, ok := clients[summary.DownloadID]; ok {
			// Prepare the final message for the UI
			finalResponse := map[string]interface{}{
				"status":       "completed",
				"summary":      summary,
				"download_url": fmt.Sprintf("/api/download/%s", summary.DownloadID),
			}
			// Send the completion message
			if err := conn.WriteJSON(finalResponse); err != nil {
				log.Printf("ERROR: Failed to write completion to WebSocket for job %s: %v", summary.DownloadID, err)
			}
			// Clean up the connection from the map
			delete(clients, summary.DownloadID)
		}
		clientsMutex.Unlock()
	}
}

func conversionHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("INFO: Received new conversion request")
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ERROR: Failed to upgrade connection: %v", err)
		return
	}
	// Note: We don't close the connection here anymore with defer.
	// It stays open to receive the final result.

	_, msg, err := conn.ReadMessage()
	if err != nil {
		log.Printf("ERROR: Failed to read message from client: %v", err)
		return
	}

	var req ConversionRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		log.Printf("ERROR: Invalid request format: %v", err)
		conn.WriteMessage(websocket.TextMessage, []byte("Error: Invalid request format"))
		return
	}

	downloadID := uuid.New().String()

	// Register the client connection before publishing the job
	clientsMutex.Lock()
	clients[downloadID] = conn
	clientsMutex.Unlock()

	// Ensure we clean up if the client disconnects prematurely
	conn.SetCloseHandler(func(code int, text string) error {
		log.Printf("INFO: WebSocket closed for job %s with code %d", downloadID, code)
		clientsMutex.Lock()
		delete(clients, downloadID)
		clientsMutex.Unlock()
		return nil
	})

	job := &queue.ConversionJob{
		URLs:       req.URLs,
		Selector:   req.Selector,
		DownloadID: downloadID,
	}

	if err := rabbitMQClient.PublishJob(job); err != nil {
		log.Printf("ERROR: Failed to publish job to queue: %v", err)
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Failed to queue job"))
		return
	}

	log.Printf("INFO: Job %s queued successfully", downloadID)

	// Send an initial "log" message back to the UI, not the final response.
	initialLog := map[string]interface{}{
		"log":   fmt.Sprintf("Job successfully queued with ID: %s. Waiting for worker...", downloadID),
		"level": "info",
	}

	if err := conn.WriteJSON(initialLog); err != nil {
		log.Printf("ERROR: Failed to write queue confirmation to WebSocket: %v", err)
	}
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/download/")
	if id == "" {
		http.Error(w, "Missing download ID", http.StatusBadRequest)
		return
	}

	dirPath := filepath.Join("tmp", "downloads", id)
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"", id))

	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return err
		}
		zipFile, err := zipWriter.Create(relPath)
		if err != nil {
			return err
		}
		fsFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fsFile.Close()
		_, err = io.Copy(zipFile, fsFile)
		return err
	})
}

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
	// We don't defer the close here, as the result listener needs it.

	// Start the result listener in the background
	go listenForResults()

	http.HandleFunc("/api/convert-ws", conversionHandler)
	http.HandleFunc("/api/download/", downloadHandler)

	log.Println("Starting Go backend server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
