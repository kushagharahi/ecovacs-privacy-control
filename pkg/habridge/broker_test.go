package habridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

func TestEmbeddedBrokerRetainAndLargePayload(t *testing.T) {
	// Pick a free ephemeral TCP port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ephemeral: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	srv, err := StartEmbeddedBroker(addr)
	if err != nil {
		t.Fatalf("StartEmbeddedBroker failed: %v", err)
	}
	defer func() { _ = srv.Close() }()

	pubOpts := paho.NewClientOptions().
		AddBroker("tcp://" + addr).
		SetClientID("test-pub").
		SetCleanSession(true)
	pubCli := paho.NewClient(pubOpts)
	if tok := pubCli.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatalf("publisher connect: %v", tok.Error())
	}
	defer pubCli.Disconnect(100)

	// Publish a >300KB retained payload (exceeding default 256KB ring buffer)
	largePayload := bytes.Repeat([]byte("A"), 320*1024)
	if tok := pubCli.Publish("valetudo/SN_TEST/MapData/map_data", 0, true, largePayload); tok.Wait() && tok.Error() != nil {
		t.Fatalf("publish large payload: %v", tok.Error())
	}

	got := make(chan []byte, 1)
	subOpts := paho.NewClientOptions().
		AddBroker("tcp://" + addr).
		SetClientID("test-sub").
		SetCleanSession(true)
	subCli := paho.NewClient(subOpts)
	if tok := subCli.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatalf("subscriber connect: %v", tok.Error())
	}
	defer subCli.Disconnect(100)

	if tok := subCli.Subscribe("valetudo/+/MapData/map_data", 0, func(_ paho.Client, m paho.Message) {
		got <- append([]byte(nil), m.Payload()...)
	}); tok.Wait() && tok.Error() != nil {
		t.Fatalf("subscribe: %v", tok.Error())
	}

	select {
	case payload := <-got:
		if !bytes.Equal(payload, largePayload) {
			t.Fatalf("received %d bytes, want %d bytes", len(payload), len(largePayload))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for retained large payload from embedded broker")
	}
}

func TestEmbeddedBrokerNoSelfEchoAndRetain(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ephemeral: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	srv, err := StartEmbeddedBroker(addr)
	if err != nil {
		t.Fatalf("StartEmbeddedBroker failed: %v", err)
	}
	defer func() { _ = srv.Close() }()

	connect := func(id string) paho.Client {
		cli := paho.NewClient(paho.NewClientOptions().
			AddBroker("tcp://" + addr).
			SetClientID(id).
			SetCleanSession(true))
		if tok := cli.Connect(); tok.Wait() && tok.Error() != nil {
			t.Fatalf("connect %s: %v", id, tok.Error())
		}
		return cli
	}

	bridgeCli := connect("mosquitto-bridge")
	defer bridgeCli.Disconnect(100)
	robotCli := connect("ha-bridge-robot")
	defer robotCli.Disconnect(100)

	bridgeGot := make(chan string, 4)
	robotGot := make(chan string, 4)

	if tok := bridgeCli.Subscribe("valetudo/#", 0, func(_ paho.Client, m paho.Message) {
		bridgeGot <- m.Topic() + "=" + string(m.Payload())
	}); tok.Wait() && tok.Error() != nil {
		t.Fatalf("bridge subscribe: %v", tok.Error())
	}
	if tok := robotCli.Subscribe("valetudo/#", 0, func(_ paho.Client, m paho.Message) {
		robotGot <- m.Topic() + "=" + string(m.Payload())
	}); tok.Wait() && tok.Error() != nil {
		t.Fatalf("robot subscribe: %v", tok.Error())
	}

	// 1. Bridge publishes a command: robot must receive it, bridge must NOT get echoed back.
	if tok := bridgeCli.Publish("valetudo/SN_TEST/set_carpet_boost", 0, false, "ON"); tok.Wait() && tok.Error() != nil {
		t.Fatalf("bridge publish: %v", tok.Error())
	}
	select {
	case msg := <-robotGot:
		if msg != "valetudo/SN_TEST/set_carpet_boost=ON" {
			t.Fatalf("robot got unexpected msg %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for robot to receive command")
	}
	select {
	case echo := <-bridgeGot:
		t.Fatalf("bridge received self-echo %q", echo)
	case <-time.After(200 * time.Millisecond):
	}

	// 2. Robot publishes a retained state: bridge receives it, robot does NOT get echoed,
	// and a late-joining client still receives the retained payload.
	if tok := robotCli.Publish("valetudo/SN_TEST/carpet_boost", 0, true, "ON"); tok.Wait() && tok.Error() != nil {
		t.Fatalf("robot publish: %v", tok.Error())
	}
	select {
	case msg := <-bridgeGot:
		if msg != "valetudo/SN_TEST/carpet_boost=ON" {
			t.Fatalf("bridge got unexpected msg %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge to receive state")
	}
	select {
	case echo := <-robotGot:
		t.Fatalf("robot received self-echo %q", echo)
	case <-time.After(200 * time.Millisecond):
	}

	lateCli := connect("late-subscriber")
	defer lateCli.Disconnect(100)
	lateGot := make(chan string, 1)
	if tok := lateCli.Subscribe("valetudo/SN_TEST/carpet_boost", 0, func(_ paho.Client, m paho.Message) {
		lateGot <- string(m.Payload())
	}); tok.Wait() && tok.Error() != nil {
		t.Fatalf("late subscribe: %v", tok.Error())
	}
	select {
	case val := <-lateGot:
		if val != "ON" {
			t.Fatalf("late subscriber got %q, want ON", val)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late subscriber did not receive retained message")
	}
}

func TestExportGridToValetudoMapDeterministicOrder(t *testing.T) {
	grid := make([][]byte, 6)
	for x := range grid {
		grid[x] = make([]byte, 6)
	}
	grid[0][0] = 4
	grid[1][1] = 5
	grid[2][2] = 6
	grid[3][3] = 7

	var first []byte
	for i := 0; i < 15; i++ {
		vMap := ExportGridToValetudoMap(
			grid, "SN123", 0, 0, 0, 0, 0,
			[]string{"0", "1", "2", "3"},
			map[string]string{"0": "A", "1": "B", "2": "C", "3": "D"},
			nil, nil, 100, 100,
		)
		raw, err := json.Marshal(vMap)
		if err != nil {
			t.Fatalf("marshal %d: %v", i, err)
		}
		if i == 0 {
			first = raw
		} else if !bytes.Equal(first, raw) {
			t.Fatalf("non-deterministic ValetudoMap output on iteration %d:\n%s\nvs\n%s", i, string(first), string(raw))
		}
	}
	_ = fmt.Sprintf("verified %d bytes", len(first))
}
