package main

import (
	"crypto/tls"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var messagePubHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	fmt.Printf("Received message: %s from topic: %s\n", msg.Payload(), msg.Topic())
}

func sub(client mqtt.Client) {
	topic := "iot/atr/#"
	token := client.Subscribe(topic, 0, messagePubHandler)
	token.Wait()
	fmt.Printf("Subscribed to topic %s", topic)
}

var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	fmt.Println("Connected")
	sub(client)
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	fmt.Printf("Connect lost: %v", err)
}

func main() {
	keepAlive := make(chan os.Signal)
	signal.Notify(keepAlive, os.Interrupt, syscall.SIGTERM)

	// All your code

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
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}
	opts.SetConnectRetry(true)
	<-keepAlive
}
