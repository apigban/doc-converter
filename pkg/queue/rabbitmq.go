package queue

import (
	"context"
	"encoding/json"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	ConversionQueue = "conversion_jobs"
)

// ConversionJob defines the structure for a job message.
type ConversionJob struct {
	URLs       []string `json:"urls"`
	Selector   string   `json:"selector"`
	DownloadID string   `json:"downloadId"`
}

// RabbitMQClient holds the connection and channel for RabbitMQ.
type RabbitMQClient struct {
	Conn    *amqp.Connection
	Channel *amqp.Channel
}

// NewRabbitMQClient creates a new RabbitMQ client and declares the necessary queue.
func NewRabbitMQClient(amqpURL string) (*RabbitMQClient, error) {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}

	_, err = ch.QueueDeclare(
		ConversionQueue, // name
		true,            // durable
		false,           // delete when unused
		false,           // exclusive
		false,           // no-wait
		nil,             // arguments
	)
	if err != nil {
		return nil, err
	}

	return &RabbitMQClient{
		Conn:    conn,
		Channel: ch,
	}, nil
}

// PublishJob sends a conversion job to the queue.
func (c *RabbitMQClient) PublishJob(job *ConversionJob) error {
	body, err := json.Marshal(job)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = c.Channel.PublishWithContext(ctx,
		"",              // exchange
		ConversionQueue, // routing key
		false,           // mandatory
		false,           // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		})
	if err != nil {
		return err
	}

	log.Printf("INFO: Published job with DownloadID: %s", job.DownloadID)
	return nil
}

// Close closes the RabbitMQ channel and connection.
func (c *RabbitMQClient) Close() {
	if c.Channel != nil {
		c.Channel.Close()
	}
	if c.Conn != nil {
		c.Conn.Close()
	}
}
