package habridge

import (
	"fmt"
	"log"

	mqtt "github.com/mochi-co/mqtt/server"
	"github.com/mochi-co/mqtt/server/events"
	"github.com/mochi-co/mqtt/server/listeners"
)

// StartEmbeddedBroker starts an in-process MQTT v3.1.1 TCP broker on listenAddr
// with 2 MiB power-of-2 client ring buffers to accommodate large map payloads
// and no-self-echo delivery so upstream Mosquitto bridges can safely use
// bidirectional wildcards (e.g. `topic valetudo/# both 0`) without loops.
func StartEmbeddedBroker(listenAddr string) (*mqtt.Server, error) {
	if listenAddr == "" {
		listenAddr = ":1883"
	}

	srv := mqtt.NewServer(&mqtt.Options{
		BufferSize:      2 * 1024 * 1024,
		BufferBlockSize: 8192,
	})

	// Suppress self-echo after retainMessage has already stored any retained payload.
	// This gives MQTT v3.1.1 bridges the same loop protection as MQTT v5 NoLocal.
	srv.Events.OnMessage = func(cl events.Client, pk events.Packet) (events.Packet, error) {
		all := srv.Clients.GetAll()
		allowed := make([]string, 0, len(all))
		for id := range all {
			if id != cl.ID {
				allowed = append(allowed, id)
			}
		}
		pk.AllowClients = allowed
		return pk, nil
	}

	tcp := listeners.NewTCP("ecovacs-mqtt", listenAddr)
	if err := srv.AddListener(tcp, nil); err != nil {
		return nil, fmt.Errorf("add mqtt listener %s: %w", listenAddr, err)
	}

	if err := srv.Serve(); err != nil {
		return nil, fmt.Errorf("serve embedded mqtt: %w", err)
	}

	log.Printf("[MQTT-BROKER] Embedded MQTT broker listening on %s (no-self-echo enabled)", listenAddr)
	return srv, nil
}
