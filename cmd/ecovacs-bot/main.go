package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/kushagharahi/ecovacs-privacy-control/pkg/controller"
	"github.com/kushagharahi/ecovacs-privacy-control/pkg/habridge"
)

type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	f        *os.File
	written  int64
}

func newRotatingWriter(path string, maxBytes int64) (*rotatingWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	info, _ := f.Stat()
	var sz int64
	if info != nil {
		sz = info.Size()
	}
	return &rotatingWriter{path: path, maxBytes: maxBytes, f: f, written: sz}, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.written+int64(len(p)) > w.maxBytes && w.f != nil {
		_ = w.f.Close()
		_ = os.Rename(w.path, w.path+".1")
		nf, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err == nil {
			w.f = nf
			w.written = 0
		}
	}
	if w.f == nil {
		return len(p), nil
	}
	n, err := w.f.Write(p)
	w.written += int64(n)
	return n, err
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		err := w.f.Close()
		w.f = nil
		return err
	}
	return nil
}

func waitForHTTPReady(ctx context.Context, url string) error {
	cli := &http.Client{Timeout: time.Second}
	for {
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		if resp, err := cli.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func defaultCertCacheDir() string {
	if v := os.Getenv("CERT_CACHE_DIR"); v != "" {
		return v
	}
	if st, err := os.Stat("/data"); err == nil && st.IsDir() {
		return "/data/ecovacs-certs"
	}
	return ""
}

func main() {
	var (
		ctrlCfg    controller.Config
		bridgeCfg  habridge.Config
		mqttListen string
		logFile    string
		logMaxKB   int64
	)

	flag.StringVar(&ctrlCfg.Protocol, "protocol", habridge.GetEnv("PROTOCOL", "xmpp"), "Target protocol: 'xmpp', 'mqtt', or 'all'")
	flag.StringVar(&ctrlCfg.ServerIP, "server-ip", habridge.GetEnv("SERVER_IP", "127.0.0.1"), "Advertised server IP for lookup response")
	flag.StringVar(&ctrlCfg.RobotIP, "robot-ip", habridge.GetEnv("ROBOT_IP", "127.0.0.1"), "Robot IP address")
	flag.BoolVar(&ctrlCfg.LocalRobot, "local-robot", true, "Run directly on the robot (local filesystem triggers instead of telnet)")
	flag.StringVar(&ctrlCfg.CertCacheDir, "cert-cache-dir", defaultCertCacheDir(), "Directory to cache generated TLS XMPP cert/key")

	flag.StringVar(&mqttListen, "mqtt-listen", habridge.GetEnv("MQTT_LISTEN", ":1883"), "Embedded MQTT broker listen address ('off' to disable)")
	flag.StringVar(&bridgeCfg.MQTTBroker, "mqtt-broker", habridge.GetEnv("MQTT_BROKER", "tcp://127.0.0.1:1883"), "MQTT broker URL for ha-bridge")
	flag.StringVar(&bridgeCfg.ControllerURL, "controller-url", habridge.GetEnv("CONTROLLER_URL", "http://127.0.0.1:8000"), "Ecovacs controller URL")
	flag.StringVar(&bridgeCfg.RobotSN, "robot-sn", habridge.GetEnv("ROBOT_SN", ""), "Robot Serial Number (auto-detected from XMPP if empty)")
	flag.StringVar(&bridgeCfg.HAPrefix, "ha-prefix", habridge.GetEnv("HA_PREFIX", "homeassistant"), "Home Assistant MQTT Discovery prefix")
	flag.StringVar(&bridgeCfg.MQTTTopic, "mqtt-topic", habridge.GetEnv("MQTT_TOPIC", "valetudo"), "Primary MQTT root topic")
	flag.DurationVar(&bridgeCfg.PollInterval, "poll-interval", 5*time.Second, "Telemetry polling interval")
	flag.DurationVar(&bridgeCfg.MapInterval, "map-interval", 3*time.Second, "Map polling interval")

	flag.StringVar(&logFile, "log-file", habridge.GetEnv("LOG_FILE", ""), "Optional rotating log file path")
	flag.Int64Var(&logMaxKB, "log-max-kb", 256, "Maximum size per log file in KiB before rotation")
	flag.Parse()

	if logFile != "" {
		rw, err := newRotatingWriter(logFile, logMaxKB*1024)
		if err != nil {
			log.Printf("[WARN] Could not open log file %s: %v", logFile, err)
		} else {
			defer rw.Close()
			log.SetOutput(io.MultiWriter(os.Stdout, rw))
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	hupChan := make(chan os.Signal, 1)
	signal.Notify(hupChan, syscall.SIGHUP)
	go func() {
		for range hupChan {
			log.Println("[RELOAD] Received SIGHUP! Re-executing binary in-place...")
			if binary, err := os.Executable(); err == nil {
				_ = syscall.Exec(binary, os.Args, os.Environ())
			}
		}
	}()

	log.Println("=======================================================")
	log.Println(" Starting Ecovacs On-Bot Stack (ecovacs-bot)")
	log.Println("=======================================================")

	if mqttListen != "" && mqttListen != "off" && mqttListen != "none" {
		brokerSrv, err := habridge.StartEmbeddedBroker(mqttListen)
		if err != nil {
			log.Fatalf("[MQTT-BROKER] Failed to start embedded broker: %v", err)
		}
		defer func() { _ = brokerSrv.Close() }()
	}

	if err := controller.Start(ctx, ctrlCfg); err != nil {
		log.Fatalf("[CONTROLLER] Failed to start: %v", err)
	}

	if err := waitForHTTPReady(ctx, bridgeCfg.ControllerURL+"/health"); err != nil {
		log.Fatalf("[CONTROLLER] Never became ready: %v", err)
	}

	bridgeErrCh := make(chan error, 1)
	go func() {
		if bridgeCfg.RobotSN == "" {
			log.Println("[BRIDGE] ROBOT_SN not set; waiting for XMPP bot session authentication...")
			for bridgeCfg.RobotSN == "" {
				if sn := controller.GetActiveBotSN(); sn != "" {
					bridgeCfg.RobotSN = sn
					log.Printf("[BRIDGE] Auto-detected Robot SN from XMPP: %s", sn)
					break
				}
				select {
				case <-ctx.Done():
					bridgeErrCh <- ctx.Err()
					return
				case <-time.After(500 * time.Millisecond):
				}
			}
		}
		bridgeErrCh <- habridge.Run(ctx, bridgeCfg)
	}()

	select {
	case <-sigChan:
		log.Println("[ECOVACS-BOT] Shutdown signal received...")
		cancel()
		select {
		case <-bridgeErrCh:
		case <-time.After(2 * time.Second):
		}
	case err := <-bridgeErrCh:
		if err != nil && ctx.Err() == nil {
			log.Fatalf("[BRIDGE] Exited with error: %v", err)
		}
	}

	log.Println("[ECOVACS-BOT] Exited cleanly.")
}
