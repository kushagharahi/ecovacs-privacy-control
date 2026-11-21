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
	"github.com/kushagharahi/ecovacs-privacy-control/pkg/valetudo"
)

var (
	activeGoToCancel context.CancelFunc
	activeGoToLock   sync.Mutex
)

func cancelActiveGoTo() {
	activeGoToLock.Lock()
	defer activeGoToLock.Unlock()
	if activeGoToCancel != nil {
		activeGoToCancel()
		activeGoToCancel = nil
	}
}

var (
	mqttBroker    = flag.String("mqtt-broker", getEnv("MQTT_BROKER", "tcp://192.168.1.10:1883"), "MQTT broker address")
	controllerURL = flag.String("controller-url", getEnv("CONTROLLER_URL", "http://192.168.1.10:8000"), "Ecovacs controller base URL")
	robotSN       = flag.String("robot-sn", getEnv("ROBOT_SN", "E0000000000000000000"), "Robot Serial Number")
	haPrefix      = flag.String("ha-prefix", getEnv("HA_PREFIX", "homeassistant"), "Home Assistant MQTT Discovery prefix")
	valetudoTopic = flag.String("valetudo-topic", getEnv("VALETUDO_TOPIC", "valetudo"), "Valetudo MQTT root topic")
	pollInterval  = flag.Duration("poll-interval", 5*time.Second, "Telemetry polling interval")
	mapInterval   = flag.Duration("map-interval", 3*time.Second, "Map polling interval")

	cropOffsetLock sync.RWMutex
	lastCropMinX   int = 192
	lastCropMinY   int = 281
)

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func main() {
	flag.Parse()

	log.Println("=======================================================")
	log.Println(" Starting Valetudo & Home Assistant MQTT Bridge")
	log.Printf(" - Target Robot SN:    %s", *robotSN)
	log.Printf(" - Controller Base:    %s", *controllerURL)
	log.Printf(" - MQTT Broker:        %s", *mqttBroker)
	log.Printf(" - Valetudo Topic:     %s/%s", *valetudoTopic, *robotSN)
	log.Println("=======================================================")

	topicPrefix := fmt.Sprintf("%s/%s", *valetudoTopic, *robotSN)
	httpClient := &http.Client{Timeout: 5 * time.Second}

	// 1. Configure MQTT Client
	opts := mqtt.NewClientOptions().
		AddBroker(*mqttBroker).
		SetClientID(fmt.Sprintf("valetudo-bridge-%s-%d", *robotSN, time.Now().Unix()%10000)).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second)

	opts.OnConnect = func(c mqtt.Client) {
		log.Println("[MQTT] Connected successfully to broker.")
		// Publish Home Assistant discovery upon connection
		if err := valetudo.PublishHADiscovery(c, *haPrefix, topicPrefix, *robotSN); err != nil {
			log.Printf("[MQTT] Failed to publish HA discovery: %v", err)
		}
		// Subscribe to control topics (both canonical SN topic and alias)
		subscribeTopics(c, topicPrefix, httpClient)
		if topicPrefix != "valetudo/ozmo_930" {
			subscribeTopics(c, "valetudo/ozmo_930", httpClient)
		}
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

func subscribeTopics(c mqtt.Client, topicPrefix string, httpCli *http.Client) {
	// Command Topic (Vacuum actions)
	c.Subscribe(fmt.Sprintf("%s/command", topicPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		cmd := string(m.Payload())
		log.Printf("[CMD] Received vacuum command: %s", cmd)
		cancelActiveGoTo()
		switch cmd {
		case "start", "clean":
			callAPI(httpCli, "/clean?act=start&type=auto")
		case "pause":
			callAPI(httpCli, "/clean?act=pause")
		case "stop":
			callAPI(httpCli, "/clean?act=stop")
		case "return_to_base", "return_home":
			callAPI(httpCli, "/charge")
		case "locate":
			callAPI(httpCli, "/locate")
		}
	})

	// Carpet Boost Topic
	c.Subscribe(fmt.Sprintf("%s/set_carpet_boost", topicPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		payload := strings.ToUpper(strings.TrimSpace(string(m.Payload())))
		enable := payload == "ON" || payload == "TRUE" || payload == "1"
		log.Printf("[CMD] Set carpet boost: %v", enable)
		callAPI(httpCli, fmt.Sprintf("/setCarpetBoost?enable=%v", enable))
		cbStr := "OFF"
		if enable {
			cbStr = "ON"
		}
		c.Publish(fmt.Sprintf("%s/carpet_boost", topicPrefix), 0, true, cbStr)
	})

	// Continuous Cleaning (Breakpoint Resume) Topic
	c.Subscribe(fmt.Sprintf("%s/set_continuous_clean", topicPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		payload := strings.ToUpper(strings.TrimSpace(string(m.Payload())))
		enable := payload == "ON" || payload == "TRUE" || payload == "1"
		log.Printf("[CMD] Set continuous clean: %v", enable)
		callAPI(httpCli, fmt.Sprintf("/setContinuousClean?enable=%v", enable))
		ccStr := "OFF"
		if enable {
			ccStr = "ON"
		}
		c.Publish(fmt.Sprintf("%s/continuous_clean", topicPrefix), 0, true, ccStr)
	})

	// Voice Reports Topic
	c.Subscribe(fmt.Sprintf("%s/set_voice_reports", topicPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		payload := strings.ToUpper(strings.TrimSpace(string(m.Payload())))
		enable := payload == "ON" || payload == "TRUE" || payload == "1"
		log.Printf("[CMD] Set voice reports: %v", enable)
		callAPI(httpCli, fmt.Sprintf("/setVoiceReports?enable=%v", enable))
		vrStr := "OFF"
		if enable {
			vrStr = "ON"
		}
		c.Publish(fmt.Sprintf("%s/voice_reports", topicPrefix), 0, true, vrStr)
	})

	// Do Not Disturb (DND) Topic
	c.Subscribe(fmt.Sprintf("%s/set_dnd", topicPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		payload := strings.ToUpper(strings.TrimSpace(string(m.Payload())))
		enable := payload == "ON" || payload == "TRUE" || payload == "1"
		log.Printf("[CMD] Set DND: %v", enable)
		callAPI(httpCli, fmt.Sprintf("/dnd?enable=%v", enable))
		dndStr := "OFF"
		if enable {
			dndStr = "ON"
		}
		c.Publish(fmt.Sprintf("%s/dnd", topicPrefix), 0, true, dndStr)
	})

	// Consumable Lifespan Reset Topics
	c.Subscribe(fmt.Sprintf("%s/reset_main_brush", topicPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		log.Println("[CMD] Reset Main Brush lifespan")
		callAPI(httpCli, "/resetLifeSpan?component=Brush")
	})
	c.Subscribe(fmt.Sprintf("%s/reset_side_brush", topicPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		log.Println("[CMD] Reset Side Brush lifespan")
		callAPI(httpCli, "/resetLifeSpan?component=SideBrush")
	})
	c.Subscribe(fmt.Sprintf("%s/reset_dust_filter", topicPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		log.Println("[CMD] Reset Dust Filter lifespan")
		callAPI(httpCli, "/resetLifeSpan?component=DustCaseHeap")
	})

	// Volume Topic
	c.Subscribe(fmt.Sprintf("%s/set_volume", topicPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		vol, err := strconv.Atoi(string(m.Payload()))
		if err == nil {
			log.Printf("[CMD] Set volume: %d", vol)
			callAPI(httpCli, fmt.Sprintf("/setVolume?level=%d", vol))
		}
	})

	// Water Flow Topic
	c.Subscribe(fmt.Sprintf("%s/set_water_flow", topicPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
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
	c.Subscribe(fmt.Sprintf("%s/play_sound", topicPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		val := string(m.Payload())
		// Parse ID from format "XX: Name" or direct integer
		parts := strings.Split(val, ":")
		soundID, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err == nil {
			log.Printf("[CMD] Play sound prompt ID %d", soundID)
			callAPI(httpCli, fmt.Sprintf("/playSound?sid=%d", soundID))
		}
	})

	var (
		lastZoneCmdTime    time.Time
		lastZoneCmdLock    sync.Mutex
		lastSegmentCmdTime time.Time
		lastSegmentCmdLock sync.Mutex
		lastGoToCmdTime    time.Time
		lastGoToCmdLock    sync.Mutex
	)

	// Valetudo Map Capabilities: Room/Segment Cleaning
	handleSegmentClean := func(_ mqtt.Client, m mqtt.Message) {
		cancelActiveGoTo()
		lastSegmentCmdLock.Lock()
		if time.Since(lastSegmentCmdTime) < 1*time.Second {
			lastSegmentCmdLock.Unlock()
			return
		}
		lastSegmentCmdTime = time.Now()
		lastSegmentCmdLock.Unlock()

		var req struct {
			SegmentIDs []string `json:"segment_ids"`
		}
		if err := json.Unmarshal(m.Payload(), &req); err == nil && len(req.SegmentIDs) > 0 {
			sa := strings.Join(req.SegmentIDs, ",")
			log.Printf("[CMD] Valetudo MapSegmentation Clean: %s", sa)
			callAPI(httpCli, fmt.Sprintf("/clean?act=s&type=spot_area&sa=%s", sa))
		}
	}
	c.Subscribe(fmt.Sprintf("%s/MapSegmentationCapability/clean/set", topicPrefix), 0, handleSegmentClean)
	c.Subscribe("[[topic]]/MapSegmentationCapability/clean/set", 0, handleSegmentClean)

	// Valetudo Map Capabilities: Zone Cleaning
	handleZoneClean := func(_ mqtt.Client, m mqtt.Message) {
		cancelActiveGoTo()
		lastZoneCmdLock.Lock()
		if time.Since(lastZoneCmdTime) < 1*time.Second {
			lastZoneCmdLock.Unlock()
			return
		}
		lastZoneCmdTime = time.Now()
		lastZoneCmdLock.Unlock()

		log.Printf("[CMD] Valetudo Zone Clean payload: %s", string(m.Payload()))
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
			cropOffsetLock.RLock()
			cMinX, cMinY := lastCropMinX, lastCropMinY
			cropOffsetLock.RUnlock()
			offsetValetudoX := (400 - cMinX) * 50
			offsetValetudoY := (400 - cMinY) * 50

			paX, paY := z.Points.PA.X, z.Points.PA.Y
			pcX, pcY := z.Points.PC.X, z.Points.PC.Y

			// Auto-detect coordinate scale:
			// 1. Normalized ratio (0.0 .. 1.0)
			if math.Max(math.Abs(paX), math.Abs(pcX)) <= 1.0 && math.Max(math.Abs(paY), math.Abs(pcY)) <= 1.0 {
				paX = paX * 256.0 * 50.0
				pcX = pcX * 256.0 * 50.0
				paY = paY * 292.0 * 50.0
				pcY = pcY * 292.0 * 50.0
			} else if math.Max(math.Abs(paX), math.Abs(pcX)) <= 400 && math.Max(math.Abs(paY), math.Abs(pcY)) <= 400 {
				// 2. Raw map pixel coordinates (0 .. 256, 0 .. 292)
				paX = paX * 50.0
				pcX = pcX * 50.0
				paY = paY * 50.0
				pcY = pcY * 50.0
			}
			// 3. Otherwise coordinates are already in Valetudo world coordinates (mm, e.g. 1000..15000)

			// Convert Valetudo mm to robot physical mm
			rx1 := int(paX) - offsetValetudoX
			ry1 := int(paY) - offsetValetudoY
			rx2 := int(pcX) - offsetValetudoX
			ry2 := int(pcY) - offsetValetudoY

			minX := rx1
			maxX := rx2
			if minX > maxX {
				minX, maxX = maxX, minX
			}
			minY := ry1
			maxY := ry2
			if minY > maxY {
				minY, maxY = maxY, minY
			}

			log.Printf("[CMD] Valetudo Zone Clean converted to robot physical mm: (%d,%d) to (%d,%d)", minX, minY, maxX, maxY)
			callAPI(httpCli, fmt.Sprintf("/clean?act=s&type=SpotArea&p=%d,%d,%d,%d", minX, minY, maxX, maxY))
		}
	}
	c.Subscribe(fmt.Sprintf("%s/ZoneCleaningCapability/start/set", topicPrefix), 0, handleZoneClean)
	c.Subscribe("[[topic]]/ZoneCleaningCapability/start/set", 0, handleZoneClean)

	// Valetudo Map Capabilities: GoTo Pin Target
	handleGoTo := func(_ mqtt.Client, m mqtt.Message) {
		cancelActiveGoTo()
		lastGoToCmdLock.Lock()
		if time.Since(lastGoToCmdTime) < 1*time.Second {
			lastGoToCmdLock.Unlock()
			return
		}
		lastGoToCmdTime = time.Now()
		lastGoToCmdLock.Unlock()

		var req struct {
			Coordinates struct {
				X float64 `json:"x"`
				Y float64 `json:"y"`
			} `json:"coordinates"`
		}
		if err := json.Unmarshal(m.Payload(), &req); err == nil {
			cropOffsetLock.RLock()
			cMinX, cMinY := lastCropMinX, lastCropMinY
			cropOffsetLock.RUnlock()
			offsetValetudoX := (400 - cMinX) * 50
			offsetValetudoY := (400 - cMinY) * 50

			x, y := req.Coordinates.X, req.Coordinates.Y
			if math.Max(math.Abs(x), math.Abs(y)) <= 1.0 {
				x = x * 256.0 * 50.0
				y = y * 292.0 * 50.0
			} else if math.Max(math.Abs(x), math.Abs(y)) <= 400 {
				x = x * 50.0
				y = y * 50.0
			}

			rx := int(x) - offsetValetudoX
			ry := int(y) - offsetValetudoY
			log.Printf("[CMD] Valetudo GoTo Pin Target: robot mm (%d, %d)", rx, ry)

			// Set up cancellable arrival watcher with 3-minute timeout
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			activeGoToLock.Lock()
			activeGoToCancel = cancel
			activeGoToLock.Unlock()

			// On OZMO 930, navigate to target point via 500mm x 500mm zone centered at (rx, ry)
			callAPI(httpCli, fmt.Sprintf("/clean?act=s&type=custom&p=%d,%d,%d,%d", rx-250, ry-250, rx+250, ry+250))

			// Background arrival watcher: monitors robot's physical position via /status telemetry
			// When within 350mm of target, stops the clean task so the robot parks and doesn't vacuum
			go func(ctx context.Context, targetX, targetY int) {
				ticker := time.NewTicker(500 * time.Millisecond)
				defer ticker.Stop()
				// Give robot 2 seconds to transition out of dock/idle and begin movement
				time.Sleep(2 * time.Second)

				for {
					select {
					case <-ctx.Done():
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

						// If robot was stopped, returned home, or errored elsewhere, stop tracking
						if st.CleanSpeed == "stop" || st.CleanSpeed == "idle" || st.ChargingState == "SlotCharging" {
							return
						}

						// Calculate current robot physical coordinate in mm
						currX := (st.RobotPosX - 400) * 50
						currY := (st.RobotPosY - 400) * 50
						dx := float64(targetX - currX)
						dy := float64(targetY - currY)
						dist := math.Sqrt(dx*dx + dy*dy)

						// When robot arrives within 350mm (~14 inches, vacuum radius ~175mm) of target point
						if dist <= 350 {
							log.Printf("[GOTO] Destination reached! Robot at (%d, %d), target (%d, %d), dist=%.0fmm. Halting clean so robot parks without cleaning.", currX, currY, targetX, targetY, dist)
							callAPI(httpCli, "/clean?act=stop")
							return
						}
					}
				}
			}(ctx, rx, ry)
		}
	}
	c.Subscribe(fmt.Sprintf("%s/GoToLocationCapability/target/set", topicPrefix), 0, handleGoTo)
	c.Subscribe(fmt.Sprintf("%s/GoToLocationCapability/go/set", topicPrefix), 0, handleGoTo)
	c.Subscribe("[[topic]]/GoToLocationCapability/go/set", 0, handleGoTo)

	// Valetudo Spot Cleaning Capability (Pin & Clean)
	handleSpotClean := func(_ mqtt.Client, m mqtt.Message) {
		log.Printf("[CMD] Valetudo SpotClean Target payload: %s", string(m.Payload()))
		var req struct {
			Coordinates struct {
				X float64 `json:"x"`
				Y float64 `json:"y"`
			} `json:"coordinates"`
		}
		if err := json.Unmarshal(m.Payload(), &req); err == nil {
			cropOffsetLock.RLock()
			cMinX, cMinY := lastCropMinX, lastCropMinY
			cropOffsetLock.RUnlock()
			offsetValetudoX := (400 - cMinX) * 50
			offsetValetudoY := (400 - cMinY) * 50

			x, y := req.Coordinates.X, req.Coordinates.Y
			if math.Max(math.Abs(x), math.Abs(y)) <= 1.0 {
				x = x * 256.0 * 50.0
				y = y * 292.0 * 50.0
			} else if math.Max(math.Abs(x), math.Abs(y)) <= 400 {
				x = x * 50.0
				y = y * 50.0
			}

			rx := int(x) - offsetValetudoX
			ry := int(y) - offsetValetudoY
			log.Printf("[CMD] Valetudo Spot Clean Pin Target: robot mm (%d, %d)", rx, ry)

			// Clean a 1.0m x 1.0m zone centered at (rx, ry) with suction ON
			callAPI(httpCli, fmt.Sprintf("/clean?act=s&type=custom&p=%d,%d,%d,%d", rx-500, ry-500, rx+500, ry+500))
		}
	}
	c.Subscribe(fmt.Sprintf("%s/SpotCleaningCapability/clean/set", topicPrefix), 0, handleSpotClean)
	c.Subscribe("[[topic]]/SpotCleaningCapability/clean/set", 0, handleSpotClean)

	// Valetudo Map Capabilities: Virtual Restrictions (Walls and No-Go Areas)
	c.Subscribe(fmt.Sprintf("%s/CombinedVirtualRestrictionsCapability/set", topicPrefix), 0, func(_ mqtt.Client, m mqtt.Message) {
		log.Printf("[CMD] Valetudo CombinedVirtualRestrictions set: %s", string(m.Payload()))
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

			offsetValetudoX := (400 - cropMinX) * 50
			offsetValetudoY := (400 - cropMinY) * 50

			var walls [][4]int
			for _, w := range req.VirtualWalls {
				rx1 := int(w.Points.PA.X) - offsetValetudoX
				ry1 := int(w.Points.PA.Y) - offsetValetudoY
				rx2 := int(w.Points.PB.X) - offsetValetudoX
				ry2 := int(w.Points.PB.Y) - offsetValetudoY
				walls = append(walls, [4]int{rx1, ry1, rx2, ry2})
			}

			var zones [][8]int
			for _, z := range req.NoGoAreas {
				rx1 := int(z.Points.PA.X) - offsetValetudoX
				ry1 := int(z.Points.PA.Y) - offsetValetudoY
				rx2 := int(z.Points.PB.X) - offsetValetudoX
				ry2 := int(z.Points.PB.Y) - offsetValetudoY
				rx3 := int(z.Points.PC.X) - offsetValetudoX
				ry3 := int(z.Points.PC.Y) - offsetValetudoY
				rx4 := int(z.Points.PD.X) - offsetValetudoX
				ry4 := int(z.Points.PD.Y) - offsetValetudoY
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

			c.Publish(fmt.Sprintf("%s/CombinedVirtualRestrictionsCapability", topicPrefix), 0, true, m.Payload())
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

			// Map to standard Valetudo state and publish combined JSON to state_topic
			vState := valetudo.MapEcovacsStateToValetudo(status.ChargingState, status.CleanSpeed)
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

		var vMap *valetudo.ValetudoMap
		var img image.Image
		usedRawGrid := false

		// 1. Try to fetch high-fidelity raw grid directly from controller
		if gResp, err := httpCli.Get(*controllerURL + "/mapGrid"); err == nil {
			var mapGrid ecovacs.MapGridResponse
			if json.NewDecoder(gResp.Body).Decode(&mapGrid) == nil && len(mapGrid.Grid) > 0 {
				cropOffsetLock.Lock()
				lastCropMinX = mapGrid.CropMinX
				lastCropMinY = mapGrid.CropMinY
				cropOffsetLock.Unlock()

				vMap = valetudo.ExportGridToValetudoMap(
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
				img = valetudo.GridToImage(
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
				offsetValetudoX := (400 - mapGrid.CropMinX) * 50
				offsetValetudoY := (400 - mapGrid.CropMinY) * 50
				var vwList []map[string]interface{}
				var nogoList []map[string]interface{}
				for _, r := range mapGrid.VirtualRestrictions {
					if r.Type == "virtual_wall" && len(r.Points) == 4 {
						vwList = append(vwList, map[string]interface{}{
							"points": map[string]interface{}{
								"pA": map[string]int{"x": r.Points[0] + offsetValetudoX, "y": r.Points[1] + offsetValetudoY},
								"pB": map[string]int{"x": r.Points[2] + offsetValetudoX, "y": r.Points[3] + offsetValetudoY},
							},
						})
					} else if r.Type == "no_go_area" && len(r.Points) == 8 {
						nogoList = append(nogoList, map[string]interface{}{
							"points": map[string]interface{}{
								"pA": map[string]int{"x": r.Points[0] + offsetValetudoX, "y": r.Points[1] + offsetValetudoY},
								"pB": map[string]int{"x": r.Points[2] + offsetValetudoX, "y": r.Points[3] + offsetValetudoY},
								"pC": map[string]int{"x": r.Points[4] + offsetValetudoX, "y": r.Points[5] + offsetValetudoY},
								"pD": map[string]int{"x": r.Points[6] + offsetValetudoX, "y": r.Points[7] + offsetValetudoY},
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
			img = valetudo.DrawEntitiesOnImage(
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
			vMap = valetudo.ExportImageToValetudoMap(img, *robotSN, curStatus.ChargerPixelX, curStatus.ChargerPixelY, curStatus.RobotPixelX, curStatus.RobotPixelY, curStatus.RobotAngle)
		}

		if vMap == nil || img == nil {
			return
		}

		// Generate PNG with embedded ValetudoMap zTXt chunk (for valetudo-map-card / camera.ozmo_930_map_data)
		pngBytes, err := valetudo.EncodePNGWithValetudoMap(img, vMap)
		if err != nil {
			log.Printf("[MAP] Error encoding PNG with Valetudo metadata: %v", err)
			return
		}

		// De-duplicate updates based on PNG hash
		h := sha256.Sum256(pngBytes)
		currentHash := fmt.Sprintf("%x", h)
		if currentHash == lastMapDataHash {
			return
		}
		lastMapDataHash = currentHash
		log.Printf("[MAP] Publishing updated Valetudo map (rawGrid=%v, entities=%d, layers=%d)...",
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
			"robot_angle":   curStatus.RobotAngle,
		}
		if attrJSON, err := json.Marshal(attrPayload); err == nil {
			c.Publish(fmt.Sprintf("%s/map_attributes", topicPrefix), 0, false, attrJSON)
			c.Publish(fmt.Sprintf("%s/vacuum_attributes", topicPrefix), 0, false, attrJSON)
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
