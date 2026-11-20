package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var bot_serial string = "b11fceaf-5173-4190-be6e-9c37ef3dc238"
var device_type string = "ls1ok3"
var resource string = "Lgsd"

var client mqtt.Client

func publishXML(msg XMLMsg) {
	if activeXmppBot != nil && activeXmppBot.IsReady() {
		if err := activeXmppBot.SendCommand(msg); err != nil {
			log.Printf("[PUBLISH-XMPP] Error: %v", err)
		}
		return
	}

	if client != nil && client.IsConnected() {
		topic := fmt.Sprintf("iot/p2p/%s/x/x/x/%s/%s/%s/q/x/x", msg.cmdName, bot_serial, device_type, resource)
		xmlPayload, _ := msg.cmdOpts.XmlIndent("", " ", "ctl")
		xmlString := string(xmlPayload)
		pub(client, topic, xmlString)
	} else {
		log.Printf("[PUBLISH] Neither XMPP bot nor MQTT broker connected. Command %s not sent.", msg.cmdName)
	}
}

func subscribe() {
	sub(client, "iot/atr/#")
	sub(client, fmt.Sprintf("iot/p2p/+/%s/%s/%s/#", bot_serial, device_type, resource))
}

func main() {
	keepAlive := make(chan os.Signal, 1)
	signal.Notify(keepAlive, os.Interrupt, syscall.SIGTERM)

	log.Println("=======================================================")
	log.Println(" Starting Ecovacs Privacy Controller Server")
	log.Println(" - Lookup Server (:8007) for OZMO 930 HTTP discovery")
	log.Println(" - TLS XMPP Server (:5223) for OZMO 930 command & map")
	log.Println(" - REST API (:8000) for controls and map view")
	log.Println("=======================================================")

	// Start HTTP Lookup Server (:8007) in background
	go startLookupServer()

	// Start TLS XMPP Server (:5223) in background
	go startXmppServer()

	// Optional legacy MQTT client (if ENABLE_MQTT=1)
	enableMQTT := os.Getenv("ENABLE_MQTT")
	if enableMQTT == "true" || enableMQTT == "1" {
		go func() {
			log.Println("Connecting to legacy MQTT broker...")
			client = connect()
			subscribe()
			publishXML(GetVersion)
			publishXML(GetBrushLifeSpan)
		}()
	}

	// Start REST API (:8000)
	setupApi()

	<-keepAlive
	log.Println("Shutting down server...")
}
