package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	mxj "github.com/clbanning/mxj/v2"
)

type XMLMsg struct {
	cmdName string
	cmdOpts mxj.Map
}

// ==========================================
// XML Commands (EcoRobotProtocol XML)
// ==========================================

// GetVersion requests the firmware version
var GetVersion XMLMsg = XMLMsg{
	cmdName: "GetVersion",
	cmdOpts: mxj.Map{"-name": "FW"},
}

// GetBatteryInfo requests the battery percentage
var GetBatteryInfo XMLMsg = XMLMsg{
	cmdName: "GetBatteryInfo",
	cmdOpts: mxj.Map{},
}

// GetChargeState requests the current charging status
var GetChargeState XMLMsg = XMLMsg{
	cmdName: "GetChargeState",
	cmdOpts: mxj.Map{},
}

// GetVolume requests the current volume setting
var GetVolume XMLMsg = XMLMsg{
	cmdName: "GetVolume",
	cmdOpts: mxj.Map{
		"volume": mxj.Map{
			"-unit": "percentage",
		},
	},
}

// SetVolume sets the volume of the robot voice
func SetVolume(volume int) XMLMsg {
	return XMLMsg{
		cmdName: "SetVolume",
		cmdOpts: mxj.Map{
			"volume": mxj.Map{
				"-unit": "percentage",
				"-val":  strconv.Itoa(volume),
			},
		},
	}
}

// GetWaterPermeability requests the mopping water flow level
var GetWaterPermeability XMLMsg = XMLMsg{
	cmdName: "GetWaterPermeability",
	cmdOpts: mxj.Map{},
}

// SetWaterPermeability sets the water flow level (e.g. 1, 2, 3)
func SetWaterPermeability(level int) XMLMsg {
	return XMLMsg{
		cmdName: "SetWaterPermeability",
		cmdOpts: mxj.Map{
			"-v": strconv.Itoa(level),
		},
	}
}

// Clean starts, pauses, or stops cleaning
// act: "start" ("s"), "pause" ("p"), "resume" ("r"), "stop" ("h")
// cleanType: "auto", "border", "spot", "SpotArea"
func Clean(act string, cleanType string) XMLMsg {
	actCode := "s"
	switch act {
	case "stop", "halt", "h":
		actCode = "h"
	case "pause", "p":
		actCode = "p"
	case "resume", "r":
		actCode = "r"
	default:
		actCode = "s"
	}

	cleanMap := mxj.Map{
		"-act": actCode,
	}
	if actCode == "s" {
		if cleanType == "" {
			cleanType = "auto"
		}
		cleanMap["-type"] = cleanType
		speed := CleanSpeed
		if speed == "" || speed == "Unknown" {
			speed = "standard"
		}
		cleanMap["-speed"] = speed
	}

	return XMLMsg{
		cmdName: "Clean",
		cmdOpts: mxj.Map{
			"clean": cleanMap,
		},
	}
}

// Charge commands the robot to go to the dock
// goBack: true to go to dock, false to stop going to dock
func Charge(goBack bool) XMLMsg {
	typeStr := "stopGo"
	if goBack {
		typeStr = "go"
	}
	return XMLMsg{
		cmdName: "Charge",
		cmdOpts: mxj.Map{
			"charge": mxj.Map{
				"-type": typeStr,
			},
		},
	}
}

// GetLifeSpan requests the wear status of parts (e.g. "Brush", "SideBrush", "Filter")
func GetLifeSpan(component string) XMLMsg {
	return XMLMsg{
		cmdName: "GetLifeSpan",
		cmdOpts: mxj.Map{
			"-type": component,
		},
	}
}

// GetBrushLifeSpan is a helper alias for GetLifeSpan("Brush") to preserve backwards compatibility
var GetBrushLifeSpan XMLMsg = GetBrushLifeSpanHelper()

func GetBrushLifeSpanHelper() XMLMsg {
	return GetLifeSpan("Brush")
}

// GoToPoint commands the robot to navigate to (x, y) coordinates using native taskGoto
// Handled natively on the robot via libsilentgoto.so /goTo endpoint
type GoToCmd struct {
	X int
	Y int
}


// PlaySound makes the robot play a specific audio clip
type Sound string

const (
	BOOT_UP                 Sound = "0"  // Boot up chime
	SOUND_SUSPENDED         Sound = "3"  // i am suspended
	SOUND_CHECK_WHEELS      Sound = "4"  // Please check the driving wheels
	SOUND_HELP_ME           Sound = "5"  // Please help me out
	SOUND_INSTALL_DUST_BIN  Sound = "6"  // Please install dust bin
	SOUND_DING              Sound = "17" // ding
	SOUND_BATTERY_LOW       Sound = "18" // my battery is low
	SOUND_POWER_ON_CHARGING Sound = "29" // please power me on before charging
	SOUND_I_AM_HERE         Sound = "30" // i am here
	SOUND_BRUSH_TANGLED     Sound = "31" // brush is tangled please clean my brush
	SOUND_CLEAN_SENSORS     Sound = "35" // please clean my anti drop sensors
	SOUND_BRUSH_TANGLED_2   Sound = "48" // brush is tangled please clean my brush
	SOUND_RELOCATING        Sound = "55" // I am relocating
	SOUND_UPGRADE_SUCCESS   Sound = "56" // upgrade succeeded
	SOUND_RETURNING_DOCK    Sound = "63" // i am returning to the charging dock
	SOUND_CLEANING_PAUSED   Sound = "65" // cleaning paused
	SOUND_WIFI_CONNECTED    Sound = "69" // connected please go back to the ecovacs
	SOUND_RESTORING_MAP     Sound = "71" // i am restoring the map please don't stand beside me
	SOUND_BATTERY_LOW_DOCK  Sound = "73" // my battery is low returning to the charging dock
	SOUND_RELOCATE_FAIL     Sound = "74" // difficult to locate i am starting a new cleaning cycle
	SOUND_RESUMING_CLEAN    Sound = "75" // i am resuming the clean
	SOUND_UPGRADE_FAILED    Sound = "76" // upgrade failed please try again
	SOUND_PLACE_ON_DOCK     Sound = "77" // please place me on the charging dock
	SOUND_RESUME_CLEAN      Sound = "79" // resume the clean
	SOUND_STARTING_CLEAN    Sound = "80" // i am starting the clean
	SOUND_STARTING_CLEAN_2  Sound = "81" // i am starting the clean
	SOUND_STARTING_CLEAN_3  Sound = "82" // i am starting the clean
	SOUND_READY_FOR_MOPPING Sound = "84" // i am ready for mopping
	SOUND_REMOVE_MOP_PLATE  Sound = "85" // please remove the mopping plate i am building the map
	SOUND_CLEAN_COMPLETE    Sound = "86" // cleaning is complete returning to the charging dock
	SOUND_LDS_MALFUNCTION   Sound = "89" // LDS malfunction please try to tap the LDS
	SOUND_UPGRADING         Sound = "90" // I am upgrading please wait
)

// StockSounds maps every factory firmware sound ID to its official audio prompt description
var StockSounds = map[string]string{
	"0":  "Boot up chime",
	"3":  "i am suspended",
	"4":  "Please check the driving wheels",
	"5":  "Please help me out",
	"6":  "Please install dust bin",
	"17": "ding",
	"18": "my battery is low",
	"29": "please power me on before charging",
	"30": "i am here",
	"31": "brush is tangled please clean my brush",
	"35": "please clean my anti drop sensors",
	"48": "brush is tangled please clean my brush",
	"55": "I am relocating",
	"56": "upgrade succeeded",
	"63": "i am returning to the charging dock",
	"65": "cleaning paused",
	"69": "connected please go back to the ecovacs",
	"71": "i am restoring the map please don't stand beside me",
	"73": "my battery is low returning to the charging dock",
	"74": "difficult to locate i am starting a new cleaning cycle",
	"75": "i am resuming the clean",
	"76": "upgrade failed please try again",
	"77": "please place me on the charging dock",
	"79": "resume the clean",
	"80": "i am starting the clean",
	"81": "i am starting the clean",
	"82": "i am starting the clean",
	"84": "i am ready for mopping",
	"85": "please remove the mopping plate i am building the map",
	"86": "cleaning is complete returning to the charging dock",
	"89": "LDS malfunction please try to tap the LDS",
	"90": "I am upgrading please wait",
}

// AvailableSounds maps all currently available sounds (Stock + dynamically discovered/configured custom sounds)
var AvailableSounds = make(map[string]string)

func init() {
	InitSounds()
}

// InitSounds populates AvailableSounds with StockSounds plus any dynamically discovered custom sounds
func InitSounds() {
	AvailableSounds = make(map[string]string)
	for k, v := range StockSounds {
		AvailableSounds[k] = v
	}

	// 1. Load from CUSTOM_SOUNDS environment variable (JSON or "id:name,id:name")
	if env := os.Getenv("CUSTOM_SOUNDS"); env != "" {
		var jsonMap map[string]string
		if err := json.Unmarshal([]byte(env), &jsonMap); err == nil {
			for id, name := range jsonMap {
				AvailableSounds[id] = name
			}
		} else {
			// Try comma-separated format: "98:Do not come,99:I'm gonna come"
			pairs := strings.Split(env, ",")
			for _, pair := range pairs {
				parts := strings.SplitN(strings.TrimSpace(pair), ":", 2)
				if len(parts) == 2 {
					AvailableSounds[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	// 2. Load from custom_sounds.json if present (in working dir, CUSTOM_SOUNDS_FILE, or /data/custom_sounds.json)
	soundFile := os.Getenv("CUSTOM_SOUNDS_FILE")
	if soundFile == "" {
		if _, err := os.Stat("custom_sounds.json"); err == nil {
			soundFile = "custom_sounds.json"
		} else if _, err := os.Stat("/data/custom_sounds.json"); err == nil {
			soundFile = "/data/custom_sounds.json"
		}
	}
	if soundFile != "" {
		if data, err := os.ReadFile(soundFile); err == nil {
			var fileMap map[string]string
			if err := json.Unmarshal(data, &fileMap); err == nil {
				for id, name := range fileMap {
					AvailableSounds[id] = name
				}
			}
		}
	}

	// 3. Scan CUSTOM_SOUNDS_DIR or /data/music/EN for descriptive filenames like "98_do_not_come.ogg" or "99.ogg"
	soundDir := os.Getenv("CUSTOM_SOUNDS_DIR")
	if soundDir == "" {
		if _, err := os.Stat("/data/music/EN"); err == nil {
			soundDir = "/data/music/EN"
		}
	}
	if soundDir != "" {
		if entries, err := os.ReadDir(soundDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".ogg") {
					continue
				}
				base := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
				// If named "98_Do_not_come", extract ID 98 and title "Do not come"
				if idx := strings.IndexAny(base, "_- "); idx > 0 {
					id := base[:idx]
					name := strings.Trim(base[idx+1:], "_- ")
					name = strings.ReplaceAll(name, "_", " ")
					if _, err := strconv.Atoi(id); err == nil && name != "" {
						AvailableSounds[id] = name
						continue
					}
				}
				// If named simply "98.ogg" and not in stock sounds, add placeholder if not already registered
				if _, err := strconv.Atoi(base); err == nil {
					if _, exists := AvailableSounds[base]; !exists {
						AvailableSounds[base] = fmt.Sprintf("Custom Sound %s", base)
					}
				}
			}
		}
	}
}

func PlaySound(sound Sound) XMLMsg {
	return XMLMsg{
		cmdName: "PlaySound",
		cmdOpts: mxj.Map{"-sid": string(sound)},
	}
}

func PlaySoundInt(num int) XMLMsg {
	return XMLMsg{
		cmdName: "PlaySound",
		cmdOpts: mxj.Map{"-sid": strconv.Itoa(num)},
	}
}

// XML-based spot area mapping
func PullM(msid string, mid string) XMLMsg {
	return PullMType("sa", msid, mid)
}

func PullMType(tp, msid, mid string) XMLMsg {
	return XMLMsg{
		cmdName: "PullM",
		cmdOpts: mxj.Map{"-id": tp + mid, "-tp": tp, "-msid": msid, "-mid": mid, "-seq": "0"},
	}
}

// SetVirtualRestrictions sets virtual walls and no-go areas on the robot
func SetVirtualRestrictions(msid string, walls [][4]int, zones [][8]int) XMLMsg {
	var mList []interface{}
	for _, w := range walls {
		vStr := fmt.Sprintf("[%d,%d,%d,%d]", w[0], w[1], w[2], w[3])
		mList = append(mList, mxj.Map{"-v": vStr})
	}
	for _, z := range zones {
		vStr := fmt.Sprintf("[%d,%d,%d,%d,%d,%d,%d,%d]", z[0], z[1], z[2], z[3], z[4], z[5], z[6], z[7])
		mList = append(mList, mxj.Map{"-v": vStr})
	}

	opts := mxj.Map{
		"-tp":   "vw",
		"-msid": msid,
	}
	if len(mList) > 0 {
		opts["m"] = mList
	}
	return XMLMsg{
		cmdName: "SetM",
		cmdOpts: opts,
	}
}

var GetMapSet XMLMsg = XMLMsg{
	cmdName: "GetMapSet",
	cmdOpts: mxj.Map{"-tp": "sa"},
}

var GetMapM XMLMsg = XMLMsg{
	cmdName: "GetMapM",
	cmdOpts: mxj.Map{},
}

func PullMp(pieceId int) XMLMsg {
	return XMLMsg{
		cmdName: "PullMP",
		cmdOpts: mxj.Map{
			"-pid": pieceId,
			"-id":  fmt.Sprintf("mp%02d", pieceId),
		},
	}
}

// GetWaterBoxInfo requests the water box install status (true if installed)
var GetWaterBoxInfo XMLMsg = XMLMsg{
	cmdName: "GetWaterBoxInfo",
	cmdOpts: mxj.Map{},
}

// GetCleanState requests current cleaning state
var GetCleanState XMLMsg = XMLMsg{
	cmdName: "GetCleanState",
	cmdOpts: mxj.Map{},
}

// GetError requests current error state list
var GetError XMLMsg = XMLMsg{
	cmdName: "GetError",
	cmdOpts: mxj.Map{},
}

// GetSleepStatus requests current sleep status
var GetSleepStatus XMLMsg = XMLMsg{
	cmdName: "GetSleepStatus",
	cmdOpts: mxj.Map{},
}

// SetOnOff sets specific switch states (t: switch type, on: true/false)
func SetOnOff(switchType string, on bool) XMLMsg {
	onStr := "0"
	if on {
		onStr = "1"
	}
	return XMLMsg{
		cmdName: "SetOnOff",
		cmdOpts: mxj.Map{
			"-t":  switchType,
			"-on": onStr,
		},
	}
}

// GetOnOff requests specific switch state (t: switch type)
func GetOnOff(switchType string) XMLMsg {
	return XMLMsg{
		cmdName: "GetOnOff",
		cmdOpts: mxj.Map{
			"-id": fmt.Sprintf("onoff_%s", switchType),
			"-t":  switchType,
		},
	}
}

// SetSilentModeState sets the silent mode state
func SetSilentModeState(on bool) XMLMsg {
	onStr := "0"
	if on {
		onStr = "1"
	}
	return XMLMsg{
		cmdName: "SetSilentModeState",
		cmdOpts: mxj.Map{
			"-on": onStr,
		},
	}
}

// GetSilentModeState requests the silent mode state
var GetSilentModeState XMLMsg = XMLMsg{
	cmdName: "GetSilentModeState",
	cmdOpts: mxj.Map{},
}

// SetCleanSpeed sets the clean speed (speed: "standard", "silent", "strong", "intelligent")
func SetCleanSpeed(speed string) XMLMsg {
	return XMLMsg{
		cmdName: "SetCleanSpeed",
		cmdOpts: mxj.Map{
			"-speed": speed,
		},
	}
}

// GetCleanSpeed requests the clean speed
var GetCleanSpeed XMLMsg = XMLMsg{
	cmdName: "GetCleanSpeed",
	cmdOpts: mxj.Map{},
}

// GetChargerPos requests the charging dock position
var GetChargerPos XMLMsg = XMLMsg{
	cmdName: "GetChargerPos",
	cmdOpts: mxj.Map{},
}

// GetPos requests current coordinates of the robot
var GetPos XMLMsg = XMLMsg{
	cmdName: "GetPos",
	cmdOpts: mxj.Map{},
}

// ClearMap clears the virtual map
var ClearMap XMLMsg = XMLMsg{
	cmdName: "ClearMap",
	cmdOpts: mxj.Map{},
}

// RecoverMap restores the backup map
var RecoverMap XMLMsg = XMLMsg{
	cmdName: "RecoverMap",
	cmdOpts: mxj.Map{},
}

// IfHaveMap checks if the robot has a saved map
var IfHaveMap XMLMsg = XMLMsg{
	cmdName: "IfHaveMap",
	cmdOpts: mxj.Map{},
}

// GetMapSt requests map status
var GetMapSt XMLMsg = XMLMsg{
	cmdName: "GetMapSt",
	cmdOpts: mxj.Map{},
}

// FactoryReset triggers a factory reset
var FactoryReset XMLMsg = XMLMsg{
	cmdName: "FactoryReset",
	cmdOpts: mxj.Map{},
}

// GetTime requests the current time set on the device
var GetTime XMLMsg = XMLMsg{
	cmdName: "GetTime",
	cmdOpts: mxj.Map{},
}

// SetTime sets the time on the robot
// t: Unix timestamp, tz: timezone offset hours, tzm: timezone offset minutes
func SetTime(t int64, tz int, tzm int) XMLMsg {
	return XMLMsg{
		cmdName: "SetTime",
		cmdOpts: mxj.Map{
			"time": mxj.Map{
				"-t":   strconv.FormatInt(t, 10),
				"-tz":  strconv.Itoa(tz),
				"-tzm": strconv.Itoa(tzm),
			},
		},
	}
}

// GetBlockTime requests the current DND (Do Not Disturb) block time
var GetBlockTime XMLMsg = XMLMsg{
	cmdName: "GetBlockTime",
	cmdOpts: mxj.Map{},
}

// SetBlockTime sets the DND block time range
func SetBlockTime(startHour, startMinute, endHour, endMinute int) XMLMsg {
	return XMLMsg{
		cmdName: "SetBlockTime",
		cmdOpts: mxj.Map{
			"bt": mxj.Map{
				"-sh": strconv.Itoa(startHour),
				"-sm": strconv.Itoa(startMinute),
				"-eh": strconv.Itoa(endHour),
				"-em": strconv.Itoa(endMinute),
			},
		},
	}
}

// GetBlockTimeState requests the current DND block time enabled/disabled state
var GetBlockTimeState XMLMsg = XMLMsg{
	cmdName: "GetBlockTimeState",
	cmdOpts: mxj.Map{},
}

// SetBlockTimeState enables or disables the DND block time
func SetBlockTimeState(enabled bool) XMLMsg {
	onStr := "0"
	if enabled {
		onStr = "1"
	}
	return XMLMsg{
		cmdName: "SetBlockTimeState",
		cmdOpts: mxj.Map{
			"-on": onStr,
		},
	}
}

// Move manually controls robot movement (forward, backward, left, right, spin_left, spin_right, turn_around, stop)
func Move(act string) XMLMsg {
	return XMLMsg{
		cmdName: "Move",
		cmdOpts: mxj.Map{
			"-act": act,
		},
	}
}

// ==========================================
// Advanced Cleaning Commands
// ==========================================

var CleanPause XMLMsg = XMLMsg{
	cmdName: "Clean",
	cmdOpts: mxj.Map{
		"clean": mxj.Map{
			"-act": "p",
		},
	},
}

var CleanResume XMLMsg = XMLMsg{
	cmdName: "Clean",
	cmdOpts: mxj.Map{
		"clean": mxj.Map{
			"-act": "r",
		},
	},
}

var CleanStop XMLMsg = XMLMsg{
	cmdName: "Clean",
	cmdOpts: mxj.Map{
		"clean": mxj.Map{
			"-act": "h",
		},
	},
}

// CleanSpotArea cleans specified room/spot area IDs (comma-separated, e.g. "0,1")
// Note: Decompiled firmware (plug_medusa_plugin_wifiHandler.so @ 0x183c0) checks:
// if (mid != NULL && p != NULL) -> aborts with "spotArea has mid and p".
// Room cleaning (TaskType 2) requires mid="0,1" and NO "p" attribute.
func CleanSpotArea(roomIDs string) XMLMsg {
	return XMLMsg{
		cmdName: "Clean",
		cmdOpts: mxj.Map{
			"clean": mxj.Map{
				"-act":  "s",
				"-type": "SpotArea",
				"-mid":  roomIDs,
				"-deep": "1",
			},
		},
	}
}

// CleanCustomArea cleans rectangular coordinates "x1,y1,x2,y2"
// Note: Decompiled firmware (plug_medusa_plugin_wifiHandler.so @ 0x183c0):
// Zone cleaning (TaskType 9) requires type="SpotArea" with p="x1,y1,x2,y2" and NO "mid" attribute.
// If type="spot" is used, the firmware treats it as single-point clean (TaskType 1) and fails navigation.
func CleanCustomArea(p string, count int) XMLMsg {
	deepStr := "1"
	if count > 1 {
		deepStr = strconv.Itoa(count)
	}
	return XMLMsg{
		cmdName: "Clean",
		cmdOpts: mxj.Map{
			"clean": mxj.Map{
				"-act":  "s",
				"-type": "SpotArea",
				"-p":    p,
				"-deep": deepStr,
			},
		},
	}
}

// GetCleanSum requests lifetime clean summary (count, total seconds, total area)
var GetCleanSum XMLMsg = XMLMsg{
	cmdName: "GetCleanSum",
	cmdOpts: mxj.Map{
		"-id": "cleansum",
	},
}

// GetCleanLogs requests historical cleaning session logs
func GetCleanLogs(count int) XMLMsg {
	return XMLMsg{
		cmdName: "GetCleanLogs",
		cmdOpts: mxj.Map{
			"-count": strconv.Itoa(count),
		},
	}
}

// EmptyCleanLog clears the stored cleaning logs
var EmptyCleanLog XMLMsg = XMLMsg{
	cmdName: "EmptyCleanLog",
	cmdOpts: mxj.Map{},
}

// GetAppWorkMode queries the robot's current operation mode
var GetAppWorkMode XMLMsg = XMLMsg{
	cmdName: "GetAppWorkMode",
	cmdOpts: mxj.Map{},
}

// ==========================================
// Consumables Lifespan Commands
// ==========================================

var GetSideBrushLifeSpan XMLMsg = XMLMsg{
	cmdName: "GetLifeSpan",
	cmdOpts: mxj.Map{"-type": "SideBrush"},
}

var GetDustCaseHeapLifeSpan XMLMsg = XMLMsg{
	cmdName: "GetLifeSpan",
	cmdOpts: mxj.Map{"-type": "DustCaseHeap"},
}

// ResetLifeSpan resets a component wear percentage back to 100% ("Brush", "SideBrush", "DustCaseHeap")
func ResetLifeSpan(component string) XMLMsg {
	return XMLMsg{
		cmdName: "ResetLifeSpan",
		cmdOpts: mxj.Map{
			"-type": component,
		},
	}
}

// ==========================================
// Audio & Language Commands
// ==========================================

var GetLanguages XMLMsg = XMLMsg{
	cmdName: "GetLanguages",
	cmdOpts: mxj.Map{},
}

var GetActiveLanguage XMLMsg = XMLMsg{
	cmdName: "GetActiveLanguage",
	cmdOpts: mxj.Map{},
}

func SetActiveLanguage(langID string) XMLMsg {
	return XMLMsg{
		cmdName: "SetActiveLanguage",
		cmdOpts: mxj.Map{
			"-id": langID,
		},
	}
}

var GetMute XMLMsg = XMLMsg{
	cmdName: "GetMute",
	cmdOpts: mxj.Map{},
}

func SetMute(mute bool) XMLMsg {
	muteStr := "0"
	if mute {
		muteStr = "1"
	}
	return XMLMsg{
		cmdName: "SetMute",
		cmdOpts: mxj.Map{
			"-mute": muteStr,
		},
	}
}

// ==========================================
// Map Boundaries & Traces
// ==========================================

var GetMapVirtualWalls XMLMsg = XMLMsg{
	cmdName: "GetMapSet",
	cmdOpts: mxj.Map{
		"-tp": "vw",
	},
}

func SetMapAutoReport(enabled bool) XMLMsg {
	onStr := "0"
	if enabled {
		onStr = "1"
	}
	return XMLMsg{
		cmdName: "SetMapAutoReport",
		cmdOpts: mxj.Map{
			"-on": onStr,
		},
	}
}

var GetTrM XMLMsg = XMLMsg{
	cmdName: "GetTrM",
	cmdOpts: mxj.Map{},
}

func GetTr(trid int, tf int, tt int) XMLMsg {
	return XMLMsg{
		cmdName: "GetTr",
		cmdOpts: mxj.Map{
			"-trid": strconv.Itoa(trid),
			"-tf":   strconv.Itoa(tf),
			"-tt":   strconv.Itoa(tt),
		},
	}
}

func RenameM(msid string, names map[string]string) XMLMsg {
	var mList []interface{}
	for mid, name := range names {
		mList = append(mList, mxj.Map{
			"-mid": mid,
			"-n":   name,
		})
	}
	return XMLMsg{
		cmdName: "RenameM",
		cmdOpts: mxj.Map{
			"-tp":   "sa",
			"-msid": msid,
			"m":     mList,
		},
	}
}

// ==========================================
// Cleaning Schedule Commands
// ==========================================

var GetSched XMLMsg = XMLMsg{
	cmdName: "GetSched",
	cmdOpts: mxj.Map{},
}

func AddSched(timeStr, daysStr, act string) XMLMsg {
	return XMLMsg{
		cmdName: "AddSched",
		cmdOpts: mxj.Map{
			"s": mxj.Map{
				"-t":   timeStr,
				"-w":   daysStr,
				"-act": act,
				"-en":  "1",
			},
		},
	}
}

func DelSched(id string) XMLMsg {
	return XMLMsg{
		cmdName: "DelSched",
		cmdOpts: mxj.Map{
			"-id": id,
		},
	}
}

// ==========================================
// System & Hardware Commands
// ==========================================

var GetDeviceInfo XMLMsg = XMLMsg{
	cmdName: "GetDeviceInfo",
	cmdOpts: mxj.Map{},
}

var GetWKVer XMLMsg = XMLMsg{
	cmdName: "GetWKVer",
	cmdOpts: mxj.Map{},
}

var PowerOff XMLMsg = XMLMsg{
	cmdName: "PowerOff",
	cmdOpts: mxj.Map{},
}

// ==========================================
// User Access Control Commands
// ==========================================

func AddUserCmd(jid string) XMLMsg {
	return XMLMsg{
		cmdName: "AddUser",
		cmdOpts: mxj.Map{
			"-jid": jid,
		},
	}
}

func DelUserCmd(jid string) XMLMsg {
	return XMLMsg{
		cmdName: "DelUser",
		cmdOpts: mxj.Map{
			"-jid": jid,
		},
	}
}

var GetUserInfo XMLMsg = XMLMsg{
	cmdName: "GetUserInfo",
	cmdOpts: mxj.Map{},
}

func SetAC(jid string, userman, setting, clean bool) XMLMsg {
	umStr, setStr, clnStr := "0", "0", "0"
	if userman {
		umStr = "1"
	}
	if setting {
		setStr = "1"
	}
	if clean {
		clnStr = "1"
	}
	return XMLMsg{
		cmdName: "SetAC",
		cmdOpts: mxj.Map{
			"-jid": jid,
			"acs": mxj.Map{
				"ac": []mxj.Map{
					{"-name": "userman", "-allow": umStr},
					{"-name": "setting", "-allow": setStr},
					{"-name": "clean", "-allow": clnStr},
				},
			},
		},
	}
}





