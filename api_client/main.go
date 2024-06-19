package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var bot_serial string = "b11fceaf-5173-4190-be6e-9c37ef3dc238"
var device_type string = "ls1ok3"
var resource string = "Lgsd"

var client mqtt.Client

func publishJson(msg Msg) {
	topic := fmt.Sprintf("iot/p2p/%s/x/x/x/%s/%s/%s/p/x/j", msg.cmdName, bot_serial, device_type, resource)
	jsonPayload, _ := json.Marshal(msg.cmdOpts)
	jsonString := string(jsonPayload)
	pub(client, topic, jsonString)
}

func publishXML(msg XMLMsg) {
	topic := fmt.Sprintf("iot/p2p/%s/x/x/x/%s/%s/%s/q/x/x", msg.cmdName, bot_serial, device_type, resource)
	xmlPayload, _ := msg.cmdOpts.XmlIndent("", " ", "ctl")
	xmlString := string(xmlPayload)
	pub(client, topic, xmlString)
}

func subscribe() {
	sub(client, "iot/atr/#")
	sub(client, fmt.Sprintf("iot/p2p/+/%s/%s/%s/#", bot_serial, device_type, resource))
}

func main() {
	keepAlive := make(chan os.Signal)
	signal.Notify(keepAlive, os.Interrupt, syscall.SIGTERM)

	client = connect()
	subscribe()
	publishJson(GetWkVer)
	publishXML(GetBrushLifeSpan)

	//publishXML(GetMapM)
	//publishXML(GetMapSet)

	//publishXML(Stop)

	setupApi()
	<-keepAlive
}
