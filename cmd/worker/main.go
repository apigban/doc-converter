package main

import (
	"doc-converter/pkg/converter"
	"doc-converter/pkg/queue"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	amqpURL := os.Getenv("AMQP_URL")
	if amqpURL == "" {
		amqpURL = "amqp://guest:guest@rabbitmq:5672/"
	}

	conn, err := amqp.Dial(amqpURL)
	failOnError(err, "Failed to connect to RabbitMQ")
	defer conn.Close()

	ch, err := conn.Channel()
	failOnError(err, "Failed to open a channel")
	defer ch.Close()

	q, err := ch.QueueDeclare(
		queue.ConversionQueue, // name
		true,                  // durable
		false,                 // delete when unused
		false,                 // exclusive
		false,                 // no-wait
		nil,                   // arguments
	)
	failOnError(err, "Failed to declare a queue")

	// Set prefetch count to 1 to ensure that the worker only receives one message at a time.
	// This way, if a worker crashes, the message is not lost and can be redelivered to another worker.
	err = ch.Qos(
		1,     // prefetch count
		0,     // prefetch size
		false, // global
	)
	failOnError(err, "Failed to set QoS")

	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		false,  // auto-ack is false. We will manually acknowledge messages.
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	failOnError(err, "Failed to register a consumer")

	var forever chan struct{}

	go func() {
		for d := range msgs {
			log.Printf("Received a message: %s", d.Body)
			var job queue.ConversionJob
			if err := json.Unmarshal(d.Body, &job); err != nil {
				log.Printf("ERROR: Failed to unmarshal job: %v", err)
				d.Reject(false)
				continue
			}

			c, err := converter.NewConverterForJob(job.DownloadID)
			if err != nil {
				log.Printf("ERROR: Failed to create new converter for job %s: %v", job.DownloadID, err)
				d.Reject(false)
				continue
			}

			resultsChan, summaryChan := c.Convert(job.URLs, job.Selector)

			for range resultsChan {
				// Drain results
			}

			summary := <-summaryChan
			log.Printf("INFO: Conversion finished for job %s. Successful: %d, Failed: %d",
				job.DownloadID, summary.Successful, summary.Failed)

			// *** ADD THIS PART ***
			// Publish the final summary back for the backend to hear
			publishResults(ch, &summary)
			// *********************

			// Acknowledge the message now that the work is done.
			d.Ack(false)
		}
	}()

	log.Printf(" [*] Waiting for messages. To exit press CTRL+C")

	// Wait for termination signal
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	log.Println("Shutting down worker...")
	<-forever
}

func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %v", msg, err)
	}
}

func publishResults(ch *amqp.Channel, summary *converter.Summary) {
	resultsExchange := "results_fanout"
	err := ch.ExchangeDeclare(
		resultsExchange, // name
		"fanout",        // type
		true,            // durable
		false,           // auto-deleted
		false,           // internal
		false,           // no-wait
		nil,             // arguments
	)
	if err != nil {
		log.Printf("ERROR: Failed to declare results exchange: %v", err)
		return
	}

	body, err := json.Marshal(summary)
	if err != nil {
		log.Printf("ERROR: Failed to marshal summary: %v", err)
		return
	}

	err = ch.Publish(
		resultsExchange, // exchange
		"",              // routing key (not used for fanout)
		false,           // mandatory
		false,           // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		})

	if err != nil {
		log.Printf("ERROR: Failed to publish result: %v", err)
	} else {
		log.Printf("INFO: Published completion status for job %s", summary.DownloadID)
	}
}
