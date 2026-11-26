package controller

import (
	"fmt"
	"strconv"

	mxj "github.com/clbanning/mxj/v2"
	"github.com/kushagharahi/ecovacs-privacy-control/pkg/ecovacs"
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

// GetLifeSpan requests the wear status of parts (e.g. "Brush", "SideBrush", "DustCaseHeap")
func GetLifeSpan(component string) XMLMsg {
	return XMLMsg{
		cmdName: "GetLifeSpan",
		cmdOpts: mxj.Map{
			"-type": component,
		},
	}
}

// GetBrushLifeSpan is a helper alias for GetLifeSpan("Brush") to preserve backwards compatibility
var GetBrushLifeSpan XMLMsg = GetLifeSpan("Brush")

func PlaySound(sound ecovacs.Sound) XMLMsg {
	return PlaySoundSID(strconv.Itoa(int(sound)))
}

func PlaySoundSID(sid string) XMLMsg {
	return XMLMsg{
		cmdName: "PlaySound",
		cmdOpts: mxj.Map{"-sid": sid},
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

var GetWaterBoxInfo XMLMsg = XMLMsg{
	cmdName: "GetWaterBoxInfo",
	cmdOpts: mxj.Map{},
}

var GetCleanState XMLMsg = XMLMsg{
	cmdName: "GetCleanState",
	cmdOpts: mxj.Map{},
}

var GetError XMLMsg = XMLMsg{
	cmdName: "GetError",
	cmdOpts: mxj.Map{},
}

var GetSleepStatus XMLMsg = XMLMsg{
	cmdName: "GetSleepStatus",
	cmdOpts: mxj.Map{},
}

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

func GetOnOff(switchType string) XMLMsg {
	return XMLMsg{
		cmdName: "GetOnOff",
		cmdOpts: mxj.Map{
			"-id": fmt.Sprintf("onoff_%s", switchType),
			"-t":  switchType,
		},
	}
}

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

var GetSilentModeState XMLMsg = XMLMsg{
	cmdName: "GetSilentModeState",
	cmdOpts: mxj.Map{},
}

func SetCleanSpeed(speed string) XMLMsg {
	return XMLMsg{
		cmdName: "SetCleanSpeed",
		cmdOpts: mxj.Map{
			"-speed": speed,
		},
	}
}

var GetCleanSpeed XMLMsg = XMLMsg{
	cmdName: "GetCleanSpeed",
	cmdOpts: mxj.Map{},
}

var GetChargerPos XMLMsg = XMLMsg{
	cmdName: "GetChargerPos",
	cmdOpts: mxj.Map{},
}

var GetPos XMLMsg = XMLMsg{
	cmdName: "GetPos",
	cmdOpts: mxj.Map{},
}

var ClearMap XMLMsg = XMLMsg{
	cmdName: "ClearMap",
	cmdOpts: mxj.Map{},
}

var RecoverMap XMLMsg = XMLMsg{
	cmdName: "RecoverMap",
	cmdOpts: mxj.Map{},
}

var IfHaveMap XMLMsg = XMLMsg{
	cmdName: "IfHaveMap",
	cmdOpts: mxj.Map{},
}

var GetMapSt XMLMsg = XMLMsg{
	cmdName: "GetMapSt",
	cmdOpts: mxj.Map{},
}

var FactoryReset XMLMsg = XMLMsg{
	cmdName: "FactoryReset",
	cmdOpts: mxj.Map{},
}

var GetTime XMLMsg = XMLMsg{
	cmdName: "GetTime",
	cmdOpts: mxj.Map{},
}

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

var GetBlockTime XMLMsg = XMLMsg{
	cmdName: "GetBlockTime",
	cmdOpts: mxj.Map{},
}

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

var GetBlockTimeState XMLMsg = XMLMsg{
	cmdName: "GetBlockTimeState",
	cmdOpts: mxj.Map{},
}

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

func Move(act string) XMLMsg {
	return XMLMsg{
		cmdName: "Move",
		cmdOpts: mxj.Map{
			"-act": act,
		},
	}
}

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

var GetCleanSum XMLMsg = XMLMsg{
	cmdName: "GetCleanSum",
	cmdOpts: mxj.Map{
		"-id": "cleansum",
	},
}

func GetCleanLogs(count int) XMLMsg {
	return XMLMsg{
		cmdName: "GetCleanLogs",
		cmdOpts: mxj.Map{
			"-count": strconv.Itoa(count),
		},
	}
}

var EmptyCleanLog XMLMsg = XMLMsg{
	cmdName: "EmptyCleanLog",
	cmdOpts: mxj.Map{},
}

var GetAppWorkMode XMLMsg = XMLMsg{
	cmdName: "GetAppWorkMode",
	cmdOpts: mxj.Map{},
}

var GetSideBrushLifeSpan XMLMsg = XMLMsg{
	cmdName: "GetLifeSpan",
	cmdOpts: mxj.Map{"-type": "SideBrush"},
}

var GetDustCaseHeapLifeSpan XMLMsg = XMLMsg{
	cmdName: "GetLifeSpan",
	cmdOpts: mxj.Map{"-type": "DustCaseHeap"},
}

func ResetLifeSpan(component string) XMLMsg {
	return XMLMsg{
		cmdName: "ResetLifeSpan",
		cmdOpts: mxj.Map{
			"-type": component,
		},
	}
}

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
	return GetTrWithID("", trid, tf, tt)
}

func GetTrWithID(reqID string, trid int, tf int, tt int) XMLMsg {
	opts := mxj.Map{
		"-trid": strconv.Itoa(trid),
		"-tf":   strconv.Itoa(tf),
		"-tt":   strconv.Itoa(tt),
	}
	if reqID != "" {
		opts["-id"] = reqID
	}
	return XMLMsg{
		cmdName: "GetTr",
		cmdOpts: opts,
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
