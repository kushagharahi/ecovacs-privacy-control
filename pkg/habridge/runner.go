package habridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/kushagharahi/ecovacs-privacy-control/pkg/ecovacs"
)

// Config configures the Home Assistant MQTT bridge.
type Config struct {
	MQTTBroker    string
	ControllerURL string
	RobotSN       string
	HAPrefix      string
	MQTTTopic     string
	PollInterval  time.Duration
	MapInterval   time.Duration
}

// ApplyDefaults fills empty Config fields from standard environment variables and defaults.
func (c *Config) ApplyDefaults() {
	if c.MQTTBroker == "" {
		c.MQTTBroker = GetEnv("MQTT_BROKER", "")
	}
	if c.ControllerURL == "" {
		c.ControllerURL = GetEnv("CONTROLLER_URL", "http://127.0.0.1:8000")
	}
	if c.RobotSN == "" {
		c.RobotSN = GetEnv("ROBOT_SN", "")
	}
	if c.HAPrefix == "" {
		c.HAPrefix = GetEnv("HA_PREFIX", "homeassistant")
	}
	if c.MQTTTopic == "" {
		c.MQTTTopic = GetEnv("MQTT_TOPIC", GetEnv("VALETUDO_TOPIC", "valetudo"))
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Second
	}
	if c.MapInterval <= 0 {
		c.MapInterval = 3 * time.Second
	}
}

// Bridge polls ecovacs-controller HTTP endpoints and bridges state, maps, and commands over MQTT.
type Bridge struct {
	cfg     Config
	httpCli *http.Client

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

	cropOffsetLock sync.RWMutex
	lastCropMinX   int
	lastCropMinY   int
	lastMapW       int
	lastMapH       int
}

// NewBridge creates a Bridge instance with default map viewport bounds.
func NewBridge(cfg Config, httpCli *http.Client) *Bridge {
	cfg.ApplyDefaults()
	if httpCli == nil {
		httpCli = &http.Client{Timeout: 5 * time.Second}
	}
	return &Bridge{
		cfg:          cfg,
		httpCli:      httpCli,
		lastCropMinX: 192,
		lastCropMinY: 281,
		lastMapW:     256,
		lastMapH:     292,
	}
}

// GetEnv returns os.Getenv(key) or fallback if unset/empty.
func GetEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// UniqueStrings returns a deduplicated slice preserving input order and dropping empty strings.
func UniqueStrings(items []string) []string {
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

// DebounceWithin returns true at most once per window using lock and lastTime.
func DebounceWithin(lock *sync.Mutex, lastTime *time.Time, window time.Duration) bool {
	lock.Lock()
	defer lock.Unlock()
	if time.Since(*lastTime) < window {
		return false
	}
	*lastTime = time.Now()
	return true
}

// Run connects to MQTT and runs telemetry and map publishing loops until ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	cfg.ApplyDefaults()
	if cfg.MQTTBroker == "" {
		return fmt.Errorf("MQTT broker address is required (set -mqtt-broker flag or MQTT_BROKER env var)")
	}
	if cfg.RobotSN == "" {
		return fmt.Errorf("Robot Serial Number is required (set -robot-sn flag or ROBOT_SN env var)")
	}

	ecovacs.InitSounds()
	b := NewBridge(cfg, nil)

	topicPrefix := fmt.Sprintf("%s/%s", cfg.MQTTTopic, cfg.RobotSN)
	subPrefixes := UniqueStrings([]string{
		topicPrefix,
		fmt.Sprintf("valetudo/%s", cfg.RobotSN),
		"valetudo/ozmo_930",
	})

	log.Println("=======================================================")
	log.Println(" Starting Home Assistant MQTT Bridge (ha-bridge)")
	log.Printf(" - Target Robot SN:    %s", cfg.RobotSN)
	log.Printf(" - Controller Base:    %s", cfg.ControllerURL)
	log.Printf(" - MQTT Broker:        %s", cfg.MQTTBroker)
	log.Printf(" - Primary Topic:      %s", topicPrefix)
	log.Printf(" - Command Prefixes:   %s", strings.Join(subPrefixes, ", "))
	log.Println("=======================================================")

	opts := mqtt.NewClientOptions().
		AddBroker(cfg.MQTTBroker).
		SetClientID(fmt.Sprintf("ha-bridge-%s-%d", cfg.RobotSN, time.Now().Unix()%10000)).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second)

	opts.OnConnect = func(c mqtt.Client) {
		log.Println("[MQTT] Connected successfully to broker.")
		if err := PublishHADiscovery(c, cfg.HAPrefix, topicPrefix, cfg.RobotSN); err != nil {
			log.Printf("[MQTT] Failed to publish HA discovery: %v", err)
		}
		for _, p := range subPrefixes {
			b.subscribeTopics(c, p, topicPrefix)
		}
		b.subscribeTemplateTopics(c)
	}

	opts.OnConnectionLost = func(_ mqtt.Client, err error) {
		log.Printf("[MQTT] Connection lost: %v", err)
	}

	mqttClient := mqtt.NewClient(opts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		return fmt.Errorf("MQTT connection failed: %w", token.Error())
	}

	go b.startTelemetryLoop(ctx, mqttClient, topicPrefix)
	go b.startMapLoop(ctx, mqttClient, topicPrefix)

	<-ctx.Done()
	log.Println("[BRIDGE] Shutting down...")
	b.CancelActiveGoTo()
	mqttClient.Disconnect(250)
	log.Println("[BRIDGE] Exited cleanly.")
	return nil
}

// SetMapViewport sets map viewport dimensions and crop bounds for coordinate conversions.
func (b *Bridge) SetMapViewport(cropMinX, cropMinY, mapW, mapH int) {
	b.cropOffsetLock.Lock()
	b.lastCropMinX = cropMinX
	b.lastCropMinY = cropMinY
	b.lastMapW = mapW
	b.lastMapH = mapH
	b.cropOffsetLock.Unlock()
}

// SetActiveGoToForTest installs a cancel func and done channel for testing CancelActiveGoTo.
func (b *Bridge) SetActiveGoToForTest(cancel context.CancelFunc, done chan struct{}) {
	b.activeGoToLock.Lock()
	b.activeGoToID++
	b.activeGoToCancel = cancel
	b.activeGoToDone = done
	b.activeGoToLock.Unlock()
}

// CancelActiveGoTo cancels any running GoTo arrival watcher and halts motion once.
func (b *Bridge) CancelActiveGoTo() {
	b.activeGoToLock.Lock()
	cancel := b.activeGoToCancel
	done := b.activeGoToDone
	b.activeGoToCancel = nil
	b.activeGoToDone = nil
	b.activeGoToID++
	b.activeGoToLock.Unlock()

	if cancel != nil {
		cancel()
		if done != nil {
			select {
			case <-done:
			case <-time.After(500 * time.Millisecond):
			}
		}
		if b.httpCli != nil {
			b.callAPI("/clean?act=stop")
			b.callAPI("/silentGoto?enable=0")
		}
	}
}

// WorldCoordsToRobotMM converts normalized ratios, cropped pixels, or world mm coordinates into robot mm.
func (b *Bridge) WorldCoordsToRobotMM(pts ...[2]float64) [][2]int {
	b.cropOffsetLock.RLock()
	cMinX, cMinY := b.lastCropMinX, b.lastCropMinY
	mapW, mapH := b.lastMapW, b.lastMapH
	b.cropOffsetLock.RUnlock()

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
		if maxX <= 1.0 && maxY <= 1.0 {
			x = x * float64(mapW) * 50.0
			y = y * float64(mapH) * 50.0
		} else if maxX <= 400 && maxY <= 400 {
			x = x * 50.0
			y = y * 50.0
		}
		out[i] = [2]int{int(x) - offsetWorldX, int(y) - offsetWorldY}
	}
	return out
}

func (b *Bridge) handleSegmentClean() mqtt.MessageHandler {
	return func(_ mqtt.Client, m mqtt.Message) {
		if !DebounceWithin(&b.lastSegmentCmdLock, &b.lastSegmentCmdTime, time.Second) {
			return
		}
		b.CancelActiveGoTo()

		var req struct {
			SegmentIDs []string `json:"segment_ids"`
		}
		if err := json.Unmarshal(m.Payload(), &req); err == nil && len(req.SegmentIDs) > 0 {
			sa := strings.Join(req.SegmentIDs, ",")
			log.Printf("[CMD] MapSegmentation Clean: %s", sa)
			b.callAPI("/silentGoto?enable=0")
			b.callAPI(fmt.Sprintf("/clean?act=s&type=spot_area&sa=%s", sa))
		}
	}
}

func (b *Bridge) handleZoneClean() mqtt.MessageHandler {
	return func(_ mqtt.Client, m mqtt.Message) {
		if !DebounceWithin(&b.lastZoneCmdLock, &b.lastZoneCmdTime, time.Second) {
			return
		}
		b.CancelActiveGoTo()

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
			pts := b.WorldCoordsToRobotMM(
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
			b.callAPI("/silentGoto?enable=0")
			b.callAPI(fmt.Sprintf("/clean?act=s&type=custom&p=%d,%d,%d,%d", minX, minY, maxX, maxY))
		}
	}
}

func (b *Bridge) handleGoTo() mqtt.MessageHandler {
	return func(_ mqtt.Client, m mqtt.Message) {
		if !DebounceWithin(&b.lastGoToCmdLock, &b.lastGoToCmdTime, time.Second) {
			return
		}
		b.CancelActiveGoTo()

		var req struct {
			Coordinates struct {
				X float64 `json:"x"`
				Y float64 `json:"y"`
			} `json:"coordinates"`
		}
		if err := json.Unmarshal(m.Payload(), &req); err == nil {
			pts := b.WorldCoordsToRobotMM([2]float64{req.Coordinates.X, req.Coordinates.Y})
			rx, ry := pts[0][0], pts[0][1]
			log.Printf("[CMD] GoTo Pin Target: robot mm (%d, %d)", rx, ry)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			done := make(chan struct{})
			b.activeGoToLock.Lock()
			b.activeGoToID++
			watcherID := b.activeGoToID
			b.activeGoToCancel = cancel
			b.activeGoToDone = done
			b.activeGoToLock.Unlock()

			b.callAPI("/silentGoto?enable=1")
			time.Sleep(300 * time.Millisecond)

			b.callAPI(fmt.Sprintf("/clean?act=s&type=custom&p=%d,%d,%d,%d", rx-250, ry-250, rx+250, ry+250))

			go b.runGoToWatcher(ctx, watcherID, done, rx, ry)
		}
	}
}

func (b *Bridge) runGoToWatcher(ctx context.Context, wID uint64, wDone chan struct{}, targetX, targetY int) {
	defer func() {
		b.activeGoToLock.Lock()
		if b.activeGoToID == wID {
			b.activeGoToCancel = nil
			b.activeGoToDone = nil
		}
		b.activeGoToLock.Unlock()
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
			b.callAPI("/clean?act=stop")
			b.callAPI("/silentGoto?enable=0")
			return
		case <-ticker.C:
			resp, err := b.httpCli.Get(b.cfg.ControllerURL + "/status")
			if err != nil {
				continue
			}
			var st ecovacs.StatusResponse
			if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
				resp.Body.Close()
				continue
			}
			resp.Body.Close()

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
					b.callAPI("/clean?act=stop")
					b.callAPI("/silentGoto?enable=0")
					return
				} else {
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

			if dist <= 450 {
				log.Printf("[GOTO] Destination reached! Robot at (%d, %d), target (%d, %d), dist=%.0fmm. Halting motion.", currX, currY, targetX, targetY, dist)
				b.callAPI("/clean?act=stop")
				time.Sleep(300 * time.Millisecond)
				b.callAPI("/silentGoto?enable=0")
				return
			}

			if hasStartedMoving && (st.ChargingState == "SlotCharging" || st.ChargingState == "Charging" || st.ChargingState == "Docked") {
				log.Printf("[GOTO] Robot docked externally (state=%s), ending watcher", st.ChargingState)
				b.callAPI("/silentGoto?enable=0")
				return
			}

			if hasConfirmedCleaning && (st.ChargingState == "Idle" || st.ChargingState == "Paused") {
				log.Printf("[GOTO] Clean ended externally (state=%s), ending watcher", st.ChargingState)
				b.callAPI("/clean?act=stop")
				b.callAPI("/silentGoto?enable=0")
				return
			}
		}
	}
}

func (b *Bridge) handleSpotClean() mqtt.MessageHandler {
	return func(_ mqtt.Client, m mqtt.Message) {
		b.CancelActiveGoTo()
		log.Printf("[CMD] SpotClean Target payload: %s", string(m.Payload()))
		var req struct {
			Coordinates struct {
				X float64 `json:"x"`
				Y float64 `json:"y"`
			} `json:"coordinates"`
		}
		if err := json.Unmarshal(m.Payload(), &req); err == nil {
			pts := b.WorldCoordsToRobotMM([2]float64{req.Coordinates.X, req.Coordinates.Y})
			rx, ry := pts[0][0], pts[0][1]
			log.Printf("[CMD] Spot Clean Pin Target: robot mm (%d, %d)", rx, ry)

			b.callAPI("/silentGoto?enable=0")
			b.callAPI(fmt.Sprintf("/clean?act=s&type=custom&p=%d,%d,%d,%d", rx-500, ry-500, rx+500, ry+500))
		}
	}
}

func (b *Bridge) subscribeTemplateTopics(c mqtt.Client) {
	c.Subscribe("[[topic]]/MapSegmentationCapability/clean/set", 0, b.handleSegmentClean())
	c.Subscribe("[[topic]]/ZoneCleaningCapability/start/set", 0, b.handleZoneClean())
	c.Subscribe("[[topic]]/GoToLocationCapability/go/set", 0, b.handleGoTo())
	c.Subscribe("[[topic]]/SpotCleaningCapability/clean/set", 0, b.handleSpotClean())
}

func (b *Bridge) subscribeTopics(c mqtt.Client, subPrefix, publishPrefix string) {
	c.Subscribe(fmt.Sprintf("%s/command", subPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		cmd := string(m.Payload())
		log.Printf("[CMD] Received vacuum command: %s", cmd)
		switch cmd {
		case "start", "clean":
			b.CancelActiveGoTo()
			b.callAPI("/silentGoto?enable=0")
			b.callAPI("/clean?act=start&type=auto")
		case "pause":
			b.CancelActiveGoTo()
			b.callAPI("/clean?act=pause")
		case "stop":
			b.CancelActiveGoTo()
			b.callAPI("/clean?act=stop")
		case "return_to_base", "return_home":
			b.CancelActiveGoTo()
			b.callAPI("/charge")
		case "locate":
			b.callAPI("/locate")
		}
	})

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
			b.callAPI(fmt.Sprintf(sw.endpoint, enable))
			c.Publish(fmt.Sprintf("%s/%s", publishPrefix, sw.stateTopic), 0, true, cbStr(enable))
		})
	}

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
			b.callAPI("/resetLifeSpan?component=" + btn.component)
		})
	}

	c.Subscribe(fmt.Sprintf("%s/set_volume", subPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		vol, err := strconv.Atoi(string(m.Payload()))
		if err == nil {
			log.Printf("[CMD] Set volume: %d", vol)
			b.callAPI(fmt.Sprintf("/setVolume?level=%d", vol))
		}
	})

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
		b.callAPI(fmt.Sprintf("/setWater?level=%d", flowVal))
	})

	c.Subscribe(fmt.Sprintf("%s/play_sound", subPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		val := string(m.Payload())
		parts := strings.Split(val, ":")
		soundID, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err == nil {
			log.Printf("[CMD] Play sound prompt ID %d", soundID)
			b.callAPI(fmt.Sprintf("/playSound?sid=%d", soundID))
			c.Publish(fmt.Sprintf("%s/last_sound", publishPrefix), 0, true, val)
		}
	})

	c.Subscribe(fmt.Sprintf("%s/MapSegmentationCapability/clean/set", subPrefix), 0, b.handleSegmentClean())
	c.Subscribe(fmt.Sprintf("%s/ZoneCleaningCapability/start/set", subPrefix), 0, b.handleZoneClean())
	c.Subscribe(fmt.Sprintf("%s/GoToLocationCapability/target/set", subPrefix), 0, b.handleGoTo())
	c.Subscribe(fmt.Sprintf("%s/GoToLocationCapability/go/set", subPrefix), 0, b.handleGoTo())
	c.Subscribe(fmt.Sprintf("%s/SpotCleaningCapability/clean/set", subPrefix), 0, b.handleSpotClean())

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
			if gResp, err := b.httpCli.Get(b.cfg.ControllerURL + "/mapGrid"); err == nil {
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

			pResp, err := b.httpCli.Post(b.cfg.ControllerURL+"/virtualRestrictions", "application/json", bytes.NewReader(payloadBytes))
			if err == nil {
				pResp.Body.Close()
			}

			c.Publish(fmt.Sprintf("%s/CombinedVirtualRestrictionsCapability", publishPrefix), 0, true, m.Payload())
		}
	})
}

func (b *Bridge) startTelemetryLoop(ctx context.Context, c mqtt.Client, topicPrefix string) {
	ticker := time.NewTicker(b.cfg.PollInterval)
	defer ticker.Stop()

	publishTelemetry := func() {
		resp, err := b.httpCli.Get(b.cfg.ControllerURL + "/status")
		if err != nil {
			return
		}

		var status ecovacs.StatusResponse
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			resp.Body.Close()
			return
		}
		resp.Body.Close()

		vState := MapEcovacsState(status.ChargingState, status.CleanSpeed)
		statePayload := map[string]interface{}{
			"state":         string(vState),
			"battery_level": status.BatteryLevel,
		}
		stateJSON, _ := json.Marshal(statePayload)
		c.Publish(fmt.Sprintf("%s/state", topicPrefix), 0, true, stateJSON)
		c.Publish(fmt.Sprintf("%s/battery", topicPrefix), 0, true, strconv.Itoa(status.BatteryLevel))
		c.Publish(fmt.Sprintf("%s/charging", topicPrefix), 0, true, status.ChargingState)

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

	publishTelemetry()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publishTelemetry()
		}
	}
}

func (b *Bridge) startMapLoop(ctx context.Context, c mqtt.Client, topicPrefix string) {
	ticker := time.NewTicker(b.cfg.MapInterval)
	defer ticker.Stop()

	var lastMapDataHash string

	updateMap := func() {
		var curStatus ecovacs.StatusResponse
		if sResp, err := b.httpCli.Get(b.cfg.ControllerURL + "/status"); err == nil {
			_ = json.NewDecoder(sResp.Body).Decode(&curStatus)
			sResp.Body.Close()
		}

		var vMap *ValetudoMap
		var img image.Image
		var roomPolygons map[string][][2]int
		usedRawGrid := false
		mapGridOK := false

		attrRobotX := curStatus.RobotPixelX
		attrRobotY := curStatus.RobotPixelY
		attrChargerX := curStatus.ChargerPixelX
		attrChargerY := curStatus.ChargerPixelY
		attrPoseValid := curStatus.PoseValid
		attrAngle := curStatus.RobotAngle
		attrCropMinX := curStatus.CropMinX
		attrCropMinY := curStatus.CropMinY

		if gResp, err := b.httpCli.Get(b.cfg.ControllerURL + "/mapGrid"); err == nil {
			var mapGrid ecovacs.MapGridResponse
			if gResp.StatusCode == http.StatusOK && json.NewDecoder(gResp.Body).Decode(&mapGrid) == nil {
				mapGridOK = true
				if len(mapGrid.Grid) > 0 {
					roomPolygons = mapGrid.RoomPolygons
					b.cropOffsetLock.Lock()
					b.lastCropMinX = mapGrid.CropMinX
					b.lastCropMinY = mapGrid.CropMinY
					b.lastMapW = len(mapGrid.Grid)
					b.lastMapH = len(mapGrid.Grid[0])
					b.cropOffsetLock.Unlock()

					if (curStatus.ChargingState == "SlotCharging" || curStatus.ChargingState == "Charging") &&
						mapGrid.ChargerPixelX >= 0 && mapGrid.ChargerPixelY >= 0 {
						mapGrid.RobotPixelX = mapGrid.ChargerPixelX
						mapGrid.RobotPixelY = mapGrid.ChargerPixelY
						mapGrid.PoseValid = true
					}

					attrRobotX = mapGrid.RobotPixelX
					attrRobotY = mapGrid.RobotPixelY
					attrChargerX = mapGrid.ChargerPixelX
					attrChargerY = mapGrid.ChargerPixelY
					attrPoseValid = mapGrid.PoseValid
					attrAngle = mapGrid.RobotAngle
					attrCropMinX = mapGrid.CropMinX
					attrCropMinY = mapGrid.CropMinY

					vMap = ExportGridToValetudoMap(
						mapGrid.Grid,
						b.cfg.RobotSN,
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
					img = GridToImage(
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
				} else {
					img = ecovacs.RenderNoMapImage()
					vMap = NewEmptyValetudoMap(b.cfg.RobotSN)
					c.Publish(fmt.Sprintf("%s/CombinedVirtualRestrictionsCapability", topicPrefix), 0, true, `{"virtual_walls":[],"no_go_areas":[]}`)
				}
			}
			gResp.Body.Close()
		}

		var imgBytes []byte
		if !usedRawGrid && !mapGridOK {
			resp, err := b.httpCli.Get(b.cfg.ControllerURL + "/getMapData")
			if err != nil {
				return
			}
			isEmptyHeader := resp.Header.Get("X-Map-Empty") == "true"
			imgBytes, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || len(imgBytes) == 0 {
				return
			}

			decodedImg, _, err := image.Decode(bytes.NewReader(imgBytes))
			if err != nil {
				return
			}
			if isEmptyHeader || decodedImg.Bounds().Dx() <= 1 || decodedImg.Bounds().Dy() <= 1 {
				img = ecovacs.RenderNoMapImage()
				vMap = NewEmptyValetudoMap(b.cfg.RobotSN)
				imgBytes = nil
			} else {
				img = DrawEntitiesOnImage(
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
				vMap = ExportImageToValetudoMap(img, b.cfg.RobotSN, curStatus.ChargerPixelX, curStatus.ChargerPixelY, curStatus.RobotPixelX, curStatus.RobotPixelY, curStatus.RobotAngle)
			}
		}

		if vMap == nil || img == nil {
			return
		}

		pngBytes, err := EncodePNGWithValetudoMap(img, vMap)
		if err != nil {
			log.Printf("[MAP] Error encoding PNG with map metadata: %v", err)
			return
		}

		h := sha256.Sum256(pngBytes)
		currentHash := fmt.Sprintf("%x", h)
		if currentHash == lastMapDataHash {
			return
		}
		lastMapDataHash = currentHash
		log.Printf("[MAP] Publishing updated map (rawGrid=%v, entities=%d, layers=%d)...",
			usedRawGrid, len(vMap.Entities), len(vMap.Layers))

		c.Publish(fmt.Sprintf("%s/map_data", topicPrefix), 0, true, pngBytes)

		if len(imgBytes) > 0 {
			c.Publish(fmt.Sprintf("%s/map_image", topicPrefix), 0, true, imgBytes)
		} else {
			c.Publish(fmt.Sprintf("%s/map_image", topicPrefix), 0, true, pngBytes)
		}

		if mapData, err := json.Marshal(vMap); err == nil {
			c.Publish(fmt.Sprintf("%s/MapData/map_data", topicPrefix), 0, true, mapData)
		}

		attrPayload := map[string]interface{}{
			"last_updated":    time.Now().Unix(),
			"robot_pixel_x":   attrRobotX,
			"robot_pixel_y":   attrRobotY,
			"charger_pixel_x": attrChargerX,
			"charger_pixel_y": attrChargerY,
			"pose_valid":      attrPoseValid,
			"robot_angle":     attrAngle,
			"crop_min_x":      attrCropMinX,
			"crop_min_y":      attrCropMinY,
		}
		if attrJSON, err := json.Marshal(attrPayload); err == nil {
			c.Publish(fmt.Sprintf("%s/vacuum_attributes", topicPrefix), 0, true, attrJSON)
		}
		if rooms := BuildCameraRooms(vMap, roomPolygons); len(rooms) > 0 {
			attrPayload["rooms"] = rooms
		}
		if attrJSON, err := json.Marshal(attrPayload); err == nil {
			c.Publish(fmt.Sprintf("%s/map_attributes", topicPrefix), 0, true, attrJSON)
		}
	}

	updateMap()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			updateMap()
		}
	}
}

func (b *Bridge) callAPI(endpoint string) {
	resp, err := b.httpCli.Post(b.cfg.ControllerURL+endpoint, "application/json", nil)
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
