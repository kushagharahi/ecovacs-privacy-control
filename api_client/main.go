package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var (
	bot_serial   string = "b11fceaf-5173-4190-be6e-9c37ef3dc238"
	device_type  string = "ls1ok3"
	resource     string = "Lgsd"
	client       mqtt.Client
	serverIPFlag string
	robotIPFlag  string
)

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
		return
	}

	log.Printf("[PUBLISH] Neither XMPP bot nor MQTT broker connected. Command %s not sent.", msg.cmdName)
}

func subscribe() {
	if client == nil || !client.IsConnected() {
		return
	}
	sub(client, "iot/atr/#")
	sub(client, fmt.Sprintf("iot/p2p/+/%s/%s/%s/#", bot_serial, device_type, resource))
}

func main() {
	var protocolFlag string
	flag.StringVar(&protocolFlag, "protocol", "", "Target protocol: 'xmpp' (OZMO 930), 'mqtt' (Deebot 900/901), or 'all' (default)")
	flag.StringVar(&protocolFlag, "mode", "", "Alias for -protocol")
	flag.StringVar(&serverIPFlag, "server-ip", "", "External/advertised server IP for robot connection (env: SERVER_IP or CONTROLLER_IP)")
	flag.StringVar(&robotIPFlag, "robot-ip", "", "Robot IP address for telnet commands (env: ROBOT_IP)")
	flag.Parse()

	// Environment variable fallback
	if protocolFlag == "" {
		protocolFlag = os.Getenv("PROTOCOL")
	}
	if protocolFlag == "" {
		protocolFlag = os.Getenv("MODE")
	}
	if protocolFlag == "" {
		protocolFlag = "all"
	}
	if serverIPFlag == "" {
		serverIPFlag = os.Getenv("SERVER_IP")
	}
	if serverIPFlag == "" {
		serverIPFlag = os.Getenv("CONTROLLER_IP")
	}
	if robotIPFlag == "" {
		robotIPFlag = os.Getenv("ROBOT_IP")
	}
	protocol := strings.ToLower(strings.TrimSpace(protocolFlag))

	keepAlive := make(chan os.Signal, 1)
	signal.Notify(keepAlive, os.Interrupt, syscall.SIGTERM)

	hupChan := make(chan os.Signal, 1)
	signal.Notify(hupChan, syscall.SIGHUP)
	go func() {
		for range hupChan {
			log.Println("[RELOAD] Received SIGHUP! Re-executing binary in-place without container restart...")
			binary, err := os.Executable()
			if err == nil {
				syscall.Exec(binary, os.Args, os.Environ())
			}
		}
	}()

	log.Println("=======================================================")
	log.Printf(" Starting Ecovacs Privacy Controller (Protocol: %s)", protocol)
	log.Println(" - REST API (:8000) for controls and map view")
	log.Println("=======================================================")

	enableXmpp := (protocol == "all" || protocol == "xmpp")
	enableMqtt := (protocol == "all" || protocol == "mqtt")

	if enableXmpp {
		log.Println("[XMPP] Starting Lookup Server (:8007) and TLS XMPP Server (:5223)...")
		go startLookupServer()
		go startXmppServer()
	} else {
		log.Println("[XMPP] XMPP services disabled by protocol flag.")
	}

	if enableMqtt {
		log.Println("[MQTT] Starting legacy MQTT client connection...")
		go func() {
			client = connect()
			subscribe()
			publishXML(GetVersion)
			publishXML(GetBrushLifeSpan)
		}()
	} else {
		log.Println("[MQTT] MQTT services disabled by protocol flag.")
	}

	// Start REST API (:8000)
	setupApi()

	<-keepAlive
	log.Println("Shutting down server...")
}
