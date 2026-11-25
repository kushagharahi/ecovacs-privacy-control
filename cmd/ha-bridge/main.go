package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/kushagharahi/ecovacs-privacy-control/pkg/ecovacs"
	"github.com/kushagharahi/ecovacs-privacy-control/pkg/habridge"
)

var (
	activeGoToCancel context.CancelFunc
	activeGoToDone   chan struct{}
	activeGoToID     uint64
	activeGoToLock   sync.Mutex

	lastZoneCmdTime    time.Time
	lastZoneCmdLock    sync.Mutex
	lastSegmentCmdTime time.Time
	lastSegmentCmdLock sync.Mutex
	lastGoToCmdTime    time.Time
	lastGoToCmdLock    sync.Mutex
)

func cancelActiveGoTo(httpCli *http.Client) {
	activeGoToLock.Lock()
	cancel := activeGoToCancel
	done := activeGoToDone
	activeGoToCancel = nil
	activeGoToDone = nil
	activeGoToID++
	activeGoToLock.Unlock()

	if cancel != nil {
		cancel()
		if done != nil {
			select {
			case <-done:
			case <-time.After(500 * time.Millisecond):
			}
		}
		if httpCli != nil {
			callAPI(httpCli, "/clean?act=stop")
			callAPI(httpCli, "/silentGoto?enable=0")
		}
	}
}

var (
	mqttBroker    = flag.String("mqtt-broker", getEnv("MQTT_BROKER", ""), "MQTT broker address")
	controllerURL = flag.String("controller-url", getEnv("CONTROLLER_URL", "http://127.0.0.1:8000"), "Ecovacs controller base URL")
	robotSN       = flag.String("robot-sn", getEnv("ROBOT_SN", ""), "Robot Serial Number")
	haPrefix      = flag.String("ha-prefix", getEnv("HA_PREFIX", "homeassistant"), "Home Assistant MQTT Discovery prefix")
	mqttTopic     = flag.String("mqtt-topic", getEnv("MQTT_TOPIC", getEnv("VALETUDO_TOPIC", "valetudo")), "Primary MQTT root topic (defaults to valetudo for HA map card compatibility)")
	pollInterval  = flag.Duration("poll-interval", 5*time.Second, "Telemetry polling interval")
	mapInterval   = flag.Duration("map-interval", 3*time.Second, "Map polling interval")

	cropOffsetLock sync.RWMutex
	lastCropMinX   int = 192
	lastCropMinY   int = 281
	lastMapW       int = 256
	lastMapH       int = 292
)

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

func main() {
	flag.Parse()

	if *mqttBroker == "" {
		log.Fatalf("Fatal: MQTT broker address is required (set -mqtt-broker flag or MQTT_BROKER env var)")
	}
	if *robotSN == "" {
		log.Fatalf("Fatal: Robot Serial Number is required (set -robot-sn flag or ROBOT_SN env var)")
	}

	topicPrefix := fmt.Sprintf("%s/%s", *mqttTopic, *robotSN)
	subPrefixes := uniqueStrings([]string{
		topicPrefix,
		fmt.Sprintf("ha-bridge/%s", *robotSN),
		"ha-bridge/ozmo_930",
		fmt.Sprintf("valetudo/%s", *robotSN),
		"valetudo/ozmo_930",
	})

	log.Println("=======================================================")
	log.Println(" Starting Home Assistant MQTT Bridge (ha-bridge)")
	log.Printf(" - Target Robot SN:    %s", *robotSN)
	log.Printf(" - Controller Base:    %s", *controllerURL)
	log.Printf(" - MQTT Broker:        %s", *mqttBroker)
	log.Printf(" - Primary Topic:      %s", topicPrefix)
	log.Printf(" - Command Prefixes:   %s", strings.Join(subPrefixes, ", "))
	log.Println("=======================================================")

	httpClient := &http.Client{Timeout: 5 * time.Second}

	// 1. Configure MQTT Client
	opts := mqtt.NewClientOptions().
		AddBroker(*mqttBroker).
		SetClientID(fmt.Sprintf("ha-bridge-%s-%d", *robotSN, time.Now().Unix()%10000)).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second)

	opts.OnConnect = func(c mqtt.Client) {
		log.Println("[MQTT] Connected successfully to broker.")
		// Publish Home Assistant discovery upon connection
		if err := habridge.PublishHADiscovery(c, *haPrefix, topicPrefix, *robotSN); err != nil {
			log.Printf("[MQTT] Failed to publish HA discovery: %v", err)
		}
		// Subscribe to control topics across ha-bridge and valetudo prefixes so existing HA cards continue to work
		for _, p := range subPrefixes {
			subscribeTopics(c, p, topicPrefix, httpClient)
		}
		subscribeTemplateTopics(c, httpClient)
	}

	opts.OnConnectionLost = func(c mqtt.Client, err error) {
		log.Printf("[MQTT] Connection lost: %v", err)
	}

	mqttClient := mqtt.NewClient(opts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("[MQTT] Connection failed: %v", token.Error())
	}

	// 2. Start polling routines
	stopChan := make(chan struct{})
	go startTelemetryLoop(mqttClient, topicPrefix, httpClient, stopChan)
	go startMapLoop(mqttClient, topicPrefix, httpClient, stopChan)

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

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

	<-sigChan

	log.Println("[BRIDGE] Shutting down...")
	close(stopChan)
	mqttClient.Disconnect(250)
	log.Println("[BRIDGE] Exited cleanly.")
}

func debounceWithin(lock *sync.Mutex, lastTime *time.Time, window time.Duration) bool {
	lock.Lock()
	defer lock.Unlock()
	if time.Since(*lastTime) < window {
		return false
	}
	*lastTime = time.Now()
	return true
}

func worldCoordsToRobotMM(pts ...[2]float64) [][2]int {
	cropOffsetLock.RLock()
	cMinX, cMinY := lastCropMinX, lastCropMinY
	mapW, mapH := lastMapW, lastMapH
	cropOffsetLock.RUnlock()

	offsetWorldX := (400 - cMinX) * 50
	offsetWorldY := (400 - cMinY) * 50

	var maxX, maxY float64
	for _, pt := range pts {
		if ax := math.Abs(pt[0]); ax > maxX {
			maxX = ax
		}
		if ay := math.Abs(pt[1]); ay > maxY {
			maxY = ay
		}
	}

	out := make([][2]int, len(pts))
	for i, pt := range pts {
		x, y := pt[0], pt[1]
		// 1. Normalized ratio (0.0 .. 1.0)
		if maxX <= 1.0 && maxY <= 1.0 {
			x = x * float64(mapW) * 50.0
			y = y * float64(mapH) * 50.0
		} else if maxX <= 400 && maxY <= 400 {
			// 2. Raw map pixel coordinates (0 .. 256, 0 .. 292)
			x = x * 50.0
			y = y * 50.0
		}
		// 3. Otherwise coordinates are already in map world coordinates (mm)
		out[i] = [2]int{int(x) - offsetWorldX, int(y) - offsetWorldY}
	}
	return out
}

func handleSegmentClean(httpCli *http.Client) mqtt.MessageHandler {
	return func(_ mqtt.Client, m mqtt.Message) {
		if !debounceWithin(&lastSegmentCmdLock, &lastSegmentCmdTime, time.Second) {
			return
		}
		cancelActiveGoTo(httpCli)

		var req struct {
			SegmentIDs []string `json:"segment_ids"`
		}
		if err := json.Unmarshal(m.Payload(), &req); err == nil && len(req.SegmentIDs) > 0 {
			sa := strings.Join(req.SegmentIDs, ",")
			log.Printf("[CMD] MapSegmentation Clean: %s", sa)
			callAPI(httpCli, "/silentGoto?enable=0")
			callAPI(httpCli, fmt.Sprintf("/clean?act=s&type=spot_area&sa=%s", sa))
		}
	}
}

func handleZoneClean(httpCli *http.Client) mqtt.MessageHandler {
	return func(_ mqtt.Client, m mqtt.Message) {
		if !debounceWithin(&lastZoneCmdLock, &lastZoneCmdTime, time.Second) {
			return
		}
		cancelActiveGoTo(httpCli)

		log.Printf("[CMD] Zone Clean payload: %s", string(m.Payload()))
		var req struct {
			Zones []struct {
				Points struct {
					PA struct{ X, Y float64 } `json:"pA"`
					PC struct{ X, Y float64 } `json:"pC"`
				} `json:"points"`
			} `json:"zones"`
		}
		if err := json.Unmarshal(m.Payload(), &req); err == nil && len(req.Zones) > 0 {
			z := req.Zones[0]
			pts := worldCoordsToRobotMM(
				[2]float64{z.Points.PA.X, z.Points.PA.Y},
				[2]float64{z.Points.PC.X, z.Points.PC.Y},
			)
			minX, maxX := pts[0][0], pts[1][0]
			if minX > maxX {
				minX, maxX = maxX, minX
			}
			minY, maxY := pts[0][1], pts[1][1]
			if minY > maxY {
				minY, maxY = maxY, minY
			}

			log.Printf("[CMD] Zone Clean converted to robot physical mm: (%d,%d) to (%d,%d)", minX, minY, maxX, maxY)
			callAPI(httpCli, "/silentGoto?enable=0")
			callAPI(httpCli, fmt.Sprintf("/clean?act=s&type=custom&p=%d,%d,%d,%d", minX, minY, maxX, maxY))
		}
	}
}

func handleGoTo(httpCli *http.Client) mqtt.MessageHandler {
	return func(_ mqtt.Client, m mqtt.Message) {
		if !debounceWithin(&lastGoToCmdLock, &lastGoToCmdTime, time.Second) {
			return
		}
		cancelActiveGoTo(httpCli)

		var req struct {
			Coordinates struct {
				X float64 `json:"x"`
				Y float64 `json:"y"`
			} `json:"coordinates"`
		}
		if err := json.Unmarshal(m.Payload(), &req); err == nil {
			pts := worldCoordsToRobotMM([2]float64{req.Coordinates.X, req.Coordinates.Y})
			rx, ry := pts[0][0], pts[0][1]
			log.Printf("[CMD] GoTo Pin Target: robot mm (%d, %d)", rx, ry)

			// Set up cancellable arrival watcher with 3-minute timeout
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			done := make(chan struct{})
			activeGoToLock.Lock()
			activeGoToID++
			watcherID := activeGoToID
			activeGoToCancel = cancel
			activeGoToDone = done
			activeGoToLock.Unlock()

			// Enable silent goto on robot (suppresses voice prompt "Starting to clean" and keeps suction motors at 0 RPM)
			callAPI(httpCli, "/silentGoto?enable=1")
			time.Sleep(300 * time.Millisecond)

			// On OZMO 930, navigate to target point via 500mm x 500mm zone centered at (rx, ry)
			// libdirectpath.so intercepts findpathInt400 while libsilentgoto.so keeps suction/brushes off.
			callAPI(httpCli, fmt.Sprintf("/clean?act=s&type=custom&p=%d,%d,%d,%d", rx-250, ry-250, rx+250, ry+250))

			// Background arrival watcher: monitors robot's physical position via /status telemetry
			go runGoToWatcher(ctx, watcherID, done, httpCli, rx, ry)
		}
	}
}

func runGoToWatcher(ctx context.Context, wID uint64, wDone chan struct{}, httpCli *http.Client, targetX, targetY int) {
	defer func() {
		activeGoToLock.Lock()
		if activeGoToID == wID {
			activeGoToCancel = nil
			activeGoToDone = nil
		}
		activeGoToLock.Unlock()
		close(wDone)
	}()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	hasStartedMoving := false
	hasConfirmedCleaning := false
	startTime := time.Now()
	lastLogTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.Canceled {
				log.Printf("[GOTO] Arrival watcher cancelled (external stop)")
				return
			}
			log.Printf("[GOTO] Arrival watcher timed out (context done)")
			callAPI(httpCli, "/clean?act=stop")
			callAPI(httpCli, "/silentGoto?enable=0")
			return
		case <-ticker.C:
			resp, err := httpCli.Get(*controllerURL + "/status")
			if err != nil {
				continue
			}
			var st ecovacs.StatusResponse
			if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
				resp.Body.Close()
				continue
			}
			resp.Body.Close()

			// Calculate current robot physical coordinate in mm
			currX := (st.RobotPosX - 400) * 50
			currY := (st.RobotPosY - 400) * 50
			chargerX := (st.ChargerPosX - 400) * 50
			chargerY := (st.ChargerPosY - 400) * 50
			dxC := float64(currX - chargerX)
			dyC := float64(currY - chargerY)
			distFromDock := math.Sqrt(dxC*dxC + dyC*dyC)

			dx := float64(targetX - currX)
			dy := float64(targetY - currY)
			dist := math.Sqrt(dx*dx + dy*dy)

			// Check if robot has left dock and started transit
			if !hasStartedMoving {
				if st.ChargingState == "Cleaning" {
					hasStartedMoving = true
					hasConfirmedCleaning = true
					log.Printf("[GOTO] Robot confirmed in Cleaning state, navigating towards (%d, %d)", targetX, targetY)
				} else if distFromDock > 250 || (st.ChargingState != "SlotCharging" && st.ChargingState != "Charging" && st.ChargingState != "Docked" && st.ChargingState != "Idle") {
					hasStartedMoving = true
					log.Printf("[GOTO] Robot disengaged dock (state=%s, distFromDock=%.0f), heading towards (%d, %d)", st.ChargingState, distFromDock, targetX, targetY)
				} else if time.Since(startTime) > 30*time.Second {
					log.Printf("[GOTO] Robot failed to disengage dock within 30s (state=%s), exiting watcher", st.ChargingState)
					callAPI(httpCli, "/clean?act=stop")
					callAPI(httpCli, "/silentGoto?enable=0")
					return
				} else {
					// Still in startup/undocking phase, continue waiting
					continue
				}
			}

			if st.ChargingState == "Cleaning" {
				hasConfirmedCleaning = true
			}

			if time.Since(lastLogTime) >= 1500*time.Millisecond {
				log.Printf("[GOTO] Tracking: Robot at (%d, %d), Target (%d, %d), Dist=%.0fmm, State=%s", currX, currY, targetX, targetY, dist, st.ChargingState)
				lastLogTime = time.Now()
			}

			// When robot arrives within 450mm (~18 inches, robot radius is ~175mm) of target point
			if dist <= 450 {
				log.Printf("[GOTO] Destination reached! Robot at (%d, %d), target (%d, %d), dist=%.0fmm. Halting motion.", currX, currY, targetX, targetY, dist)
				callAPI(httpCli, "/clean?act=stop")
				time.Sleep(300 * time.Millisecond)
				callAPI(httpCli, "/silentGoto?enable=0")
				return
			}

			// If robot was docked by user or returned to dock
			if hasStartedMoving && (st.ChargingState == "SlotCharging" || st.ChargingState == "Charging" || st.ChargingState == "Docked") {
				log.Printf("[GOTO] Robot docked externally (state=%s), ending watcher", st.ChargingState)
				callAPI(httpCli, "/silentGoto?enable=0")
				return
			}

			// If clean was confirmed active and subsequently stopped/paused externally
			if hasConfirmedCleaning && (st.ChargingState == "Idle" || st.ChargingState == "Paused") {
				log.Printf("[GOTO] Clean ended externally (state=%s), ending watcher", st.ChargingState)
				callAPI(httpCli, "/clean?act=stop")
				callAPI(httpCli, "/silentGoto?enable=0")
				return
			}
		}
	}
}

func handleSpotClean(httpCli *http.Client) mqtt.MessageHandler {
	return func(_ mqtt.Client, m mqtt.Message) {
		cancelActiveGoTo(httpCli)
		log.Printf("[CMD] SpotClean Target payload: %s", string(m.Payload()))
		var req struct {
			Coordinates struct {
				X float64 `json:"x"`
				Y float64 `json:"y"`
			} `json:"coordinates"`
		}
		if err := json.Unmarshal(m.Payload(), &req); err == nil {
			pts := worldCoordsToRobotMM([2]float64{req.Coordinates.X, req.Coordinates.Y})
			rx, ry := pts[0][0], pts[0][1]
			log.Printf("[CMD] Spot Clean Pin Target: robot mm (%d, %d)", rx, ry)

			// Clean a 1.0m x 1.0m zone centered at (rx, ry) with suction ON
			callAPI(httpCli, "/silentGoto?enable=0")
			callAPI(httpCli, fmt.Sprintf("/clean?act=s&type=custom&p=%d,%d,%d,%d", rx-500, ry-500, rx+500, ry+500))
		}
	}
}

func subscribeTemplateTopics(c mqtt.Client, httpCli *http.Client) {
	c.Subscribe("[[topic]]/MapSegmentationCapability/clean/set", 0, handleSegmentClean(httpCli))
	c.Subscribe("[[topic]]/ZoneCleaningCapability/start/set", 0, handleZoneClean(httpCli))
	c.Subscribe("[[topic]]/GoToLocationCapability/go/set", 0, handleGoTo(httpCli))
	c.Subscribe("[[topic]]/SpotCleaningCapability/clean/set", 0, handleSpotClean(httpCli))
}

func subscribeTopics(c mqtt.Client, subPrefix, publishPrefix string, httpCli *http.Client) {
	// Command Topic (Vacuum actions)
	c.Subscribe(fmt.Sprintf("%s/command", subPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		cmd := string(m.Payload())
		log.Printf("[CMD] Received vacuum command: %s", cmd)
		switch cmd {
		case "start", "clean":
			cancelActiveGoTo(httpCli)
			callAPI(httpCli, "/silentGoto?enable=0")
			callAPI(httpCli, "/clean?act=start&type=auto")
		case "pause":
			cancelActiveGoTo(httpCli)
			callAPI(httpCli, "/clean?act=pause")
		case "stop":
			cancelActiveGoTo(httpCli)
			callAPI(httpCli, "/clean?act=stop")
		case "return_to_base", "return_home":
			cancelActiveGoTo(httpCli)
			callAPI(httpCli, "/charge")
		case "locate":
			callAPI(httpCli, "/locate")
		}
	})

	// Boolean Switch Topics
	boolSwitches := []struct {
		cmdTopic   string
		stateTopic string
		endpoint   string
		label      string
	}{
		{"set_carpet_boost", "carpet_boost", "/setCarpetBoost?enable=%v", "carpet boost"},
		{"set_continuous_clean", "continuous_clean", "/setContinuousClean?enable=%v", "continuous clean"},
		{"set_voice_reports", "voice_reports", "/setVoiceReports?enable=%v", "voice reports"},
		{"set_dnd", "dnd", "/dnd?enable=%v", "DND"},
	}
	for _, sw := range boolSwitches {
		sw := sw
		c.Subscribe(fmt.Sprintf("%s/%s", subPrefix, sw.cmdTopic), 0, func(_ mqtt.Client, m mqtt.Message) {
			payload := strings.ToUpper(strings.TrimSpace(string(m.Payload())))
			enable := payload == "ON" || payload == "TRUE" || payload == "1"
			log.Printf("[CMD] Set %s: %v", sw.label, enable)
			callAPI(httpCli, fmt.Sprintf(sw.endpoint, enable))
			c.Publish(fmt.Sprintf("%s/%s", publishPrefix, sw.stateTopic), 0, true, cbStr(enable))
		})
	}

	// Consumable Lifespan Reset Topics
	resetButtons := []struct {
		topic     string
		component string
		label     string
	}{
		{"reset_main_brush", "Brush", "Main Brush"},
		{"reset_side_brush", "SideBrush", "Side Brush"},
		{"reset_dust_filter", "DustCaseHeap", "Dust Filter"},
	}
	for _, btn := range resetButtons {
		btn := btn
		c.Subscribe(fmt.Sprintf("%s/%s", subPrefix, btn.topic), 0, func(_ mqtt.Client, _ mqtt.Message) {
			log.Printf("[CMD] Reset %s lifespan", btn.label)
			callAPI(httpCli, "/resetLifeSpan?component="+btn.component)
		})
	}

	// Volume Topic
	c.Subscribe(fmt.Sprintf("%s/set_volume", subPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		vol, err := strconv.Atoi(string(m.Payload()))
		if err == nil {
			log.Printf("[CMD] Set volume: %d", vol)
			callAPI(httpCli, fmt.Sprintf("/setVolume?level=%d", vol))
		}
	})

	// Water Flow Topic
	c.Subscribe(fmt.Sprintf("%s/set_water_flow", subPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		flowStr := string(m.Payload())
		flowVal := 2
		switch flowStr {
		case "Low":
			flowVal = 1
		case "Medium":
			flowVal = 2
		case "High":
			flowVal = 3
		case "Max":
			flowVal = 4
		}
		log.Printf("[CMD] Set water flow: %s (%d)", flowStr, flowVal)
		callAPI(httpCli, fmt.Sprintf("/setWater?level=%d", flowVal))
	})

	// Play Sound Topic
	c.Subscribe(fmt.Sprintf("%s/play_sound", subPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		val := string(m.Payload())
		// Parse ID from format "XX: Name" or direct integer
		parts := strings.Split(val, ":")
		soundID, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err == nil {
			log.Printf("[CMD] Play sound prompt ID %d", soundID)
			callAPI(httpCli, fmt.Sprintf("/playSound?sid=%d", soundID))
			c.Publish(fmt.Sprintf("%s/last_sound", publishPrefix), 0, true, val)
		}
	})

	// Map Capabilities (Valetudo-compatible MQTT topics)
	c.Subscribe(fmt.Sprintf("%s/MapSegmentationCapability/clean/set", subPrefix), 0, handleSegmentClean(httpCli))
	c.Subscribe(fmt.Sprintf("%s/ZoneCleaningCapability/start/set", subPrefix), 0, handleZoneClean(httpCli))
	c.Subscribe(fmt.Sprintf("%s/GoToLocationCapability/target/set", subPrefix), 0, handleGoTo(httpCli))
	c.Subscribe(fmt.Sprintf("%s/GoToLocationCapability/go/set", subPrefix), 0, handleGoTo(httpCli))
	c.Subscribe(fmt.Sprintf("%s/SpotCleaningCapability/clean/set", subPrefix), 0, handleSpotClean(httpCli))

	// Virtual Restrictions (Walls and No-Go Areas)
	c.Subscribe(fmt.Sprintf("%s/CombinedVirtualRestrictionsCapability/set", subPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		log.Printf("[CMD] CombinedVirtualRestrictions set: %s", string(m.Payload()))
		var req struct {
			VirtualWalls []struct {
				Points struct {
					PA struct{ X, Y float64 } `json:"pA"`
					PB struct{ X, Y float64 } `json:"pB"`
				} `json:"points"`
			} `json:"virtual_walls"`
			NoGoAreas []struct {
				Points struct {
					PA struct{ X, Y float64 } `json:"pA"`
					PB struct{ X, Y float64 } `json:"pB"`
					PC struct{ X, Y float64 } `json:"pC"`
					PD struct{ X, Y float64 } `json:"pD"`
				} `json:"points"`
			} `json:"no_go_areas"`
		}

		if err := json.Unmarshal(m.Payload(), &req); err == nil {
			cropMinX, cropMinY := 0, 0
			if gResp, err := httpCli.Get(*controllerURL + "/mapGrid"); err == nil {
				var mg ecovacs.MapGridResponse
				if json.NewDecoder(gResp.Body).Decode(&mg) == nil {
					cropMinX, cropMinY = mg.CropMinX, mg.CropMinY
				}
				gResp.Body.Close()
			}

			offsetWorldX := (400 - cropMinX) * 50
			offsetWorldY := (400 - cropMinY) * 50

			var walls [][4]int
			for _, w := range req.VirtualWalls {
				rx1 := int(w.Points.PA.X) - offsetWorldX
				ry1 := int(w.Points.PA.Y) - offsetWorldY
				rx2 := int(w.Points.PB.X) - offsetWorldX
				ry2 := int(w.Points.PB.Y) - offsetWorldY
				walls = append(walls, [4]int{rx1, ry1, rx2, ry2})
			}

			var zones [][8]int
			for _, z := range req.NoGoAreas {
				rx1 := int(z.Points.PA.X) - offsetWorldX
				ry1 := int(z.Points.PA.Y) - offsetWorldY
				rx2 := int(z.Points.PB.X) - offsetWorldX
				ry2 := int(z.Points.PB.Y) - offsetWorldY
				rx3 := int(z.Points.PC.X) - offsetWorldX
				ry3 := int(z.Points.PC.Y) - offsetWorldY
				rx4 := int(z.Points.PD.X) - offsetWorldX
				ry4 := int(z.Points.PD.Y) - offsetWorldY
				zones = append(zones, [8]int{rx1, ry1, rx2, ry2, rx3, ry3, rx4, ry4})
			}

			payloadBytes, _ := json.Marshal(map[string]interface{}{
				"virtual_walls": walls,
				"no_go_areas":   zones,
			})

			pResp, err := httpCli.Post(*controllerURL+"/virtualRestrictions", "application/json", bytes.NewReader(payloadBytes))
			if err == nil {
				pResp.Body.Close()
			}

			c.Publish(fmt.Sprintf("%s/CombinedVirtualRestrictionsCapability", publishPrefix), 0, true, m.Payload())
		}
	})
}

func startTelemetryLoop(c mqtt.Client, topicPrefix string, httpCli *http.Client, stopChan <-chan struct{}) {
	ticker := time.NewTicker(*pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			resp, err := httpCli.Get(*controllerURL + "/status")
			if err != nil {
				continue
			}

			var status ecovacs.StatusResponse
			if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
				resp.Body.Close()
				continue
			}
			resp.Body.Close()

			// Map to standard Home Assistant vacuum state and publish combined JSON to state_topic
			vState := habridge.MapEcovacsState(status.ChargingState, status.CleanSpeed)
			statePayload := map[string]interface{}{
				"state":         string(vState),
				"battery_level": status.BatteryLevel,
			}
			stateJSON, _ := json.Marshal(statePayload)
			c.Publish(fmt.Sprintf("%s/state", topicPrefix), 0, true, stateJSON)
			c.Publish(fmt.Sprintf("%s/battery", topicPrefix), 0, true, strconv.Itoa(status.BatteryLevel))
			c.Publish(fmt.Sprintf("%s/charging", topicPrefix), 0, true, status.ChargingState)

			// Publish sensors (retained so dashboard immediately has data on load)
			c.Publish(fmt.Sprintf("%s/brush_lifespan", topicPrefix), 0, true, strconv.Itoa(status.BrushLifeSpan))
			c.Publish(fmt.Sprintf("%s/side_brush_lifespan", topicPrefix), 0, true, strconv.Itoa(status.SideBrushLifeSpan))
			c.Publish(fmt.Sprintf("%s/filter_lifespan", topicPrefix), 0, true, strconv.Itoa(status.DustCaseHeapLifeSpan))
			c.Publish(fmt.Sprintf("%s/volume", topicPrefix), 0, true, strconv.Itoa(status.VolumeLevel))

			wbState := "OFF"
			if status.WaterBoxInstalled {
				wbState = "ON"
			}
			c.Publish(fmt.Sprintf("%s/water_box", topicPrefix), 0, true, wbState)

			waterNames := map[int]string{1: "Low", 2: "Medium", 3: "High", 4: "Max"}
			flowName := "Medium"
			if name, ok := waterNames[status.WaterLevel]; ok {
				flowName = name
			}
			c.Publish(fmt.Sprintf("%s/water_flow", topicPrefix), 0, true, flowName)

			c.Publish(fmt.Sprintf("%s/carpet_boost", topicPrefix), 0, true, cbStr(status.CarpetBoost))
			c.Publish(fmt.Sprintf("%s/continuous_clean", topicPrefix), 0, true, cbStr(status.ContinuousClean))
			c.Publish(fmt.Sprintf("%s/voice_reports", topicPrefix), 0, true, cbStr(status.VoiceReports))
			c.Publish(fmt.Sprintf("%s/dnd", topicPrefix), 0, true, cbStr(status.DNDEnabled))

			c.Publish(fmt.Sprintf("%s/total_clean_count", topicPrefix), 0, true, strconv.Itoa(status.TotalCleanCount))
			c.Publish(fmt.Sprintf("%s/total_clean_area", topicPrefix), 0, true, strconv.Itoa(status.TotalCleanArea))
			totalHours := float64(status.TotalCleanTime) / 3600.0
			c.Publish(fmt.Sprintf("%s/total_clean_time", topicPrefix), 0, true, fmt.Sprintf("%.1f", totalHours))
		}
	}
}

func startMapLoop(c mqtt.Client, topicPrefix string, httpCli *http.Client, stopChan <-chan struct{}) {
	ticker := time.NewTicker(*mapInterval)
	defer ticker.Stop()

	var lastMapDataHash string

	updateMap := func() {
		// Fetch live status
		var curStatus ecovacs.StatusResponse
		if sResp, err := httpCli.Get(*controllerURL + "/status"); err == nil {
			json.NewDecoder(sResp.Body).Decode(&curStatus)
			sResp.Body.Close()
		}

		var vMap *habridge.ValetudoMap
		var img image.Image
		var roomPolygons map[string][][2]int
		usedRawGrid := false

		// 1. Try to fetch high-fidelity raw grid directly from controller
		if gResp, err := httpCli.Get(*controllerURL + "/mapGrid"); err == nil {
			var mapGrid ecovacs.MapGridResponse
			if json.NewDecoder(gResp.Body).Decode(&mapGrid) == nil && len(mapGrid.Grid) > 0 {
				roomPolygons = mapGrid.RoomPolygons
				cropOffsetLock.Lock()
				lastCropMinX = mapGrid.CropMinX
				lastCropMinY = mapGrid.CropMinY
				lastMapW = len(mapGrid.Grid)
				lastMapH = len(mapGrid.Grid[0])
				cropOffsetLock.Unlock()

				if curStatus.ChargingState == "SlotCharging" || curStatus.ChargingState == "Charging" {
					mapGrid.RobotPixelX = mapGrid.ChargerPixelX
					mapGrid.RobotPixelY = mapGrid.ChargerPixelY
					curStatus.RobotPixelX = curStatus.ChargerPixelX
					curStatus.RobotPixelY = curStatus.ChargerPixelY
					curStatus.PoseValid = true
				}

				vMap = habridge.ExportGridToValetudoMap(
					mapGrid.Grid,
					*robotSN,
					mapGrid.ChargerPixelX,
					mapGrid.ChargerPixelY,
					mapGrid.RobotPixelX,
					mapGrid.RobotPixelY,
					mapGrid.RobotAngle,
					mapGrid.Rooms,
					mapGrid.RoomNames,
					mapGrid.VirtualRestrictions,
					mapGrid.PathPoints,
					mapGrid.CropMinX,
					mapGrid.CropMinY,
				)
				img = habridge.GridToImage(
					mapGrid.Grid,
					mapGrid.ChargerPixelX,
					mapGrid.ChargerPixelY,
					mapGrid.RobotPixelX,
					mapGrid.RobotPixelY,
					mapGrid.RobotAngle,
					mapGrid.VirtualRestrictions,
					mapGrid.PathPoints,
					mapGrid.CropMinX,
					mapGrid.CropMinY,
				)
				usedRawGrid = true

				// Publish current virtual restrictions state
				offsetWorldX := (400 - mapGrid.CropMinX) * 50
				offsetWorldY := (400 - mapGrid.CropMinY) * 50
				var vwList []map[string]interface{}
				var nogoList []map[string]interface{}
				for _, r := range mapGrid.VirtualRestrictions {
					if r.Type == "virtual_wall" && len(r.Points) == 4 {
						vwList = append(vwList, map[string]interface{}{
							"points": map[string]interface{}{
								"pA": map[string]int{"x": r.Points[0] + offsetWorldX, "y": r.Points[1] + offsetWorldY},
								"pB": map[string]int{"x": r.Points[2] + offsetWorldX, "y": r.Points[3] + offsetWorldY},
							},
						})
					} else if r.Type == "no_go_area" && len(r.Points) == 8 {
						nogoList = append(nogoList, map[string]interface{}{
							"points": map[string]interface{}{
								"pA": map[string]int{"x": r.Points[0] + offsetWorldX, "y": r.Points[1] + offsetWorldY},
								"pB": map[string]int{"x": r.Points[2] + offsetWorldX, "y": r.Points[3] + offsetWorldY},
								"pC": map[string]int{"x": r.Points[4] + offsetWorldX, "y": r.Points[5] + offsetWorldY},
								"pD": map[string]int{"x": r.Points[6] + offsetWorldX, "y": r.Points[7] + offsetWorldY},
							},
						})
					}
				}
				restrPayload, _ := json.Marshal(map[string]interface{}{
					"virtual_walls": vwList,
					"no_go_areas":   nogoList,
				})
				c.Publish(fmt.Sprintf("%s/CombinedVirtualRestrictionsCapability", topicPrefix), 0, true, restrPayload)
			}
			gResp.Body.Close()
		}

		// 2. Fallback to raster /getMapData if raw grid not populated yet
		var imgBytes []byte
		if !usedRawGrid {
			resp, err := httpCli.Get(*controllerURL + "/getMapData")
			if err != nil {
				return
			}
			imgBytes, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || len(imgBytes) == 0 {
				return
			}

			decodedImg, _, err := image.Decode(bytes.NewReader(imgBytes))
			if err != nil {
				return
			}
			img = habridge.DrawEntitiesOnImage(
				decodedImg,
				curStatus.ChargerPixelX,
				curStatus.ChargerPixelY,
				curStatus.RobotPixelX,
				curStatus.RobotPixelY,
				curStatus.RobotAngle,
				nil,
				nil,
				curStatus.CropMinX,
				curStatus.CropMinY,
			)
			vMap = habridge.ExportImageToValetudoMap(img, *robotSN, curStatus.ChargerPixelX, curStatus.ChargerPixelY, curStatus.RobotPixelX, curStatus.RobotPixelY, curStatus.RobotAngle)
		}

		if vMap == nil || img == nil {
			return
		}

		// Generate PNG with embedded ValetudoMap zTXt chunk (for HA map cards / camera.ozmo_930_map_data)
		pngBytes, err := habridge.EncodePNGWithValetudoMap(img, vMap)
		if err != nil {
			log.Printf("[MAP] Error encoding PNG with map metadata: %v", err)
			return
		}

		// De-duplicate updates based on PNG hash
		h := sha256.Sum256(pngBytes)
		currentHash := fmt.Sprintf("%x", h)
		if currentHash == lastMapDataHash {
			return
		}
		lastMapDataHash = currentHash
		log.Printf("[MAP] Publishing updated map (rawGrid=%v, entities=%d, layers=%d)...",
			usedRawGrid, len(vMap.Entities), len(vMap.Layers))

		// Publish PNG with zTXt chunk to HA camera topic (retained)
		c.Publish(fmt.Sprintf("%s/map_data", topicPrefix), 0, true, pngBytes)

		// Publish raw JPEG to HA camera topic (retained)
		if len(imgBytes) > 0 {
			c.Publish(fmt.Sprintf("%s/map_image", topicPrefix), 0, true, imgBytes)
		} else {
			c.Publish(fmt.Sprintf("%s/map_image", topicPrefix), 0, true, pngBytes)
		}

		// Publish standard JSON to /MapData/map_data topic (for cards that read JSON directly)
		mapData, err := json.Marshal(vMap)
		if err == nil {
			c.Publish(fmt.Sprintf("%s/MapData/map_data", topicPrefix), 0, true, mapData)
		}

		// Publish entity attributes with timestamp to force HA state machine to notify Lovelace
		attrPayload := map[string]interface{}{
			"last_updated":  time.Now().Unix(),
			"robot_pixel_x": curStatus.RobotPixelX,
			"robot_pixel_y": curStatus.RobotPixelY,
			"pose_valid":    curStatus.PoseValid,
			"robot_angle":   curStatus.RobotAngle,
		}
		if attrJSON, err := json.Marshal(attrPayload); err == nil {
			c.Publish(fmt.Sprintf("%s/vacuum_attributes", topicPrefix), 0, false, attrJSON)
		}
		if rooms := habridge.BuildCameraRooms(vMap, roomPolygons); len(rooms) > 0 {
			attrPayload["rooms"] = rooms
		}
		if attrJSON, err := json.Marshal(attrPayload); err == nil {
			c.Publish(fmt.Sprintf("%s/map_attributes", topicPrefix), 0, false, attrJSON)
		}
	}

	// Immediate initial update
	updateMap()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			updateMap()
		}
	}
}

func callAPI(httpCli *http.Client, endpoint string) {
	resp, err := httpCli.Post(*controllerURL+endpoint, "application/json", nil)
	if err != nil {
		log.Printf("[API] Error calling %s: %v", endpoint, err)
		return
	}
	resp.Body.Close()
}

func cbStr(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}
