package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/pkg/errors"

	"github.com/concurrentai/concurrentai-core/src/shared/domain"
	"github.com/concurrentai/concurrentai-core/src/shared/messaging"
)

// main : Runs the model executor background service
func main() {
	config := LoadConfig()

	client, closeClient := createPulsarClient(config)
	defer closeClient()

	consumer, closeConsumer := createPulsarConsumer(client, config)
	defer closeConsumer()

	producer, closeProducer := createPulsarProducer(client, config)
	defer closeProducer()

	for {
		if err := HandleNextMessage(consumer, producer, config); err != nil {
			log.Println(err)
		}
	}
}

// createPulsarClient : Create a Pulsar client
func createPulsarClient(config *Config) (messaging.Client, func()) {
	client, err := messaging.NewPulsarClient(config.PulsarURL)
	if err != nil {
		log.Fatal(err)
	}
	return client, func() {
		client.Close()
	}
}

// createPulsarConsumer : Create a Pulsar consumer
func createPulsarConsumer(client messaging.Client, config *Config) (messaging.Consumer, func()) {
	topic := config.TopicName("model-input")
	subscription := config.SubscriptionName("model-input")
	consumer, err := client.CreateConsumer(topic, subscription)
	if err != nil {
		log.Fatal(err)
	}
	return consumer, func() {
		consumer.Close()
	}
}

// createPulsarProducer : Create a Pulsar producer
func createPulsarProducer(client messaging.Client, config *Config) (messaging.Producer, func()) {
	producer, err := client.CreateProducer(config.TopicName("model-response"))
	if err != nil {
		log.Fatal(err)
	}
	return producer, func() {
		producer.Close()
	}
}

// HandleNextMessage : Execute a model request and forward the response
func HandleNextMessage(consumer messaging.Consumer, producer messaging.Producer, config *Config) error {
	defer func() {
		if err := recover(); err != nil {
			log.Printf("panic occurred: %s", err)
		}
	}()

	payload, err := consumer.Receive()
	if err != nil {
		return errors.Wrap(err, "failed to read rendezvous message from consumer")
	}

	var message *domain.RendezvousMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return errors.Wrap(err, "failed to parse rendezvous message")
	}

	if err := setModelResponse(message, config); err != nil {
		return errors.Wrap(err, "failed to get model response")
	}

	payload, err = json.Marshal(message)
	if err != nil {
		return errors.Wrap(err, "failed to encode model response")
	}

	if err := producer.Send(payload); err != nil {
		return errors.Wrap(err, "failed to send rendezvous message with model response")
	}

	log.Println("published message: " + string(payload))
	return nil
}

// setModelResponse : Sets model response values on a RendezvousMessage struct
func setModelResponse(message *domain.RendezvousMessage, config *Config) error {
	request := []byte(message.RequestData)

	message.SetModelRequestStart(time.Now())
	response, err := http.Post(config.ModelEndpoint, "application/json", bytes.NewBuffer(request))
	if err != nil {
		return errors.Wrap(err, "error calling model endpoint")
	}
	message.SetModelRequestStop(time.Now())
	defer response.Body.Close()

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return errors.Wrap(err, "error reading model response")
	}

	message.ResponseModelID = config.ModelID
	message.ResponseData = fmt.Sprintf("{ \"results\": %s }", string(body))

	return nil
}
