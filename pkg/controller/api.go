package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/kushagharahi/ecovacs-privacy-control/pkg/ecovacs"
)

var (
	silentGotoFilePath = "/tmp/silent_goto"
	gotoTargetFilePath = "/tmp/goto_target"
	gotoStopFilePath   = "/tmp/goto_stop"
)

func setupApi(ctx context.Context, apiPort string) error {
	if apiPort == "" {
		apiPort = "8000"
	}
	r := mux.NewRouter()
	r.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}).Methods("GET")
	r.HandleFunc("/getMapData", getMapData).Methods("GET", "HEAD")
	r.HandleFunc("/mapGrid", handleMapGrid).Methods("GET", "HEAD")
	r.HandleFunc("/status", getStatus).Methods("GET")
	r.HandleFunc("/clean", handleClean).Methods("POST", "GET")
	r.HandleFunc("/charge", handleCharge).Methods("POST", "GET")
	r.HandleFunc("/setVolume", handleSetVolume).Methods("POST")
	r.HandleFunc("/setWater", handleSetWater).Methods("POST")
	r.HandleFunc("/locate", handleLocate).Methods("POST", "GET")
	r.HandleFunc("/move", handleMove).Methods("POST", "GET")
	r.HandleFunc("/setFanSpeed", handleSetFanSpeed).Methods("POST")
	r.HandleFunc("/setCarpetBoost", handleSetCarpetBoost).Methods("POST", "GET")
	r.HandleFunc("/setContinuousClean", handleSetContinuousClean).Methods("POST", "GET")
	r.HandleFunc("/setVoiceReports", handleSetVoiceReports).Methods("POST", "GET")
	r.HandleFunc("/dnd", handleDND).Methods("POST", "GET")
	r.HandleFunc("/resetLifeSpan", handleResetLifeSpan).Methods("POST")
	r.HandleFunc("/sounds", handleListSounds).Methods("GET")
	r.HandleFunc("/playSound", handlePlaySound).Methods("POST", "GET")
	r.HandleFunc("/virtualRestrictions", handleVirtualRestrictions).Methods("GET", "POST")
	r.HandleFunc("/rooms/rename", handleRenameRooms).Methods("POST")
	r.HandleFunc("/silentGoto", handleSilentGoto).Methods("POST", "GET")
	r.HandleFunc("/goTo", handleGoTo).Methods("POST", "GET")
	r.HandleFunc("/recoverMap", handleRecoverMap).Methods("POST", "GET")

	ln, err := net.Listen("tcp", ":"+apiPort)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: r}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[API] REST server error: %v", err)
		}
	}()
	return nil
}

func getMapData(res http.ResponseWriter, req *http.Request) {
	publishXML(GetMapM)
	publishXML(GetMapSet)
	publishXML(GetMapVirtualWalls)
	publishXML(IfHaveMap)
	publishXML(GetMapSt)
	publishXML(SetMapAutoReport(true))

	img := getImageFromMapGrid()
	buffer := new(bytes.Buffer)
	if err := jpeg.Encode(buffer, img, nil); err != nil {
		log.Println("unable to encode image.")
	}

	res.Header().Set("Content-Type", "image/jpeg")
	res.Header().Set("Content-Length", strconv.Itoa(len(buffer.Bytes())))
	if _, err := res.Write(buffer.Bytes()); err != nil {
		log.Println("unable to write image.")
	}
}

type MapGridResponse = ecovacs.MapGridResponse

func handleMapGrid(res http.ResponseWriter, req *http.Request) {
	publishXML(GetMapVirtualWalls)
	grid, minX, minY, maxX, maxY := GetMapGridRaw()
	w, h := 0, 0
	if len(grid) > 0 {
		w = len(grid)
		h = len(grid[0])
	}

	statusMu.RLock()
	pixRobotX := RobotPosX - minX
	pixRobotY := RobotPosY - minY
	pixChargerX := ChargerPosX - minX
	pixChargerY := ChargerPosY - minY
	chargingState := ChargingState
	poseValid := RobotPoseValid
	robotAngle := RobotAngle
	roomsCopy := append([]string(nil), DiscoveredRooms...)
	statusMu.RUnlock()

	if pXStr := os.Getenv("ROBOT_DOCK_PIXEL_X"); pXStr != "" {
		if px, err := strconv.Atoi(pXStr); err == nil {
			pixChargerX = px
		}
	}
	if pYStr := os.Getenv("ROBOT_DOCK_PIXEL_Y"); pYStr != "" {
		if py, err := strconv.Atoi(pYStr); err == nil {
			pixChargerY = py
		}
	}

	isDocked := chargingState == "SlotCharging" || chargingState == "Charging"
	if isDocked {
		pixRobotX = pixChargerX
		pixRobotY = pixChargerY
		poseValid = true
	}

	restrictionsLock.Lock()
	restrs := make([]VirtualRestriction, len(DiscoveredRestrictions))
	copy(restrs, DiscoveredRestrictions)
	restrictionsLock.Unlock()

	roomNamesLock.Lock()
	rNames := make(map[string]string)
	for k, v := range DiscoveredRoomNames {
		rNames[k] = v
	}
	roomNamesLock.Unlock()

	pathLock.Lock()
	pts := make([]PathPoint, len(DiscoveredPath))
	copy(pts, DiscoveredPath)
	pathLock.Unlock()

	var rPolys map[string][][2]int
	if len(grid) > 0 {
		spotAreasLock.Lock()
		if len(spotAreaPolygons) > 0 {
			rPolys = make(map[string][][2]int, len(spotAreaPolygons))
			for rId, poly := range spotAreaPolygons {
				cropped := make([][2]int, len(poly))
				for i, pt := range poly {
					cropped[i] = [2]int{pt[0] - minX, pt[1] - minY}
				}
				rPolys[strconv.Itoa(rId)] = cropped
			}
		}
		spotAreasLock.Unlock()
	}

	resp := MapGridResponse{
		Width:               w,
		Height:              h,
		CropMinX:            minX,
		CropMinY:            minY,
		CropMaxX:            maxX,
		CropMaxY:            maxY,
		RobotPixelX:         pixRobotX,
		RobotPixelY:         pixRobotY,
		PoseValid:           poseValid,
		ChargerPixelX:       pixChargerX,
		ChargerPixelY:       pixChargerY,
		RobotAngle:          robotAngle,
		Rooms:               roomsCopy,
		RoomNames:           rNames,
		RoomPolygons:        rPolys,
		VirtualRestrictions: restrs,
		PathPoints:          pts,
		Grid:                grid,
	}

	res.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(res).Encode(resp)
}

func handleRenameRooms(res http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		http.Error(res, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Msid  string            `json:"msid"`
		Rooms map[string]string `json:"rooms"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(res, err.Error(), http.StatusBadRequest)
		return
	}
	msid := body.Msid
	if msid == "" {
		msid = ActiveMsid
	}
	if msid == "" {
		msid = "1"
	}

	publishXML(RenameM(msid, body.Rooms))

	roomNamesLock.Lock()
	for k, v := range body.Rooms {
		DiscoveredRoomNames[k] = v
	}
	roomNamesLock.Unlock()

	res.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(res).Encode(map[string]string{"status": "ok"})
}

func handleVirtualRestrictions(res http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" {
		restrictionsLock.Lock()
		restrs := make([]VirtualRestriction, len(DiscoveredRestrictions))
		copy(restrs, DiscoveredRestrictions)
		restrictionsLock.Unlock()
		res.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(res).Encode(map[string]interface{}{
			"virtual_restrictions": restrs,
		})
		return
	}

	if req.Method == "POST" {
		var payload struct {
			VirtualWalls [][4]int `json:"virtual_walls"`
			NoGoAreas    [][8]int `json:"no_go_areas"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			http.Error(res, err.Error(), http.StatusBadRequest)
			return
		}

		// Update in-memory cache
		restrictionsLock.Lock()
		DiscoveredRestrictions = []VirtualRestriction{}
		idCounter := 0
		for _, w := range payload.VirtualWalls {
			DiscoveredRestrictions = append(DiscoveredRestrictions, VirtualRestriction{
				ID:     idCounter,
				Type:   "virtual_wall",
				Points: []int{w[0], w[1], w[2], w[3]},
			})
			idCounter++
		}
		for _, z := range payload.NoGoAreas {
			DiscoveredRestrictions = append(DiscoveredRestrictions, VirtualRestriction{
				ID:     idCounter,
				Type:   "no_go_area",
				Points: []int{z[0], z[1], z[2], z[3], z[4], z[5], z[6], z[7]},
			})
			idCounter++
		}
		msid := ActiveMsid
		restrictionsLock.Unlock()

		// Publish XML SetM to robot
		publishXML(SetVirtualRestrictions(msid, payload.VirtualWalls, payload.NoGoAreas))
		log.Printf("[MAP] Sent SetVirtualRestrictions (msid=%s): %d walls, %d zones", msid, len(payload.VirtualWalls), len(payload.NoGoAreas))

		res.Header().Set("Content-Type", "application/json")
		_, _ = res.Write([]byte(`{"status":"success"}`))
		return
	}
}

type StatusResponse = ecovacs.StatusResponse

func getStatus(res http.ResponseWriter, req *http.Request) {
	// Proactively request latest updates from the vacuum
	publishXML(GetBatteryInfo)
	publishXML(GetChargeState)
	publishXML(GetCleanState)
	publishXML(GetOnOff("b"))
	publishXML(GetOnOff("l"))
	publishXML(GetOnOff("s"))
	publishXML(GetOnOff("g"))
	publishXML(GetCleanSum)
	publishXML(GetPos)
	publishXML(GetChargerPos)
	publishXML(GetVolume)
	publishXML(GetWaterPermeability)
	publishXML(GetBrushLifeSpan)
	publishXML(GetSideBrushLifeSpan)
	publishXML(GetDustCaseHeapLifeSpan)

	statusMu.RLock()
	batteryLevel, chargingState := BatteryLevel, ChargingState
	volumeLevel, waterLevel, waterBoxInstalled := VolumeLevel, WaterLevel, WaterBoxInstalled
	carpetBoost, continuousClean, voiceReports, dndEnabled := CarpetBoost, ContinuousClean, VoiceReports, DNDEnabled
	totalCleanCount, totalCleanArea, totalCleanTime := TotalCleanCount, TotalCleanArea, TotalCleanTime
	cleanSpeed, sleepStatus := CleanSpeed, SleepStatus
	brushLifeSpan, sideBrushLifeSpan, dustCaseHeapLifeSpan := BrushLifeSpan, SideBrushLifeSpan, DustCaseHeapLifeSpan
	discoveredRooms := append([]string(nil), DiscoveredRooms...)
	errs, robotAngle := Errors, RobotAngle
	poseValid := RobotPoseValid
	robotPosX, robotPosY, chargerPosX, chargerPosY := RobotPosX, RobotPosY, ChargerPosX, ChargerPosY
	cropMinX, cropMinY := MapCropMinX, MapCropMinY
	statusMu.RUnlock()

	pixelRobotX := 0
	pixelRobotY := 0
	pixelChargerX := 0
	pixelChargerY := 0

	if cropMinX > 0 || cropMinY > 0 {
		if robotPosX >= 0 && robotPosY >= 0 {
			pixelRobotX = robotPosX - cropMinX
			pixelRobotY = robotPosY - cropMinY
		}
		if chargerPosX >= 0 && chargerPosY >= 0 {
			pixelChargerX = chargerPosX - cropMinX
			pixelChargerY = chargerPosY - cropMinY
		}
	} else {
		pixelRobotX = robotPosX
		pixelRobotY = robotPosY
		pixelChargerX = chargerPosX
		pixelChargerY = chargerPosY
	}

	if pXStr := os.Getenv("ROBOT_DOCK_PIXEL_X"); pXStr != "" {
		if px, err := strconv.Atoi(pXStr); err == nil {
			pixelChargerX = px
			if cropMinX > 0 || cropMinY > 0 {
				chargerPosX = cropMinX + px
			}
		}
	}
	if pYStr := os.Getenv("ROBOT_DOCK_PIXEL_Y"); pYStr != "" {
		if py, err := strconv.Atoi(pYStr); err == nil {
			pixelChargerY = py
			if cropMinX > 0 || cropMinY > 0 {
				chargerPosY = cropMinY + py
			}
		}
	}

	posX := robotPosX
	posY := robotPosY
	if chargingState == "SlotCharging" || chargingState == "Charging" {
		pixelRobotX = pixelChargerX
		pixelRobotY = pixelChargerY
		posX = chargerPosX
		posY = chargerPosY
		poseValid = true
	}

	response := StatusResponse{
		BatteryLevel:         batteryLevel,
		ChargingState:        chargingState,
		VolumeLevel:          volumeLevel,
		WaterLevel:           waterLevel,
		WaterBoxInstalled:    waterBoxInstalled,
		CarpetBoost:          carpetBoost,
		ContinuousClean:      continuousClean,
		VoiceReports:         voiceReports,
		DNDEnabled:           dndEnabled,
		TotalCleanCount:      totalCleanCount,
		TotalCleanArea:       totalCleanArea,
		TotalCleanTime:       totalCleanTime,
		CleanSpeed:           cleanSpeed,
		SleepStatus:          sleepStatus,
		BrushLifeSpan:        brushLifeSpan,
		SideBrushLifeSpan:    sideBrushLifeSpan,
		DustCaseHeapLifeSpan: dustCaseHeapLifeSpan,
		DiscoveredRooms:      discoveredRooms,
		Errors:               errs,
		RobotPosX:            posX,
		RobotPosY:            posY,
		RobotAngle:           robotAngle,
		ChargerPosX:          chargerPosX,
		ChargerPosY:          chargerPosY,
		RobotPixelX:          pixelRobotX,
		RobotPixelY:          pixelRobotY,
		PoseValid:            poseValid,
		ChargerPixelX:        pixelChargerX,
		ChargerPixelY:        pixelChargerY,
		CropMinX:             cropMinX,
		CropMinY:             cropMinY,
	}

	res.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(res).Encode(response)
}

func handleClean(res http.ResponseWriter, req *http.Request) {
	act := req.URL.Query().Get("act")
	cleanType := req.URL.Query().Get("type")

	if act == "pause" {
		publishXML(CleanPause)
	} else if act == "stop" {
		publishXML(CleanStop)
	} else if cleanType == "spot_area" || cleanType == "SpotArea" {
		p := req.URL.Query().Get("p")
		sa := req.URL.Query().Get("sa")
		if sa == "" {
			sa = req.URL.Query().Get("mid")
		}
		if p != "" && sa == "" {
			publishXML(CleanCustomArea(p, 1))
		} else {
			publishXML(CleanSpotArea(sa))
		}
	} else if cleanType == "custom" || cleanType == "spot" || cleanType == "CustomArea" {
		p := req.URL.Query().Get("p")
		publishXML(CleanCustomArea(p, 1))
	} else {
		publishXML(Clean(act, cleanType))
	}

	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(`{"status":"success"}`))
}

func handleSetFanSpeed(res http.ResponseWriter, req *http.Request) {
	speed := req.URL.Query().Get("speed")
	if speed == "" {
		speed = "standard"
	}
	CleanSpeed = speed
	publishXML(SetCleanSpeed(speed))
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(`{"status":"success"}`))
}

func handleSetCarpetBoost(res http.ResponseWriter, req *http.Request) {
	enableStr := req.URL.Query().Get("enable")
	enable := enableStr == "true" || enableStr == "1" || enableStr == "ON"
	CarpetBoost = enable
	publishXML(SetOnOff("b", enable))
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(fmt.Sprintf(`{"status":"success","carpet_boost":%v}`, enable)))
}

func handleSetContinuousClean(res http.ResponseWriter, req *http.Request) {
	enableStr := req.URL.Query().Get("enable")
	enable := enableStr == "true" || enableStr == "1" || enableStr == "ON"
	ContinuousClean = enable
	publishXML(SetOnOff("l", enable))
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(fmt.Sprintf(`{"status":"success","continuous_clean":%v}`, enable)))
}

func handleSetVoiceReports(res http.ResponseWriter, req *http.Request) {
	enableStr := req.URL.Query().Get("enable")
	enable := enableStr == "true" || enableStr == "1" || enableStr == "ON"
	VoiceReports = enable
	// In Ecovacs DR930 firmware: switch 's' is Silent Mode (Hardware Mute).
	publishXML(SetOnOff("s", !enable))
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(fmt.Sprintf(`{"status":"success","voice_reports":%v}`, enable)))
}

func handleDND(res http.ResponseWriter, req *http.Request) {
	onStr := req.URL.Query().Get("on")
	if onStr == "" {
		onStr = req.URL.Query().Get("enable")
	}
	on := onStr == "1" || onStr == "true" || onStr == "ON"
	DNDEnabled = on
	publishXML(SetOnOff("g", on))
	publishXML(SetBlockTimeState(on))
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(fmt.Sprintf(`{"status":"success","dnd_enabled":%v}`, on)))
}

func handleResetLifeSpan(res http.ResponseWriter, req *http.Request) {
	comp := req.URL.Query().Get("component")
	if comp == "" {
		comp = "Brush"
	}
	publishXML(ResetLifeSpan(comp))
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(`{"status":"success"}`))
}

func handleCharge(res http.ResponseWriter, req *http.Request) {
	publishXML(Charge(true))
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(`{"status":"success"}`))
}

func handleSetVolume(res http.ResponseWriter, req *http.Request) {
	volStr := req.URL.Query().Get("level")
	vol, err := strconv.Atoi(volStr)
	if err != nil {
		res.WriteHeader(http.StatusBadRequest)
		_, _ = res.Write([]byte(`{"status":"error","message":"invalid level"}`))
		return
	}
	publishXML(SetVolume(vol))
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(`{"status":"success"}`))
}

func handleSetWater(res http.ResponseWriter, req *http.Request) {
	levelStr := req.URL.Query().Get("level")
	level, err := strconv.Atoi(levelStr)
	if err != nil {
		res.WriteHeader(http.StatusBadRequest)
		_, _ = res.Write([]byte(`{"status":"error","message":"invalid level"}`))
		return
	}
	publishXML(SetWaterPermeability(level))
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(`{"status":"success"}`))
}

func handleLocate(res http.ResponseWriter, req *http.Request) {
	publishXML(PlaySound(ecovacs.SOUND_I_AM_HERE))
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(`{"status":"success","message":"playing I am here sound"}`))
}

func handleListSounds(res http.ResponseWriter, req *http.Request) {
	res.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(res).Encode(ecovacs.AvailableSounds)
}

func handlePlaySound(res http.ResponseWriter, req *http.Request) {
	sid := req.URL.Query().Get("sid")
	if sid == "" {
		sid = req.URL.Query().Get("id")
	}
	if sid == "" {
		http.Error(res, `{"error":"missing sid parameter. Use /sounds to view available IDs."}`, http.StatusBadRequest)
		return
	}
	desc := "custom sound ID"
	if sidNum, err := strconv.Atoi(sid); err == nil {
		if d, ok := ecovacs.AvailableSounds[ecovacs.Sound(sidNum)]; ok {
			desc = d
		}
	}
	publishXML(PlaySoundSID(sid))
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(res).Encode(map[string]string{
		"status":      "success",
		"sid":         sid,
		"description": desc,
	})
}

func handleMove(res http.ResponseWriter, req *http.Request) {
	act := req.URL.Query().Get("act")
	if act == "" {
		act = "forward"
	}
	publishXML(Move(act))
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(`{"status":"success"}`))
}

func handleRecoverMap(res http.ResponseWriter, req *http.Request) {
	publishXML(RecoverMap)
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(`{"status":"success","action":"RecoverMap"}`))
}

func runRobotTelnetCmd(robotIP, shellCmd string) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(robotIP, "23"), 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	buf := make([]byte, 512)
	var acc strings.Builder
	sentLogin, sentPass, sentCmd := false, false, false
	fullCmd := shellCmd + " && printf '__CMD_%s__\\n' OK; exit\r\n"

	for {
		_ = conn.SetReadDeadline(time.Now().Add(600 * time.Millisecond))
		n, readErr := conn.Read(buf)
		if n > 0 {
			for i := 0; i < n; i++ {
				if buf[i] == 0xff {
					i += 2
					continue
				}
				if buf[i] != 0 {
					acc.WriteByte(buf[i])
				}
			}
			text := acc.String()
			if strings.Contains(text, "__CMD_OK__") {
				return nil
			}
			lower := strings.ToLower(text)
			if !sentLogin && (strings.Contains(lower, "login:") || strings.Contains(lower, "username:")) {
				sentLogin = true
				_, _ = conn.Write([]byte("root\r\n"))
				continue
			}
			if !sentPass && strings.Contains(lower, "password:") {
				sentPass = true
				_, _ = conn.Write([]byte("ecovacs\r\n"))
				continue
			}
			if !sentCmd && (strings.Contains(text, "# ") || strings.HasSuffix(strings.TrimSpace(text), "#")) {
				sentCmd = true
				_, _ = conn.Write([]byte(fullCmd))
			}
		}
		if readErr != nil {
			if ne, ok := readErr.(net.Error); ok && ne.Timeout() && !sentCmd {
				sentCmd = true
				if _, wErr := conn.Write([]byte(fullCmd)); wErr != nil {
					return wErr
				}
				continue
			}
			if strings.Contains(acc.String(), "__CMD_OK__") {
				return nil
			}
			return readErr
		}
	}
}

func resolveRobotIP() string {
	robotIP := robotIPFlag
	if robotIP == "" {
		robotIP = os.Getenv("ROBOT_IP")
	}
	if robotIP == "" {
		if bot := getActiveXmppBot(); bot != nil {
			robotIP = bot.RemoteIP()
		}
	}
	return robotIP
}

func isLocalRobotMode(robotIP string) bool {
	return localRobot || isLoopbackHost(robotIP)
}

func setSilentGotoLocal(enable bool) error {
	if enable {
		return os.WriteFile(silentGotoFilePath, []byte{}, 0644)
	}
	if err := os.Remove(silentGotoFilePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func handleSilentGoto(res http.ResponseWriter, req *http.Request) {
	enableStr := req.URL.Query().Get("enable")
	enable := enableStr == "true" || enableStr == "1" || enableStr == "ON"

	robotIP := resolveRobotIP()
	if isLocalRobotMode(robotIP) {
		if err := setSilentGotoLocal(enable); err != nil {
			log.Printf("[SILENT-GOTO] Local file error: %v", err)
			http.Error(res, fmt.Sprintf(`{"status":"error","message":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if enable {
			log.Printf("[SILENT-GOTO] Created %s locally on robot", silentGotoFilePath)
		} else {
			log.Printf("[SILENT-GOTO] Removed %s locally on robot", silentGotoFilePath)
		}
		res.Header().Set("Content-Type", "application/json")
		res.WriteHeader(http.StatusOK)
		_, _ = res.Write([]byte(fmt.Sprintf(`{"status":"success","silent_goto":%v}`, enable)))
		return
	}

	if robotIP == "" {
		log.Printf("[SILENT-GOTO] Error: ROBOT_IP is required (set ROBOT_IP env var or -robot-ip flag)")
		http.Error(res, `{"status":"error","message":"ROBOT_IP is required (set ROBOT_IP env var or -robot-ip flag)"}`, http.StatusBadRequest)
		return
	}

	cmd := "rm -f /tmp/silent_goto"
	if enable {
		cmd = "touch /tmp/silent_goto"
	}
	if err := runRobotTelnetCmd(robotIP, cmd); err != nil {
		log.Printf("[SILENT-GOTO] Telnet error to %s: %v", robotIP, err)
		http.Error(res, fmt.Sprintf(`{"status":"error","message":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	if enable {
		log.Printf("[SILENT-GOTO] Created /tmp/silent_goto on robot (%s)", robotIP)
	} else {
		log.Printf("[SILENT-GOTO] Removed /tmp/silent_goto on robot (%s)", robotIP)
	}

	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	_, _ = res.Write([]byte(fmt.Sprintf(`{"status":"success","silent_goto":%v}`, enable)))
}

func handleGoTo(res http.ResponseWriter, req *http.Request) {
	act := req.URL.Query().Get("act")
	if act == "" {
		if req.URL.Query().Get("stop") == "1" || req.URL.Query().Get("stop") == "true" {
			act = "stop"
		} else {
			act = "start"
		}
	}

	robotIP := resolveRobotIP()
	localMode := isLocalRobotMode(robotIP)
	if robotIP == "" && localMode {
		robotIP = "127.0.0.1"
	}
	if robotIP == "" {
		log.Printf("[GOTO] Error: ROBOT_IP is required (set ROBOT_IP env var or -robot-ip flag)")
		http.Error(res, `{"status":"error","message":"ROBOT_IP is required (set ROBOT_IP env var or -robot-ip flag)"}`, http.StatusBadRequest)
		return
	}

	res.Header().Set("Content-Type", "application/json")

	if act == "stop" {
		log.Printf("[GOTO] Stopping native Go-To on robot (%s)", robotIP)
		stopped := false
		// Try TCP 2026 first (direct to libsilentgoto native hook)
		if conn, err := net.DialTimeout("tcp", net.JoinHostPort(robotIP, "2026"), 1*time.Second); err == nil {
			_ = conn.SetDeadline(time.Now().Add(1 * time.Second))
			_, _ = conn.Write([]byte("STOP\n"))
			conn.Close()
			stopped = true
		}
		if localMode {
			if err := os.WriteFile(gotoStopFilePath, []byte{}, 0644); err == nil {
				stopped = true
			}
			_ = setSilentGotoLocal(false)
		} else if conn, err := net.DialTimeout("tcp", net.JoinHostPort(robotIP, "23"), 2*time.Second); err == nil {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			time.Sleep(50 * time.Millisecond)
			_, _ = conn.Write([]byte("touch /tmp/goto_stop\nrm -f /tmp/silent_goto\n"))
			stopped = true
		}
		if stopped {
			res.WriteHeader(http.StatusOK)
			_, _ = res.Write([]byte(`{"status":"success","action":"stopped"}`))
		} else {
			http.Error(res, `{"status":"error","message":"failed to connect to robot"}`, http.StatusInternalServerError)
		}
		return
	}

	xStr := req.URL.Query().Get("x")
	yStr := req.URL.Query().Get("y")
	if xStr == "" || yStr == "" {
		http.Error(res, `{"status":"error","message":"x and y coordinates are required"}`, http.StatusBadRequest)
		return
	}
	x, errX := strconv.Atoi(xStr)
	y, errY := strconv.Atoi(yStr)
	if errX != nil || errY != nil {
		http.Error(res, `{"status":"error","message":"invalid x or y coordinate format"}`, http.StatusBadRequest)
		return
	}

	log.Printf("[GOTO] Initiating native Go-To navigation to (%d, %d) on robot (%s)", x, y, robotIP)

	started := false
	// Try TCP 2026 first (direct to libsilentgoto native hook)
	if conn, err := net.DialTimeout("tcp", net.JoinHostPort(robotIP, "2026"), 1*time.Second); err == nil {
		_ = conn.SetDeadline(time.Now().Add(1 * time.Second))
		cmd := fmt.Sprintf("GOTO %d %d\n", x, y)
		if _, err := conn.Write([]byte(cmd)); err == nil {
			var respBuf [128]byte
			n, _ := conn.Read(respBuf[:])
			if n > 0 {
				log.Printf("[GOTO] Robot TCP 2026 response: %s", strings.TrimSpace(string(respBuf[:n])))
			}
			started = true
		}
		conn.Close()
	}

	if !started && localMode {
		payload := fmt.Sprintf("%d %d\n", x, y)
		if err := os.WriteFile(gotoTargetFilePath, []byte(payload), 0644); err != nil {
			http.Error(res, fmt.Sprintf(`{"status":"error","message":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		log.Printf("[GOTO] Wrote target locally to %s", gotoTargetFilePath)
		started = true
	}

	// Fallback via Telnet (writes /tmp/goto_target trigger)
	if !started {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(robotIP, "23"), 2*time.Second)
		if err != nil {
			log.Printf("[GOTO] Telnet dial error to %s: %v", robotIP, err)
			http.Error(res, fmt.Sprintf(`{"status":"error","message":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		time.Sleep(50 * time.Millisecond)
		cmd := fmt.Sprintf("echo \"%d %d\" > /tmp/goto_target\n", x, y)
		_, _ = conn.Write([]byte(cmd))
		log.Printf("[GOTO] Wrote target to /tmp/goto_target via Telnet on %s", robotIP)
		started = true
	}

	res.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(res).Encode(map[string]interface{}{
		"status": "success",
		"action": "start",
		"x":      x,
		"y":      y,
	})
}
