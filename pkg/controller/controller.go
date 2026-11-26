package controller

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/kushagharahi/ecovacs-privacy-control/pkg/ecovacs"
)

var (
	bot_serial   string = "b11fceaf-5173-4190-be6e-9c37ef3dc238"
	device_type  string = "ls1ok3"
	resource     string = "Lgsd"
	client       mqtt.Client
	serverIPFlag string
	robotIPFlag  string
	localRobot   bool
	cfgGlobal    Config
)

// Config configures the Ecovacs controller services (lookup, TLS XMPP, legacy MQTT, and REST API).
type Config struct {
	Protocol     string
	ServerIP     string
	RobotIP      string
	LookupPort   string
	XmppPort     string
	APIPort      string
	CertFile     string
	KeyFile      string
	CertCacheDir string
	LocalRobot   bool
}

// ApplyDefaults populates empty fields from standard environment variables and defaults.
func (c *Config) ApplyDefaults() {
	if c.Protocol == "" {
		c.Protocol = getEnv("PROTOCOL", getEnv("MODE", "all"))
	}
	c.Protocol = strings.ToLower(strings.TrimSpace(c.Protocol))

	if c.ServerIP == "" {
		c.ServerIP = getEnv("SERVER_IP", getEnv("CONTROLLER_IP", ""))
	}
	if c.RobotIP == "" {
		c.RobotIP = getEnv("ROBOT_IP", "")
	}
	if c.LookupPort == "" {
		c.LookupPort = getEnv("LOOKUP_PORT", "8007")
	}
	if c.XmppPort == "" {
		c.XmppPort = getEnv("XMPP_PORT", "5223")
	}
	if c.APIPort == "" {
		c.APIPort = getEnv("API_PORT", "8000")
	}
	if c.CertFile == "" {
		c.CertFile = getEnv("CERT_FILE", "")
	}
	if c.KeyFile == "" {
		c.KeyFile = getEnv("KEY_FILE", "")
	}
	if c.CertCacheDir == "" {
		c.CertCacheDir = getEnv("CERT_CACHE_DIR", "")
	}
	if !c.LocalRobot && isLoopbackHost(c.RobotIP) {
		c.LocalRobot = true
	}
}

func publishXML(msg XMLMsg) {
	if bot := getActiveXmppBot(); bot != nil && bot.IsReady() {
		if err := bot.SendCommand(msg); err != nil {
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

// GetActiveBotSN returns the serial number of the currently authenticated XMPP bot session, if any.
func GetActiveBotSN() string {
	botLock.RLock()
	defer botLock.RUnlock()
	if activeXmppBot != nil && !activeXmppBot.closed && activeXmppBot.sn != "" {
		return activeXmppBot.sn
	}
	return ""
}

// Start initializes and launches the controller servers until ctx is cancelled.
func Start(ctx context.Context, cfg Config) error {
	cfg.ApplyDefaults()
	cfgGlobal = cfg
	serverIPFlag = cfg.ServerIP
	robotIPFlag = cfg.RobotIP
	localRobot = cfg.LocalRobot

	ecovacs.InitSounds()

	log.Println("=======================================================")
	log.Printf(" Starting Ecovacs Privacy Controller (Protocol: %s)", cfg.Protocol)
	log.Printf(" - REST API (:%s) for controls and map view", cfg.APIPort)
	log.Println("=======================================================")

	enableXmpp := cfg.Protocol == "all" || cfg.Protocol == "xmpp"
	enableMqtt := cfg.Protocol == "all" || cfg.Protocol == "mqtt"

	if enableXmpp {
		log.Printf("[XMPP] Starting Lookup Server (:%s) and TLS XMPP Server (:%s)...", cfg.LookupPort, cfg.XmppPort)
		if err := startLookupServer(ctx, cfg.LookupPort); err != nil {
			return fmt.Errorf("lookup server: %w", err)
		}
		if err := startXmppServer(ctx, cfg); err != nil {
			return fmt.Errorf("xmpp server: %w", err)
		}
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

	if err := setupApi(ctx, cfg.APIPort); err != nil {
		return fmt.Errorf("rest api: %w", err)
	}
	return nil
}

// RunCLI runs the standalone controller binary from CLI flags and environment variables.
func RunCLI() {
	var cfg Config
	flag.StringVar(&cfg.Protocol, "protocol", "", "Target protocol: 'xmpp' (OZMO 930), 'mqtt' (Deebot 900/901), or 'all' (default)")
	flag.StringVar(&cfg.Protocol, "mode", "", "Alias for -protocol")
	flag.StringVar(&cfg.ServerIP, "server-ip", "", "External/advertised server IP for robot connection (env: SERVER_IP or CONTROLLER_IP)")
	flag.StringVar(&cfg.RobotIP, "robot-ip", "", "Robot IP address for telnet commands (env: ROBOT_IP)")
	flag.BoolVar(&cfg.LocalRobot, "local-robot", false, "Run directly on the robot (uses local filesystem instead of telnet)")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := Start(ctx, cfg); err != nil {
		log.Fatalf("[CONTROLLER] Startup failed: %v", err)
	}

	keepAlive := make(chan os.Signal, 1)
	signal.Notify(keepAlive, os.Interrupt, syscall.SIGTERM)

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

	<-keepAlive
	log.Println("Shutting down server...")
	cancel()
	time.Sleep(150 * time.Millisecond)
}
