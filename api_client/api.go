package main

import (
	"bytes"
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
)

func setupApi() {
	r := mux.NewRouter()
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
	http.Handle("/", r)
	go http.ListenAndServe(":8000", r)
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

type MapGridResponse struct {
	Width               int                  `json:"width"`
	Height              int                  `json:"height"`
	CropMinX            int                  `json:"crop_min_x"`
	CropMinY            int                  `json:"crop_min_y"`
	CropMaxX            int                  `json:"crop_max_x"`
	CropMaxY            int                  `json:"crop_max_y"`
	RobotPixelX         int                  `json:"robot_pixel_x"`
	RobotPixelY         int                  `json:"robot_pixel_y"`
	PoseValid           bool                 `json:"pose_valid"`
	ChargerPixelX       int                  `json:"charger_pixel_x"`
	ChargerPixelY       int                  `json:"charger_pixel_y"`
	RobotAngle          int                  `json:"robot_angle"`
	Rooms               []string             `json:"rooms"`
	RoomNames           map[string]string    `json:"room_names"`
	VirtualRestrictions []VirtualRestriction `json:"virtual_restrictions"`
	PathPoints          []PathPoint          `json:"path_points"`
	Grid                [][]byte             `json:"grid"`
}

func handleMapGrid(res http.ResponseWriter, req *http.Request) {
	publishXML(GetMapVirtualWalls)
	grid, minX, minY, maxX, maxY := GetMapGridRaw()
	w, h := 0, 0
	if len(grid) > 0 {
		w = len(grid)
		h = len(grid[0])
	}

	pixRobotX := RobotPosX - minX
	pixRobotY := RobotPosY - minY
	pixChargerX := ChargerPosX - minX
	pixChargerY := ChargerPosY - minY

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

	isDocked := ChargingState == "SlotCharging" || ChargingState == "Charging"
	poseValid := RobotPoseValid
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
		RobotAngle:          RobotAngle,
		Rooms:               DiscoveredRooms,
		RoomNames:           rNames,
		VirtualRestrictions: restrs,
		PathPoints:          pts,
		Grid:                grid,
	}

	res.Header().Set("Content-Type", "application/json")
	json.NewEncoder(res).Encode(resp)
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
	json.NewEncoder(res).Encode(map[string]string{"status": "ok"})
}

func handleVirtualRestrictions(res http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" {
		restrictionsLock.Lock()
		restrs := make([]VirtualRestriction, len(DiscoveredRestrictions))
		copy(restrs, DiscoveredRestrictions)
		restrictionsLock.Unlock()
		res.Header().Set("Content-Type", "application/json")
		json.NewEncoder(res).Encode(map[string]interface{}{
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
		res.Write([]byte(`{"status":"success"}`))
		return
	}
}

type StatusResponse struct {
	BatteryLevel         int      `json:"battery_level"`
	ChargingState        string   `json:"charging_state"`
	VolumeLevel          int      `json:"volume_level"`
	WaterLevel           int      `json:"water_level"`
	WaterBoxInstalled    bool     `json:"water_box_installed"`
	CarpetBoost          bool     `json:"carpet_boost"`
	ContinuousClean      bool     `json:"continuous_clean"`
	VoiceReports         bool     `json:"voice_reports"`
	DNDEnabled           bool     `json:"dnd_enabled"`
	TotalCleanCount      int      `json:"total_clean_count"`
	TotalCleanArea       int      `json:"total_clean_area"`
	TotalCleanTime       int      `json:"total_clean_time"`
	CleanSpeed           string   `json:"clean_speed"`
	SleepStatus          string   `json:"sleep_status"`
	BrushLifeSpan        int      `json:"brush_lifespan"`
	SideBrushLifeSpan    int      `json:"side_brush_lifespan"`
	DustCaseHeapLifeSpan int      `json:"filter_lifespan"`
	DiscoveredRooms      []string `json:"discovered_rooms"`
	Errors               string   `json:"errors"`
	RobotPosX            int      `json:"robot_x"`
	RobotPosY            int      `json:"robot_y"`
	RobotAngle           int      `json:"robot_angle"`
	ChargerPosX          int      `json:"charger_x"`
	ChargerPosY          int      `json:"charger_y"`
	RobotPixelX          int      `json:"robot_pixel_x"`
	RobotPixelY          int      `json:"robot_pixel_y"`
	PoseValid            bool     `json:"pose_valid"`
	ChargerPixelX        int      `json:"charger_pixel_x"`
	ChargerPixelY        int      `json:"charger_pixel_y"`
	CropMinX             int      `json:"crop_min_x"`
	CropMinY             int      `json:"crop_min_y"`
}

func getStatus(res http.ResponseWriter, req *http.Request) {
	// Proactively request latest updates from the vacuum
	publishXML(GetBatteryInfo)
	publishXML(GetChargeState)
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

	pixelRobotX := 0
	pixelRobotY := 0
	pixelChargerX := 0
	pixelChargerY := 0

	// In our coordinate space:
	// RobotPosY is the row, RobotPosX is the column in mapInfo.mapGrid.
	// MapCropMinX is minRow, MapCropMinY is minCol.
	// On imgRGBA (width = numRows, height = numCols):
	// pixelX = row - minRow = RobotPosY - MapCropMinX
	// pixelY = col - minCol = RobotPosX - MapCropMinY
	if MapCropMinX > 0 && MapCropMinY > 0 {
		if RobotPosX > 0 && RobotPosY > 0 {
			pixelRobotX = RobotPosX - MapCropMinX
			pixelRobotY = RobotPosY - MapCropMinY
		}
		if ChargerPosX > 0 && ChargerPosY > 0 {
			pixelChargerX = ChargerPosX - MapCropMinX
			pixelChargerY = ChargerPosY - MapCropMinY
		}
	} else {
		pixelRobotX = RobotPosX
		pixelRobotY = RobotPosY
		pixelChargerX = ChargerPosX
		pixelChargerY = ChargerPosY
	}

	if pXStr := os.Getenv("ROBOT_DOCK_PIXEL_X"); pXStr != "" {
		if px, err := strconv.Atoi(pXStr); err == nil {
			pixelChargerX = px
		}
	}
	if pYStr := os.Getenv("ROBOT_DOCK_PIXEL_Y"); pYStr != "" {
		if py, err := strconv.Atoi(pYStr); err == nil {
			pixelChargerY = py
		}
	}

	poseValid := RobotPoseValid
	posX := RobotPosX
	posY := RobotPosY
	if ChargingState == "SlotCharging" || ChargingState == "Charging" {
		pixelRobotX = pixelChargerX
		pixelRobotY = pixelChargerY
		posX = ChargerPosX
		posY = ChargerPosY
		poseValid = true
	}

	response := StatusResponse{
		BatteryLevel:         BatteryLevel,
		ChargingState:        ChargingState,
		VolumeLevel:          VolumeLevel,
		WaterLevel:           WaterLevel,
		WaterBoxInstalled:    WaterBoxInstalled,
		CarpetBoost:          CarpetBoost,
		ContinuousClean:      ContinuousClean,
		VoiceReports:         VoiceReports,
		DNDEnabled:           DNDEnabled,
		TotalCleanCount:      TotalCleanCount,
		TotalCleanArea:       TotalCleanArea,
		TotalCleanTime:       TotalCleanTime,
		CleanSpeed:           CleanSpeed,
		SleepStatus:          SleepStatus,
		BrushLifeSpan:        BrushLifeSpan,
		SideBrushLifeSpan:    SideBrushLifeSpan,
		DustCaseHeapLifeSpan: DustCaseHeapLifeSpan,
		DiscoveredRooms:      DiscoveredRooms,
		Errors:               Errors,
		RobotPosX:            posX,
		RobotPosY:            posY,
		RobotAngle:           RobotAngle,
		ChargerPosX:          ChargerPosX,
		ChargerPosY:          ChargerPosY,
		RobotPixelX:          pixelRobotX,
		RobotPixelY:          pixelRobotY,
		PoseValid:            poseValid,
		ChargerPixelX:        pixelChargerX,
		ChargerPixelY:        pixelChargerY,
		CropMinX:             MapCropMinX,
		CropMinY:             MapCropMinY,
	}

	res.Header().Set("Content-Type", "application/json")
	json.NewEncoder(res).Encode(response)
}

func handleClean(res http.ResponseWriter, req *http.Request) {
	act := req.URL.Query().Get("act")
	cleanType := req.URL.Query().Get("type")

	if act == "pause" {
		publishXML(CleanPause)
	} else if act == "stop" {
		publishXML(CleanStop)
	} else if cleanType == "spot_area" || cleanType == "SpotArea" {
		sa := req.URL.Query().Get("sa")
		if sa == "" {
			sa = req.URL.Query().Get("mid")
		}
		publishXML(CleanSpotArea(sa))
	} else if cleanType == "custom" || cleanType == "spot" || cleanType == "CustomArea" {
		p := req.URL.Query().Get("p")
		publishXML(CleanCustomArea(p, 1))
	} else {
		publishXML(Clean(act, cleanType))
	}

	res.WriteHeader(http.StatusOK)
	res.Write([]byte(`{"status":"success"}`))
}

func handleSetFanSpeed(res http.ResponseWriter, req *http.Request) {
	speed := req.URL.Query().Get("speed")
	if speed == "" {
		speed = "standard"
	}
	CleanSpeed = speed
	publishXML(SetCleanSpeed(speed))
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(`{"status":"success"}`))
}

func handleSetCarpetBoost(res http.ResponseWriter, req *http.Request) {
	enableStr := req.URL.Query().Get("enable")
	enable := enableStr == "true" || enableStr == "1" || enableStr == "ON"
	CarpetBoost = enable
	publishXML(SetOnOff("b", enable))
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(fmt.Sprintf(`{"status":"success","carpet_boost":%v}`, enable)))
}

func handleSetContinuousClean(res http.ResponseWriter, req *http.Request) {
	enableStr := req.URL.Query().Get("enable")
	enable := enableStr == "true" || enableStr == "1" || enableStr == "ON"
	ContinuousClean = enable
	publishXML(SetOnOff("l", enable))
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(fmt.Sprintf(`{"status":"success","continuous_clean":%v}`, enable)))
}

func handleSetVoiceReports(res http.ResponseWriter, req *http.Request) {
	enableStr := req.URL.Query().Get("enable")
	enable := enableStr == "true" || enableStr == "1" || enableStr == "ON"
	VoiceReports = enable
	// In Ecovacs DR930 firmware: switch 's' is Silent Mode (Hardware Mute).
	// When Silent Mode is ON (s=1), the firmware executes `amixer sset 'Speaker Mute' on`.
	// Therefore, VoiceReports = true means Silent Mode = false (s=0).
	// VoiceReports = false means Silent Mode = true (s=1).
	publishXML(SetOnOff("s", !enable))
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(fmt.Sprintf(`{"status":"success","voice_reports":%v}`, enable)))
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
	res.Write([]byte(fmt.Sprintf(`{"status":"success","dnd_enabled":%v}`, on)))
}

func handleResetLifeSpan(res http.ResponseWriter, req *http.Request) {
	comp := req.URL.Query().Get("component")
	if comp == "" {
		comp = "Brush"
	}
	publishXML(ResetLifeSpan(comp))
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(`{"status":"success"}`))
}

func handleCharge(res http.ResponseWriter, req *http.Request) {
	publishXML(Charge(true))
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(`{"status":"success"}`))
}

func handleSetVolume(res http.ResponseWriter, req *http.Request) {
	volStr := req.URL.Query().Get("level")
	vol, err := strconv.Atoi(volStr)
	if err != nil {
		res.WriteHeader(http.StatusBadRequest)
		res.Write([]byte(`{"status":"error","message":"invalid level"}`))
		return
	}
	publishXML(SetVolume(vol))
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(`{"status":"success"}`))
}

func handleSetWater(res http.ResponseWriter, req *http.Request) {
	levelStr := req.URL.Query().Get("level")
	level, err := strconv.Atoi(levelStr)
	if err != nil {
		res.WriteHeader(http.StatusBadRequest)
		res.Write([]byte(`{"status":"error","message":"invalid level"}`))
		return
	}
	publishXML(SetWaterPermeability(level))
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(`{"status":"success"}`))
}

func handleLocate(res http.ResponseWriter, req *http.Request) {
	publishXML(PlaySound(SOUND_I_AM_HERE))
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(`{"status":"success","message":"playing I am here sound"}`))
}

func handleListSounds(res http.ResponseWriter, req *http.Request) {
	res.Header().Set("Content-Type", "application/json")
	json.NewEncoder(res).Encode(AvailableSounds)
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
	desc, ok := AvailableSounds[sid]
	if !ok {
		desc = "custom sound ID"
	}
	publishXML(PlaySound(Sound(sid)))
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	json.NewEncoder(res).Encode(map[string]string{
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
	res.Write([]byte(`{"status":"success"}`))
}

func handleSilentGoto(res http.ResponseWriter, req *http.Request) {
	enableStr := req.URL.Query().Get("enable")
	enable := enableStr == "true" || enableStr == "1" || enableStr == "ON"

	robotIP := robotIPFlag
	if robotIP == "" {
		robotIP = os.Getenv("ROBOT_IP")
	}
	if robotIP == "" && activeXmppBot != nil {
		robotIP = activeXmppBot.RemoteIP()
	}
	if robotIP == "" {
		log.Printf("[SILENT-GOTO] Error: ROBOT_IP is required (set ROBOT_IP env var or -robot-ip flag)")
		http.Error(res, `{"status":"error","message":"ROBOT_IP is required (set ROBOT_IP env var or -robot-ip flag)"}`, http.StatusBadRequest)
		return
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(robotIP, "23"), 2*time.Second)
	if err != nil {
		log.Printf("[SILENT-GOTO] Telnet dial error to %s: %v", robotIP, err)
		http.Error(res, fmt.Sprintf(`{"status":"error","message":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	time.Sleep(100 * time.Millisecond)
	if enable {
		_, _ = conn.Write([]byte("touch /tmp/silent_goto\n"))
		log.Printf("[SILENT-GOTO] Created /tmp/silent_goto on robot (%s)", robotIP)
	} else {
		_, _ = conn.Write([]byte("rm -f /tmp/silent_goto\n"))
		log.Printf("[SILENT-GOTO] Removed /tmp/silent_goto on robot (%s)", robotIP)
	}
	time.Sleep(100 * time.Millisecond)

	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(fmt.Sprintf(`{"status":"success","silent_goto":%v}`, enable)))
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

	robotIP := robotIPFlag
	if robotIP == "" {
		robotIP = os.Getenv("ROBOT_IP")
	}
	if robotIP == "" && activeXmppBot != nil {
		robotIP = activeXmppBot.RemoteIP()
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
		// Also touch /tmp/goto_stop via Telnet as safety fallback
		if conn, err := net.DialTimeout("tcp", net.JoinHostPort(robotIP, "23"), 2*time.Second); err == nil {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			time.Sleep(50 * time.Millisecond)
			_, _ = conn.Write([]byte("touch /tmp/goto_stop\nrm -f /tmp/silent_goto\n"))
			stopped = true
		}
		if stopped {
			res.WriteHeader(http.StatusOK)
			res.Write([]byte(`{"status":"success","action":"stopped"}`))
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
	json.NewEncoder(res).Encode(map[string]interface{}{
		"status": "success",
		"action": "start",
		"x":      x,
		"y":      y,
	})
}



