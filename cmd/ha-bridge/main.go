package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kushagharahi/ecovacs-privacy-control/pkg/habridge"
)

func main() {
	var cfg habridge.Config
	flag.StringVar(&cfg.MQTTBroker, "mqtt-broker", habridge.GetEnv("MQTT_BROKER", ""), "MQTT broker address")
	flag.StringVar(&cfg.ControllerURL, "controller-url", habridge.GetEnv("CONTROLLER_URL", "http://127.0.0.1:8000"), "Ecovacs controller base URL")
	flag.StringVar(&cfg.RobotSN, "robot-sn", habridge.GetEnv("ROBOT_SN", ""), "Robot Serial Number")
	flag.StringVar(&cfg.HAPrefix, "ha-prefix", habridge.GetEnv("HA_PREFIX", "homeassistant"), "Home Assistant MQTT Discovery prefix")
	flag.StringVar(&cfg.MQTTTopic, "mqtt-topic", habridge.GetEnv("MQTT_TOPIC", habridge.GetEnv("VALETUDO_TOPIC", "valetudo")), "Primary MQTT root topic (defaults to valetudo for HA map card compatibility)")
	flag.DurationVar(&cfg.PollInterval, "poll-interval", 5*time.Second, "Telemetry polling interval")
	flag.DurationVar(&cfg.MapInterval, "map-interval", 3*time.Second, "Map polling interval")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	hupChan := make(chan os.Signal, 1)
	signal.Notify(hupChan, syscall.SIGHUP)
	go func() {
		for range hupChan {
			log.Println("[RELOAD] Received SIGHUP! Re-executing binary in-place without container restart...")
			binary, err := os.Executable()
			if err == nil {
				_ = syscall.Exec(binary, os.Args, os.Environ())
			}
		}
	}()

	go func() {
		<-sigChan
		cancel()
	}()

	if err := habridge.Run(ctx, cfg); err != nil {
		log.Fatalf("[BRIDGE] Fatal: %v", err)
	}
}
