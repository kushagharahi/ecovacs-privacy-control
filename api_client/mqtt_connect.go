package main

import (
	"crypto/tls"
	"fmt"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var messagePubHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	fmt.Printf("Received message: %s from topic: %s\n", msg.Payload(), msg.Topic())
}

func sub(client mqtt.Client, topic string) {
	token := client.Subscribe(topic, 0, messagePubHandler)
	token.Wait()
	fmt.Printf("Subscribed to topic %s\n", topic)
}

func pub(client mqtt.Client, topic string, payload string) {
	token := client.Publish(topic, 0, false, payload)
	token.Wait()
	fmt.Printf("Published message: %s on topic: %s\n", payload, topic)
}

var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	fmt.Println("Connected")
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	fmt.Printf("Connect lost: %v\n", err)
}

func connect() mqtt.Client {
	var broker = "mq-ww.ecouser.net"
	var port = 8883
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("mqtts://%s:%d", broker, port))
	opts.SetTLSConfig(&tls.Config{InsecureSkipVerify: true})
	opts.SetClientID("communication_server")
	opts.SetUsername("server")
	opts.SetDefaultPublishHandler(messagePubHandler)
	opts.OnConnect = connectHandler
	opts.OnConnectionLost = connectLostHandler

	client := mqtt.NewClient(opts)
	token := client.Connect()
	token.Wait()
	opts.SetConnectRetry(true)
	return client
}
