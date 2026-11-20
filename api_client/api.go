package main

import (
	"bytes"
	"encoding/json"
	"image/jpeg"
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

func setupApi() {
	r := mux.NewRouter()
	r.HandleFunc("/getMapData", getMapData).Methods("GET")
	r.HandleFunc("/status", getStatus).Methods("GET")
	r.HandleFunc("/clean", handleClean).Methods("POST")
	r.HandleFunc("/charge", handleCharge).Methods("POST")
	r.HandleFunc("/setVolume", handleSetVolume).Methods("POST")
	r.HandleFunc("/setWater", handleSetWater).Methods("POST")
	r.HandleFunc("/locate", handleLocate).Methods("POST", "GET")
	r.HandleFunc("/move", handleMove).Methods("POST", "GET")
	r.HandleFunc("/setFanSpeed", handleSetFanSpeed).Methods("POST")
	r.HandleFunc("/dnd", handleDND).Methods("POST")
	r.HandleFunc("/resetLifeSpan", handleResetLifeSpan).Methods("POST")
	r.HandleFunc("/sounds", handleListSounds).Methods("GET")
	r.HandleFunc("/playSound", handlePlaySound).Methods("POST", "GET")
	http.Handle("/", r)
	go http.ListenAndServe(":8000", r)
}

func getMapData(res http.ResponseWriter, req *http.Request) {
	publishXML(GetMapM)
	publishXML(GetMapSet)
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

type StatusResponse struct {
	BatteryLevel         int      `json:"battery_level"`
	ChargingState        string   `json:"charging_state"`
	VolumeLevel          int      `json:"volume_level"`
	WaterLevel           int      `json:"water_level"`
	WaterBoxInstalled    bool     `json:"water_box_installed"`
	CleanSpeed           string   `json:"clean_speed"`
	SleepStatus          string   `json:"sleep_status"`
	BrushLifeSpan        int      `json:"brush_lifespan"`
	SideBrushLifeSpan    int      `json:"side_brush_lifespan"`
	DustCaseHeapLifeSpan int      `json:"filter_lifespan"`
	DiscoveredRooms      []string `json:"discovered_rooms"`
	Errors               string   `json:"errors"`
}

func getStatus(res http.ResponseWriter, req *http.Request) {
	// Proactively request latest updates from the vacuum
	publishXML(GetBatteryInfo)
	publishXML(GetChargeState)
	publishXML(GetVolume)
	publishXML(GetWaterPermeability)
	publishXML(GetBrushLifeSpan)
	publishXML(GetSideBrushLifeSpan)
	publishXML(GetDustCaseHeapLifeSpan)

	response := StatusResponse{
		BatteryLevel:         BatteryLevel,
		ChargingState:        ChargingState,
		VolumeLevel:          VolumeLevel,
		WaterLevel:           WaterLevel,
		WaterBoxInstalled:    WaterBoxInstalled,
		CleanSpeed:           CleanSpeed,
		SleepStatus:          SleepStatus,
		BrushLifeSpan:        BrushLifeSpan,
		SideBrushLifeSpan:    SideBrushLifeSpan,
		DustCaseHeapLifeSpan: DustCaseHeapLifeSpan,
		DiscoveredRooms:      DiscoveredRooms,
		Errors:               Errors,
	}

	res.Header().Set("Content-Type", "application/json")
	json.NewEncoder(res).Encode(response)
}

func handleClean(res http.ResponseWriter, req *http.Request) {
	act := req.URL.Query().Get("act")
	if act == "" {
		act = "clean" // default action is clean (start/resume)
	}
	cleanType := req.URL.Query().Get("type")
	if cleanType == "" {
		cleanType = "auto"
	}

	if act == "pause" {
		publishXML(CleanPause)
	} else if act == "stop" {
		publishXML(CleanStop)
	} else if cleanType == "spot_area" {
		sa := req.URL.Query().Get("sa")
		msid := req.URL.Query().Get("msid")
		mid := req.URL.Query().Get("mid")
		if msid == "" {
			msid = "7"
		}
		if mid == "" {
			mid = "0"
		}
		publishXML(CleanSpotArea(msid, mid, sa))
	} else if cleanType == "custom" {
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
	publishXML(SetCleanSpeed(speed))
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(`{"status":"success"}`))
}

func handleDND(res http.ResponseWriter, req *http.Request) {
	on := req.URL.Query().Get("on") == "1"
	publishXML(SetBlockTimeState(on))
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(`{"status":"success"}`))
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


