package controller

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/kushagharahi/ecovacs-privacy-control/pkg/ecovacs"
)

var (
	serverIPFlag string
	robotIPFlag  string
	localRobot   bool
	cfgGlobal    Config
)

// Config configures the Ecovacs controller services (lookup, TLS XMPP, and REST API).
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
		c.Protocol = getEnv("PROTOCOL", getEnv("MODE", "xmpp"))
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

	log.Printf("[PUBLISH] No XMPP bot connected. Command %s not sent.", msg.cmdName)
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

	log.Printf("[XMPP] Starting Lookup Server (:%s) and TLS XMPP Server (:%s)...", cfg.LookupPort, cfg.XmppPort)
	if err := startLookupServer(ctx, cfg.LookupPort); err != nil {
		return fmt.Errorf("lookup server: %w", err)
	}
	if err := startXmppServer(ctx, cfg); err != nil {
		return fmt.Errorf("xmpp server: %w", err)
	}

	if err := setupApi(ctx, cfg.APIPort); err != nil {
		return fmt.Errorf("rest api: %w", err)
	}
	return nil
}
