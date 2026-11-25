package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/itchio/lzma"
)

var statusMu sync.RWMutex
var BatteryLevel int = 0
var ChargingState string = "Unknown"
var VolumeLevel int = 0
var WaterLevel int = 0
var WaterBoxInstalled bool = false
var CarpetBoost bool = true
var ContinuousClean bool = true
var VoiceReports bool = true
var DNDEnabled bool = false
var TotalCleanCount int = 0
var TotalCleanArea int = 0
var TotalCleanTime int = 0
var CleanSpeed string = "Unknown"
var SleepStatus string = "Unknown"
var Errors string = ""
var BrushLifeSpan int = 100
var SideBrushLifeSpan int = 100
var DustCaseHeapLifeSpan int = 100
var DiscoveredRooms []string = []string{}
var (
	roomNamesLock       sync.Mutex
	DiscoveredRoomNames = make(map[string]string)
)

type PathPoint struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Type      int  `json:"type"`      // 0 = clean, 1 = mop
	Connected bool `json:"connected"` // true = connected to previous point
}

type pendingTraceReq struct {
	id          string
	trid        int
	tf          int
	tt          int
	requestedAt time.Time
}

var (
	pathLock         sync.Mutex
	DiscoveredPath   = []PathPoint{}
	ActiveTrID       = 0
	tracePointsByIdx = make(map[int]PathPoint)
	pendingGetTr     = make(map[string]pendingTraceReq)
	traceReqSeq      int
	sendTraceCmd     = publishXML
)

var RobotPosX int = 0
var RobotPosY int = 0
var RobotPoseValid bool = false
var RobotAngle int = 0
var ChargerPosX int = 0
var ChargerPosY int = 0
var MapCropMinX int = 0
var MapCropMinY int = 0
var MapCropMaxX int = 0
var MapCropMaxY int = 0

var mapMutex sync.RWMutex
var pullMpReqMap = make(map[string]int)
var pullMpLock sync.Mutex

func handleResponse(cmdName string, msgValues map[string]interface{}) error {
	fmt.Printf("%s %s\n", cmdName, msgValues)

	if msgValues == nil {
		return nil
	}

	// For JSON responses, the keys might be directly on the map, not wrapped in "ctl"
	var valuesMap map[string]interface{}
	if queryVal, ok := msgValues["query"]; ok {
		if qMap, ok := queryVal.(map[string]interface{}); ok {
			if ctlVal, ok := qMap["ctl"]; ok {
				if ctlMap, ok := ctlVal.(map[string]interface{}); ok {
					valuesMap = ctlMap
				}
			} else {
				valuesMap = qMap
			}
		}
	} else if ctlVal, ok := msgValues["ctl"]; ok {
		if ctlMap, ok := ctlVal.(map[string]interface{}); ok {
			valuesMap = ctlMap
		}
	} else {
		valuesMap = msgValues
	}

	if valuesMap != nil {
		switch cmdName {
		case "GetMapM", "MapM":
			var err error
			mapInfo, err = getMapDataValues(valuesMap)
			if err != nil {
				log.Printf("[MAP] getMapDataValues error: %v", err)
				return err
			}
			log.Printf("[MAP] MapInfo loaded: mapId=%d, grid=%dx%d, pieces=%dx%d",
				mapInfo.mapId, mapInfo.columnGrid, mapInfo.rowGrid, mapInfo.columnPiece, mapInfo.rowPiece)
		case "IfHaveMap":
			log.Printf("[MAP] IfHaveMap response: %+v", valuesMap)
		case "GetMapSt", "MapSt":
			log.Printf("[MAP] MapSt response: %+v", valuesMap)
		case "MapP":
			log.Printf("[MAP] MapP received piece: %+v", valuesMap["-pid"])
			// MXJ parses integer XML attributes as float64 when mapping interface{}
			var mapId int
			if pidFloat, ok := valuesMap["-pid"].(float64); ok {
				mapId = int(pidFloat)
			} else if pidInt, ok := valuesMap["-pid"].(int); ok {
				mapId = pidInt
			} else if pidStr, ok := valuesMap["-pid"].(string); ok {
				mapId, _ = strconv.Atoi(pidStr)
			}

			decodedMapPiece, err := decodeMapPiece(valuesMap)
			if err != nil {
				return err
			}
			insertMapPieceIntoMapGrid(mapId, decodedMapPiece)
		case "PullMP", "mp":
			pIndex := -1
			if pidVal, ok := valuesMap["-pid"]; ok {
				if pidStr, ok := pidVal.(string); ok {
					pIndex, _ = strconv.Atoi(pidStr)
				} else if pidFloat, ok := pidVal.(float64); ok {
					pIndex = int(pidFloat)
				}
			}
			if pIndex < 0 {
				if idVal, ok := valuesMap["-id"]; ok {
					if idStr, ok := idVal.(string); ok {
						if strings.HasPrefix(idStr, "mp") {
							pIndex, _ = strconv.Atoi(idStr[2:])
						} else {
							pullMpLock.Lock()
							if mapped, found := pullMpReqMap[idStr]; found {
								pIndex = mapped
							}
							pullMpLock.Unlock()
							if pIndex < 0 {
								if num, err := strconv.Atoi(idStr); err == nil && num >= 0 && num < 64 {
									pIndex = num
								}
							}
						}
					}
				}
			}
			if pIndex < 0 {
				pIndex = pieceIndex
				pieceIndex = (pieceIndex + 1) % 64
			}

			// Save mapId if present (do not use as pIndex!)
			if iStr, ok := valuesMap["-i"].(string); ok {
				if mId, err := strconv.Atoi(iStr); err == nil && mId > 0 {
					mapInfo.mapId = mId
				}
			}

			log.Printf("[MAP] PullMP received piece index %d", pIndex)
			decodedMapPiece, err := decodeMapPiece(valuesMap)
			if err != nil {
				log.Printf("[MAP] Failed decoding map piece %d: %v", pIndex, err)
				return err
			}
			insertMapPieceIntoMapGrid(pIndex, decodedMapPiece)
		case "GetMapSet", "MapSet":
			log.Printf("[MAP] MapSet response: %+v", valuesMap)
			getSpotAreas(valuesMap)
		case "PullM":
			log.Printf("[MAP] PullM response: %+v", valuesMap)
			handlePullMResponse(valuesMap)
		case "GetTrM", "TrM":
			log.Printf("[PATH] TrM response: %+v", valuesMap)
			handleTrM(valuesMap, cmdName == "TrM")
		case "GetTr", "trace":
			log.Printf("[PATH] GetTr response received")
			handleGetTr(valuesMap)
		case "CleanReport":
			if clean, ok := valuesMap["clean"].(map[string]interface{}); ok {
				statusMu.Lock()
				if speedStr, ok := clean["-speed"].(string); ok {
					CleanSpeed = speedStr
				}
				if stStr, ok := clean["-st"].(string); ok {
					switch stStr {
					case "h":
						if ChargingState != "SlotCharging" && ChargingState != "Charging" && ChargingState != "WireCharging" && ChargingState != "Going" && ChargingState != "Returning" {
							ChargingState = "Idle"
						}
					case "c", "s":
						ChargingState = "Cleaning"
					case "p":
						ChargingState = "Paused"
					case "r":
						ChargingState = "Returning"
					}
				}
				statusMu.Unlock()
			}
		case "GetBatteryInfo", "BatteryInfo":
			statusMu.Lock()
			if powerStr, ok := valuesMap["-power"].(string); ok {
				BatteryLevel, _ = strconv.Atoi(powerStr)
			} else if powerFloat, ok := valuesMap["-power"].(float64); ok {
				BatteryLevel = int(powerFloat)
			} else if battery, ok := valuesMap["battery"].(map[string]interface{}); ok {
				if powerStr, ok := battery["-power"].(string); ok {
					BatteryLevel, _ = strconv.Atoi(powerStr)
				} else if powerFloat, ok := battery["-power"].(float64); ok {
					BatteryLevel = int(powerFloat)
				}
			} else if val, ok := valuesMap["value"].(float64); ok { // JSON representation
				BatteryLevel = int(val)
			}
			lvl := BatteryLevel
			statusMu.Unlock()
			log.Printf("[BATTERY] Received battery level: %d%%", lvl)
		case "GetChargeState", "ChargeState":
			statusMu.Lock()
			if charge, ok := valuesMap["charge"].(map[string]interface{}); ok {
				if typeStr, ok := charge["-type"].(string); ok {
					/* Firmware returns <charge type="Idle"/> whenever off dock, including during active clean */
					if typeStr != "Idle" || (ChargingState != "Cleaning" && ChargingState != "Paused" && ChargingState != "Returning") {
						ChargingState = typeStr
					}
				}
			} else if isCharging, ok := valuesMap["isCharging"].(float64); ok { // JSON representation
				if isCharging == 1 {
					ChargingState = "Charging"
				} else if ChargingState != "Cleaning" && ChargingState != "Paused" && ChargingState != "Returning" {
					ChargingState = "Discharging"
				}
			}
			statusMu.Unlock()
		case "GetPos", "Pos":
			// In Pos packets, if t="c", it indicates the charging dock position
			if tType, ok := valuesMap["-t"].(string); ok && tType == "c" {
				pStr := ""
				if p, ok := valuesMap["-p"].(string); ok {
					pStr = p
				} else if pos, ok := valuesMap["pos"].(map[string]interface{}); ok {
					if ps, ok := pos["-pos"].(string); ok {
						pStr = ps
					}
				}
				if pStr != "" {
					pStr = strings.Trim(pStr, "[]")
					parts := strings.Split(pStr, ",")
					if len(parts) >= 2 {
						rawX, _ := strconv.ParseFloat(parts[0], 64)
						rawY, _ := strconv.ParseFloat(parts[1], 64)
						center := 400
						if len(mapInfo.mapGrid) > 0 {
							center = len(mapInfo.mapGrid) / 2
						}
						ChargerPosX = center + int(rawX/50.0)
						ChargerPosY = center + int(rawY/50.0)
						log.Printf("[POS] Pos packet had t=c, assigned to ChargerPos=(%d,%d)", ChargerPosX, ChargerPosY)
					}
				}
				break
			}

			// For robot pose, check if SLAM localization is valid
			isValid := true
			if v, ok := valuesMap["-valid"].(string); ok && v == "0" {
				isValid = false
			} else if v, ok := valuesMap["-valid"].(float64); ok && v == 0 {
				isValid = false
			}
			RobotPoseValid = isValid
			if !isValid {
				log.Printf("[POS] Notice: Robot reports unlocalized pose (valid=0), accepting reported coordinates (%v) as best-effort", valuesMap["-p"])
			}

			pStr := ""
			if p, ok := valuesMap["-p"].(string); ok {
				pStr = p
			} else if pos, ok := valuesMap["pos"].(map[string]interface{}); ok {
				if ps, ok := pos["-pos"].(string); ok {
					pStr = ps
				}
			}
			if pStr != "" {
				pStr = strings.Trim(pStr, "[]")
				parts := strings.Split(pStr, ",")
				if len(parts) >= 2 {
					rawX, _ := strconv.ParseFloat(parts[0], 64)
					rawY, _ := strconv.ParseFloat(parts[1], 64)
					center := 400
					if len(mapInfo.mapGrid) > 0 {
						center = len(mapInfo.mapGrid) / 2
					}
					RobotPosX = center + int(rawX/50.0)
					RobotPosY = center + int(rawY/50.0)
					log.Printf("[POS] Parsed RobotPos: raw=(%v,%v) -> grid=(%d,%d)", rawX, rawY, RobotPosX, RobotPosY)
				}
			}
			if aStr, ok := valuesMap["-a"].(string); ok {
				RobotAngle, _ = strconv.Atoi(aStr)
			} else if aFloat, ok := valuesMap["-a"].(float64); ok {
				RobotAngle = int(aFloat)
			} else if pos, ok := valuesMap["pos"].(map[string]interface{}); ok {
				if aStr, ok := pos["-angle"].(string); ok {
					RobotAngle, _ = strconv.Atoi(aStr)
				}
			}
		case "GetChargerPos", "ChargerPos":
			pStr := ""
			if p, ok := valuesMap["-p"].(string); ok {
				pStr = p
			} else if pos, ok := valuesMap["pos"].(map[string]interface{}); ok {
				if ps, ok := pos["-pos"].(string); ok {
					pStr = ps
				}
			}
			if pStr != "" {
				pStr = strings.Trim(pStr, "[]")
				parts := strings.Split(pStr, ",")
				if len(parts) >= 2 {
					rawX, _ := strconv.ParseFloat(parts[0], 64)
					rawY, _ := strconv.ParseFloat(parts[1], 64)
					center := 400
					if len(mapInfo.mapGrid) > 0 {
						center = len(mapInfo.mapGrid) / 2
					}
					ChargerPosX = center + int(rawX/50.0)
					ChargerPosY = center + int(rawY/50.0)
					if overrideX := os.Getenv("ROBOT_DOCK_X"); overrideX != "" {
						if xVal, err := strconv.Atoi(overrideX); err == nil {
							ChargerPosX = xVal
						}
					}
					if overrideY := os.Getenv("ROBOT_DOCK_Y"); overrideY != "" {
						if yVal, err := strconv.Atoi(overrideY); err == nil {
							ChargerPosY = yVal
						}
					}
					log.Printf("[POS] Parsed ChargerPos: raw=(%v,%v) -> grid=(%d,%d)", rawX, rawY, ChargerPosX, ChargerPosY)
				}
			}
		case "GetVolume":
			if volume, ok := valuesMap["volume"].(map[string]interface{}); ok {
				if valStr, ok := volume["-val"].(string); ok {
					VolumeLevel, _ = strconv.Atoi(valStr)
				}
			} else if vol, ok := valuesMap["volume"].(float64); ok { // JSON representation
				VolumeLevel = int(vol)
			}
		case "GetWaterPermeability", "getWaterInfo":
			if vStr, ok := valuesMap["-v"].(string); ok {
				WaterLevel, _ = strconv.Atoi(vStr)
			} else if amount, ok := valuesMap["amount"].(float64); ok { // JSON representation
				WaterLevel = int(amount)
			}
		case "GetWaterBoxInfo":
			if onStr, ok := valuesMap["-on"].(string); ok {
				WaterBoxInstalled = (onStr == "1")
			}
		case "GetOnOff", "OnOff", "SetOnOff":
			onVal := ""
			if on, ok := valuesMap["-on"].(string); ok {
				onVal = on
			} else if on, ok := valuesMap["on"].(string); ok {
				onVal = on
			}
			idStr, _ := valuesMap["-id"].(string)
			tVal, _ := valuesMap["-t"].(string)
			if tVal == "" && strings.HasPrefix(idStr, "onoff_") {
				tVal = strings.TrimPrefix(idStr, "onoff_")
			}
			isTrue := (onVal == "1")
			switch tVal {
			case "b":
				statusMu.Lock()
				CarpetBoost = isTrue
				statusMu.Unlock()
				log.Printf("[ONOFF] CarpetBoost = %v", isTrue)
			case "l":
				statusMu.Lock()
				ContinuousClean = isTrue
				statusMu.Unlock()
				log.Printf("[ONOFF] ContinuousClean = %v", isTrue)
			case "s":
				// Switch 's' in firmware is Silent Mode (1 = Mute ON, 0 = Mute OFF / VoiceReports active)
				statusMu.Lock()
				VoiceReports = !isTrue
				statusMu.Unlock()
				log.Printf("[ONOFF] Silent Mode = %v (VoiceReports = %v)", isTrue, !isTrue)
			case "g":
				statusMu.Lock()
				DNDEnabled = isTrue
				statusMu.Unlock()
				log.Printf("[ONOFF] DNDEnabled = %v", isTrue)
			default:
				log.Printf("[ONOFF] Ignoring untyped/unknown OnOff response (t=%q, on=%v)", tVal, isTrue)
			}
		case "GetCleanSum", "CleanSum":
			statusMu.Lock()
			if cStr, ok := valuesMap["-c"].(string); ok {
				TotalCleanCount, _ = strconv.Atoi(cStr)
			}
			if aStr, ok := valuesMap["-a"].(string); ok {
				TotalCleanArea, _ = strconv.Atoi(aStr)
			}
			if lStr, ok := valuesMap["-l"].(string); ok {
				TotalCleanTime, _ = strconv.Atoi(lStr)
			}
			cnt, area, tm := TotalCleanCount, TotalCleanArea, TotalCleanTime
			statusMu.Unlock()
			log.Printf("[CLEANSUM] Received clean sum: count=%d, area=%d m², time=%d s", cnt, area, tm)
		case "GetSleepStatus":
			if stStr, ok := valuesMap["-st"].(string); ok {
				statusMu.Lock()
				SleepStatus = stStr
				statusMu.Unlock()
			}
		case "GetCleanSpeed":
			if speedStr, ok := valuesMap["-speed"].(string); ok {
				statusMu.Lock()
				CleanSpeed = speedStr
				statusMu.Unlock()
			}
		case "GetError":
			if errsStr, ok := valuesMap["-errs"].(string); ok {
				statusMu.Lock()
				Errors = errsStr
				statusMu.Unlock()
			}
		case "GetLifeSpan", "LifeSpan":
			var val int = 100
			if leftStr, ok := valuesMap["-left"].(string); ok {
				left, _ := strconv.Atoi(leftStr)
				total := 100
				if totStr, ok := valuesMap["-total"].(string); ok {
					total, _ = strconv.Atoi(totStr)
				}
				if left <= 0 {
					val = 0
				} else if total > 0 {
					val = int(float64(left) / float64(total) * 100.0)
				}
			} else if valStr, ok := valuesMap["-val"].(string); ok {
				val, _ = strconv.Atoi(valStr)
			} else if valFloat, ok := valuesMap["-val"].(float64); ok {
				val = int(valFloat)
			}
			compType, _ := valuesMap["-type"].(string)
			switch compType {
			case "Brush":
				BrushLifeSpan = val
			case "SideBrush":
				SideBrushLifeSpan = val
			case "DustCaseHeap":
				DustCaseHeapLifeSpan = val
			}
		}
	}
	return nil
}


var spotAreasLock sync.Mutex
var spotAreaPolygons = make(map[int][][2]int) // mid -> list of [2]int [gridX, gridY]

func pointInPolygon(x, y int, poly [][2]int) bool {
	n := len(poly)
	if n < 3 {
		return false
	}
	inside := false
	p1x, p1y := poly[0][0], poly[0][1]
	for i := 1; i <= n; i++ {
		p2x, p2y := poly[i%n][0], poly[i%n][1]
		if y > min(p1y, p2y) && y <= max(p1y, p2y) && x <= max(p1x, p2x) {
			if p1y != p2y {
				xinters := (y-p1y)*(p2x-p1x)/(p2y-p1y) + p1x
				if p1x == p2x || x <= xinters {
					inside = !inside
				}
			}
		}
		p1x, p1y = p2x, p2y
	}
	return inside
}

type VirtualRestriction struct {
	ID     int    `json:"id"`
	Type   string `json:"type"` // "virtual_wall" or "no_go_area"
	Points []int  `json:"points"` // raw coordinates in mm: [x1, y1, x2, y2] or [x1, y1, x2, y2, x3, y3, x4, y4]
}

var (
	restrictionsLock       sync.Mutex
	DiscoveredRestrictions []VirtualRestriction
	ActiveMsid             = "7"
)

func handlePullMResponse(valuesMap map[string]interface{}) {
	mStr, ok := valuesMap["-m"].(string)
	if !ok || mStr == "" {
		return
	}

	tpVal, _ := valuesMap["-tp"].(string)
	idVal, _ := valuesMap["-id"].(string)

	isVW := tpVal == "vw" || strings.HasPrefix(idVal, "vw") || strings.HasPrefix(mStr, "[")
	if isVW {
		cleanStr := strings.Trim(mStr, "[]")
		parts := strings.Split(cleanStr, ",")
		var coords []int
		for _, p := range parts {
			c, err := strconv.Atoi(strings.TrimSpace(p))
			if err == nil {
				coords = append(coords, c)
			}
		}

		mid := 0
		if strings.HasPrefix(idVal, "vw") {
			mid, _ = strconv.Atoi(idVal[2:])
		} else if midStr, ok := valuesMap["-mid"].(string); ok {
			mid, _ = strconv.Atoi(midStr)
		}

		if len(coords) == 4 {
			// Virtual Wall [x1, y1, x2, y2]
			restrictionsLock.Lock()
			updated := false
			for i, r := range DiscoveredRestrictions {
				if r.ID == mid && r.Type == "virtual_wall" {
					DiscoveredRestrictions[i].Points = coords
					updated = true
					break
				}
			}
			if !updated {
				DiscoveredRestrictions = append(DiscoveredRestrictions, VirtualRestriction{
					ID:     mid,
					Type:   "virtual_wall",
					Points: coords,
				})
			}
			restrictionsLock.Unlock()
			log.Printf("[MAP] Parsed virtual wall #%d: %+v", mid, coords)
		} else if len(coords) == 8 {
			// No-go area polygon [x1, y1, x2, y2, x3, y3, x4, y4]
			restrictionsLock.Lock()
			updated := false
			for i, r := range DiscoveredRestrictions {
				if r.ID == mid && r.Type == "no_go_area" {
					DiscoveredRestrictions[i].Points = coords
					updated = true
					break
				}
			}
			if !updated {
				DiscoveredRestrictions = append(DiscoveredRestrictions, VirtualRestriction{
					ID:     mid,
					Type:   "no_go_area",
					Points: coords,
				})
			}
			restrictionsLock.Unlock()
			log.Printf("[MAP] Parsed no-go area #%d: %+v", mid, coords)
		}
		return
	}

	mid := -1
	if idVal, ok := valuesMap["-id"].(string); ok && strings.HasPrefix(idVal, "sa") {
		mid, _ = strconv.Atoi(idVal[2:])
	} else if midVal, ok := valuesMap["-mid"].(string); ok {
		mid, _ = strconv.Atoi(midVal)
	}

	if mid < 0 {
		spotAreasLock.Lock()
		for _, rStr := range DiscoveredRooms {
			rId, _ := strconv.Atoi(rStr)
			if _, exists := spotAreaPolygons[rId]; !exists {
				mid = rId
				break
			}
		}
		spotAreasLock.Unlock()
	}

	if mid < 0 {
		return
	}

	var poly [][2]int
	pts := strings.Split(mStr, ";")
	for _, pt := range pts {
		pt = strings.TrimSpace(pt)
		if pt == "" {
			continue
		}
		coords := strings.Split(pt, ",")
		if len(coords) == 2 {
			rawX, err1 := strconv.Atoi(coords[0])
			rawY, err2 := strconv.Atoi(coords[1])
			if err1 == nil && err2 == nil {
				// Ecovacs 50mm grid centered at (400, 400)
				gx := 400 + (rawX / 50)
				gy := 400 + (rawY / 50)
				poly = append(poly, [2]int{gx, gy})
			}
		}
	}

	if len(poly) < 3 {
		return
	}

	spotAreasLock.Lock()
	spotAreaPolygons[mid] = poly
	spotAreasLock.Unlock()

	log.Printf("[MAP] Successfully parsed room %d (Room %c) with %d polygon points", mid, 'A'+mid, len(poly))

	// Rasterize room into mapGrid
	mapMutex.Lock()
	for x := 0; x < len(mapInfo.mapGrid); x++ {
		for y := 0; y < len(mapInfo.mapGrid[x]); y++ {
			val := mapInfo.mapGrid[x][y]
			if val == 1 || val == 3 {
				if pointInPolygon(x, y, poly) {
					mapInfo.mapGrid[x][y] = byte(4 + mid)
				}
			}
		}
	}
	mapMutex.Unlock()
}

func getSpotAreas(valuesMap map[string]interface{}) {
	msid, _ := valuesMap["-msid"].(string)
	if msid != "" {
		ActiveMsid = msid
	}
	tp, _ := valuesMap["-tp"].(string)

	if tp == "vw" {
		handleVirtualWallsSet(msid, valuesMap)
		return
	}

	addRoom := func(mid string, name string) {
		if mid == "" {
			return
		}
		if name != "" {
			roomNamesLock.Lock()
			DiscoveredRoomNames[mid] = name
			roomNamesLock.Unlock()
			log.Printf("[MAP] Spot area #%s has name '%s'", mid, name)
		}
		found := false
		for _, r := range DiscoveredRooms {
			if r == mid {
				found = true
				break
			}
		}
		if !found {
			DiscoveredRooms = append(DiscoveredRooms, mid)
		}
		mInt, _ := strconv.Atoi(mid)
		spotAreasLock.Lock()
		_, hasPoly := spotAreaPolygons[mInt]
		spotAreasLock.Unlock()
		if msid != "" && !hasPoly {
			publishXML(PullM(msid, mid))
		}
	}

	if mVal, ok := valuesMap["m"]; ok {
		switch mList := mVal.(type) {
		case []interface{}:
			for _, m := range mList {
				if mMap, ok := m.(map[string]interface{}); ok {
					mid, _ := mMap["-mid"].(string)
					n, _ := mMap["-n"].(string)
					addRoom(mid, n)
				}
			}
		case map[string]interface{}:
			mid, _ := mList["-mid"].(string)
			n, _ := mList["-n"].(string)
			addRoom(mid, n)
		}
	}
}

func handleVirtualWallsSet(msid string, valuesMap map[string]interface{}) {
	var vmMids []string
	if mVal, ok := valuesMap["m"]; ok {
		switch mList := mVal.(type) {
		case []interface{}:
			for _, m := range mList {
				if mMap, ok := m.(map[string]interface{}); ok {
					if mid, ok := mMap["-mid"].(string); ok && mid != "" {
						vmMids = append(vmMids, mid)
					}
				}
			}
		case map[string]interface{}:
			if mid, ok := mList["-mid"].(string); ok && mid != "" {
				vmMids = append(vmMids, mid)
			}
		}
	}

	if len(vmMids) == 0 {
		restrictionsLock.Lock()
		DiscoveredRestrictions = []VirtualRestriction{}
		restrictionsLock.Unlock()
		log.Println("[MAP] No virtual restrictions configured on robot.")
		return
	}

	for _, mid := range vmMids {
		publishXML(PullMType("vw", msid, mid))
	}
}

type MapInfo struct {
	columnGrid  int
	columnPiece int
	crc         []int64
	mapId       int
	pixelWidth  int
	rowGrid     int
	rowPiece    int
	mapGrid     [][]byte
}

//valuesMap is not a pointer because map values are passed by reference
func getMapDataValues(valuesMap map[string]interface{}) (MapInfo, error) {

	var mapInfo MapInfo

	if iStr, ok := valuesMap["-i"].(string); ok {
		mapInfo.mapId, _ = strconv.Atoi(iStr)
	}
	if wStr, ok := valuesMap["-w"].(string); ok {
		mapInfo.columnGrid, _ = strconv.Atoi(wStr)
	}
	if hStr, ok := valuesMap["-h"].(string); ok {
		mapInfo.rowGrid, _ = strconv.Atoi(hStr)
	}
	if cStr, ok := valuesMap["-c"].(string); ok {
		mapInfo.columnPiece, _ = strconv.Atoi(cStr)
	}
	if rStr, ok := valuesMap["-r"].(string); ok {
		mapInfo.rowPiece, _ = strconv.Atoi(rStr)
	}
	if pStr, ok := valuesMap["-p"].(string); ok {
		mapInfo.pixelWidth, _ = strconv.Atoi(pStr)
	}
	if mStr, ok := valuesMap["-m"].(string); ok {
		mapInfo.crc, _ = sliceInt64(strings.Split(mStr, ","))
	}

	rowBits := mapInfo.rowGrid * mapInfo.rowPiece
	columnBits := mapInfo.columnGrid * mapInfo.columnPiece

	mapMutex.Lock()
	mapInfo.mapGrid = make([][]byte, rowBits)
	for i := 0; i < rowBits; i++ {
		mapInfo.mapGrid[i] = make([]byte, columnBits)
	}
	mapMutex.Unlock()

	numMapPieces := mapInfo.columnPiece * mapInfo.rowPiece

	pullMpLock.Lock()
	for i := 0; i < numMapPieces; i++ {
		reqID := fmt.Sprintf("mp%02d", i)
		pullMpReqMap[reqID] = i
	}
	pullMpLock.Unlock()

	go func() {
		for i := 0; i < numMapPieces; i++ {
			publishXML(PullMp(i))
			time.Sleep(15 * time.Millisecond)
		}
	}()

	return mapInfo, nil
}

func decodeMapPiece(valuesMap map[string]interface{}) ([]byte, error) {
	if base64EncodedString, ok := valuesMap["-p"].(string); ok {
		lzmaCompressedMapPiece, err := base64.StdEncoding.DecodeString(base64EncodedString)
		if err != nil {
			return nil, fmt.Errorf("Base64 decoding failed: %w", err)
		}

		if len(lzmaCompressedMapPiece) < 9 {
			return nil, fmt.Errorf("Map piece LZMA payload too short (%d bytes)", len(lzmaCompressedMapPiece))
		}

		// Convert Ecovacs 9-byte header to standard 13-byte LZMA header:
		// Insert 4 bytes of 0s at index 8 for 64-bit uncompressed size
		zeroPadding := make([]byte, 4)
		tempSlice := lzmaCompressedMapPiece[8:]
		tempSlice = append(zeroPadding, tempSlice...)
		lzmaCompressedMapPiece = append(lzmaCompressedMapPiece[:8], tempSlice...)

		lmzaCompressedBuffer := bytes.NewBuffer(lzmaCompressedMapPiece)

		decodedMapPiece := make([]byte, 10000)
		lzmaReadCloser := lzma.NewReader(lmzaCompressedBuffer)

		n, err := ioReadFull(lzmaReadCloser, decodedMapPiece)
		lzmaReadCloser.Close()
		if err != nil && n == 0 {
			return nil, fmt.Errorf("LZMA decompress error: %w", err)
		}

		return decodedMapPiece[:n], nil
	} else {
		return nil, fmt.Errorf("Could not get base64 encoded string from message")
	}
}

func ioReadFull(r io.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func insertMapPieceIntoMapGrid(pieceId int, decodedMapPieceData []byte) {
	mapMutex.Lock()
	defer mapMutex.Unlock()

	colPiece := mapInfo.columnPiece
	if colPiece <= 0 {
		colPiece = 1
	}
	rowStart := pieceId / colPiece
	columnStart := pieceId % colPiece

	for row := 0; row < mapInfo.rowGrid; row++ {
		for column := 0; column < mapInfo.columnGrid; column++ {

			bufferRow := row + rowStart*mapInfo.rowGrid
			bufferColumn := column + columnStart*mapInfo.columnGrid
			pieceDataPosition := mapInfo.rowGrid*row + column

			if pieceDataPosition < len(decodedMapPieceData) &&
				bufferRow < len(mapInfo.mapGrid) &&
				bufferColumn < len(mapInfo.mapGrid[bufferRow]) {
				mapInfo.mapGrid[bufferRow][bufferColumn] = decodedMapPieceData[pieceDataPosition]
			}
		}
	}
}

func getImageFromMapGrid() image.Image {
	scaledImg := getScaledMapGrid()
	scaledImgX := len(scaledImg)
	if scaledImgX == 0 || len(scaledImg[0]) == 0 {
		return image.NewRGBA(image.Rectangle{image.Point{0, 0}, image.Point{1, 1}})
	}
	scaledImgY := len(scaledImg[0])
	imgRGBA := image.NewRGBA(image.Rectangle{image.Point{0, 0}, image.Point{scaledImgX, scaledImgY}})

	for x := 0; x < scaledImgX; x++ {
		for y := 0; y < scaledImgY; y++ {
			imgRGBA.Set(x, y, MapData(scaledImg[x][y]).MapDataColorMapping())
		}
	}

	return image.Image(imgRGBA)
}

func getScaledMapGrid() [][]byte {
	mapMutex.RLock()
	defer mapMutex.RUnlock()

	if len(mapInfo.mapGrid) == 0 {
		return [][]byte{}
	}

	minX := -1
	minY := -1
	maxX := -1
	maxY := -1
	for x := 0; x < len(mapInfo.mapGrid); x++ {
		for y := 0; y < len(mapInfo.mapGrid[x]); y++ {
			if mapInfo.mapGrid[x][y] != 0 {
				if minX == -1 || x < minX {
					minX = x
				}
				if minY == -1 || y < minY {
					minY = y
				}
				if maxX == -1 || x > maxX {
					maxX = x
				}
				if maxY == -1 || y > maxY {
					maxY = y
				}
			}
		}
	}

	if minX == -1 || minY == -1 || maxX < minX || maxY < minY {
		return [][]byte{}
	}

	MapCropMinX = minX
	MapCropMinY = minY
	MapCropMaxX = maxX
	MapCropMaxY = maxY

	w := maxX - minX + 1
	h := maxY - minY + 1
	scaledMapGrid := make([][]byte, w)
	for i := 0; i < w; i++ {
		scaledMapGrid[i] = make([]byte, h)
		for j := 0; j < h; j++ {
			scaledMapGrid[i][j] = mapInfo.mapGrid[i+minX][j+minY]
		}
	}

	spotAreasLock.Lock()
	if len(spotAreaPolygons) > 0 {
		for i := 0; i < w; i++ {
			for j := 0; j < h; j++ {
				val := scaledMapGrid[i][j]
				if val == 1 || val == 3 {
					wx := i + minX
					wy := j + minY
					for rId, poly := range spotAreaPolygons {
						if pointInPolygon(wx, wy, poly) {
							scaledMapGrid[i][j] = byte(4 + rId)
							mapInfo.mapGrid[wx][wy] = byte(4 + rId)
							break
						}
					}
				}
			}
		}
	}
	spotAreasLock.Unlock()

	return scaledMapGrid
}

func GetMapGridRaw() ([][]byte, int, int, int, int) {
	grid := getScaledMapGrid()
	return grid, MapCropMinX, MapCropMinY, MapCropMaxX, MapCropMaxY
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func sliceInt64(sa []string) ([]int64, error) {
	si := make([]int64, 0, len(sa))
	for _, a := range sa {
		i, err := strconv.ParseInt(a, 10, 64)
		if err != nil {
			return si, err
		}
		si = append(si, i)
	}
	return si, nil
}

type MapData int

const (
	NoData MapData = iota
	Floor
	Wall
	Carpet
)

func (mapData MapData) MapDataColorMapping() color.RGBA {
	switch mapData {
	case NoData:
		return color.RGBA{0, 0, 0, 255} //black
	case Floor:
		return color.RGBA{15, 10, 222, 255} //blue
	case Wall:
		return color.RGBA{255, 255, 255, 255} //white
	case Carpet:
		return color.RGBA{100, 200, 200, 255} //cyan
	}
	return color.RGBA{0, 0, 0, 255} //black
}

var pieceIndex = 0
var mapInfo = MapInfo{}

func resetTraceSessionLocked(trid int) {
	ActiveTrID = trid
	tracePointsByIdx = make(map[int]PathPoint)
	pendingGetTr = make(map[string]pendingTraceReq)
	DiscoveredPath = []PathPoint{}
}

func rebuildDiscoveredPathLocked() {
	if len(tracePointsByIdx) == 0 {
		DiscoveredPath = []PathPoint{}
		return
	}
	maxIdx := -1
	for idx := range tracePointsByIdx {
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	pts := make([]PathPoint, 0, len(tracePointsByIdx))
	for i := 0; i <= maxIdx; i++ {
		if pt, ok := tracePointsByIdx[i]; ok {
			pts = append(pts, pt)
		}
	}
	DiscoveredPath = pts
}

func parseIntAttr(m map[string]interface{}, key string) (int, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch val := v.(type) {
	case string:
		n, err := strconv.Atoi(val)
		return n, err == nil
	case float64:
		return int(val), true
	case int:
		return val, true
	}
	return 0, false
}

func isTraceIndexCoveredLocked(trid, idx int) bool {
	if _, ok := tracePointsByIdx[idx]; ok {
		return true
	}
	for _, req := range pendingGetTr {
		if req.trid == trid && idx >= req.tf && idx <= req.tt {
			return true
		}
	}
	return false
}

func handleTrM(valuesMap map[string]interface{}, isLivePush bool) {
	ctlMap := valuesMap
	if nested, ok := valuesMap["ctl"].(map[string]interface{}); ok {
		ctlMap = nested
	}
	trid, _ := parseIntAttr(ctlMap, "-trid")
	c, _ := parseIntAttr(ctlMap, "-c")
	log.Printf("[PATH] Received TrM metadata: trid=%d, count=%d", trid, c)
	if trid <= 0 || c <= 0 {
		return
	}

	const maxTraceChunk = 200
	now := time.Now()
	var toSend []pendingTraceReq

	pathLock.Lock()
	if trid != ActiveTrID {
		resetTraceSessionLocked(trid)
	}
	for id, req := range pendingGetTr {
		if req.trid != trid || now.Sub(req.requestedAt) > 15*time.Second {
			delete(pendingGetTr, id)
		}
	}

	skipTip := false
	if isLivePush && c > 1 {
		if _, hasTip := tracePointsByIdx[c-1]; !hasTip {
			allPrior := true
			for i := 0; i < c-1; i++ {
				if !isTraceIndexCoveredLocked(trid, i) {
					allPrior = false
					break
				}
			}
			skipTip = allPrior
		}
	}

	if !skipTip {
		for idx := 0; idx < c; {
			if isTraceIndexCoveredLocked(trid, idx) {
				idx++
				continue
			}
			tf := idx
			tt := idx
			for tt+1 < c && tt-tf+1 < maxTraceChunk && !isTraceIndexCoveredLocked(trid, tt+1) {
				tt++
			}
			traceReqSeq++
			reqID := strconv.Itoa(900000 + (traceReqSeq % 99999))
			req := pendingTraceReq{
				id:          reqID,
				trid:        trid,
				tf:          tf,
				tt:          tt,
				requestedAt: now,
			}
			pendingGetTr[reqID] = req
			toSend = append(toSend, req)
			idx = tt + 1
		}
	}
	pathLock.Unlock()

	if len(toSend) == 1 {
		sendTraceCmd(GetTrWithID(toSend[0].id, trid, toSend[0].tf, toSend[0].tt))
	} else if len(toSend) > 1 {
		go func(reqs []pendingTraceReq) {
			for i, r := range reqs {
				if i > 0 {
					time.Sleep(50 * time.Millisecond)
				}
				sendTraceCmd(GetTrWithID(r.id, r.trid, r.tf, r.tt))
			}
		}(toSend)
	}
}

func handleGetTr(valuesMap map[string]interface{}) {
	ctlMap := valuesMap
	if nested, ok := valuesMap["ctl"].(map[string]interface{}); ok {
		ctlMap = nested
	}
	trStr, _ := ctlMap["-tr"].(string)
	if trStr == "" {
		return
	}

	points, err := decodeTracePoints(trStr)
	if err != nil {
		log.Printf("[PATH] Error decoding trace points: %v", err)
		return
	}

	idStr, _ := ctlMap["-id"].(string)
	trid, _ := parseIntAttr(ctlMap, "-trid")
	tf, hasTF := parseIntAttr(ctlMap, "-tf")

	pathLock.Lock()
	fromPending := false
	if idStr != "" {
		if req, ok := pendingGetTr[idStr]; ok {
			delete(pendingGetTr, idStr)
			trid = req.trid
			tf = req.tf
			hasTF = true
			fromPending = true
		}
	}

	if trid > 0 && trid != ActiveTrID {
		if fromPending && ActiveTrID != 0 {
			pathLock.Unlock()
			return
		}
		resetTraceSessionLocked(trid)
	}

	if hasTF {
		for i, pt := range points {
			tracePointsByIdx[tf+i] = pt
		}
		rebuildDiscoveredPathLocked()
	} else {
		DiscoveredPath = append(DiscoveredPath, points...)
	}
	total := len(DiscoveredPath)
	pathLock.Unlock()
	log.Printf("[PATH] Parsed %d trace points (total=%d)", len(points), total)
}

func decodeTracePoints(b64Str string) ([]PathPoint, error) {
	rawLzma, err := base64.StdEncoding.DecodeString(b64Str)
	if err != nil {
		return nil, fmt.Errorf("Base64 decode failed: %w", err)
	}
	if len(rawLzma) < 9 {
		return nil, fmt.Errorf("Payload too short (%d bytes)", len(rawLzma))
	}

	// Ecovacs 9-byte header -> standard 13-byte header
	zeroPadding := make([]byte, 4)
	tempSlice := rawLzma[8:]
	tempSlice = append(zeroPadding, tempSlice...)
	rawLzma = append(rawLzma[:8], tempSlice...)

	buf := bytes.NewBuffer(rawLzma)
	reader := lzma.NewReader(buf)
	decoded := make([]byte, 20000)
	n, err := ioReadFull(reader, decoded)
	reader.Close()
	if err != nil && n == 0 {
		return nil, fmt.Errorf("LZMA decompress error: %w", err)
	}

	data := decoded[:n]
	var points []PathPoint
	for i := 0; i+5 <= len(data); i += 5 {
		chunk := data[i : i+5]
		yRaw := int(int16(binary.LittleEndian.Uint16(chunk[0:2])))
		xRaw := int(int16(binary.LittleEndian.Uint16(chunk[2:4])))
		b2 := chunk[4]

		xMm := yRaw * 10
		yMm := xRaw * 10
		connected := ((b2 >> 7) & 1) == 0
		pointType := int(b2 & 1)

		points = append(points, PathPoint{
			X:         xMm,
			Y:         yMm,
			Type:      pointType,
			Connected: connected,
		})
	}
	return points, nil
}
