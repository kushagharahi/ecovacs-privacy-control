package ecovacs

import (
	"image/color"
)

// StatusResponse represents the canonical REST and telemetry status payload
type StatusResponse struct {
	BatteryLevel         int      `json:"battery_level"`
	ChargingState        string   `json:"charging_state"`
	VolumeLevel          int      `json:"volume_level"`
	WaterLevel           int      `json:"water_level"`
	WaterBoxInstalled    bool     `json:"water_box_installed"`
	CarpetBoost          bool     `json:"carpet_boost"`
	CleanSpeed           string   `json:"clean_speed"`
	SleepStatus          string   `json:"sleep_status"`
	BrushLifeSpan        int      `json:"brush_lifespan"`
	SideBrushLifeSpan    int      `json:"side_brush_lifespan"`
	DustCaseHeapLifeSpan int      `json:"filter_lifespan"`
	ContinuousClean      bool     `json:"continuous_clean"`
	VoiceReports         bool     `json:"voice_reports"`
	DNDEnabled           bool     `json:"dnd_enabled"`
	TotalCleanCount      int      `json:"total_clean_count"`
	TotalCleanArea       int      `json:"total_clean_area"`
	TotalCleanTime       int      `json:"total_clean_time"`
	DiscoveredRooms      []string `json:"discovered_rooms"`
	Errors               string   `json:"errors"`
	RobotPosX            int      `json:"robot_x"`
	RobotPosY            int      `json:"robot_y"`
	RobotAngle           int      `json:"robot_angle"`
	ChargerPosX          int      `json:"charger_x"`
	ChargerPosY          int      `json:"charger_y"`
	RobotPixelX          int      `json:"robot_pixel_x"`
	RobotPixelY          int      `json:"robot_pixel_y"`
	ChargerPixelX        int      `json:"charger_pixel_x"`
	ChargerPixelY        int      `json:"charger_pixel_y"`
	CropMinX             int      `json:"crop_min_x"`
	CropMinY             int      `json:"crop_min_y"`
}

// VirtualRestriction represents a virtual wall or no-go boundary
type VirtualRestriction struct {
	ID     int    `json:"id"`
	Type   string `json:"type"` // "virtual_wall" or "no_go_area"
	Points []int  `json:"points"` // raw coordinates in mm: [x1, y1, x2, y2] or [x1, y1, x2, y2, x3, y3, x4, y4]
}

// PathPoint represents a single point along the cleaning/mopping trajectory
type PathPoint struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Type      int  `json:"type"`      // 0 = clean, 1 = mop
	Connected bool `json:"connected"` // true = connected to previous point
}

// MapGridResponse represents raw or cropped map grid data from controller
type MapGridResponse struct {
	Width               int                  `json:"width"`
	Height              int                  `json:"height"`
	CropMinX            int                  `json:"crop_min_x"`
	CropMinY            int                  `json:"crop_min_y"`
	CropMaxX            int                  `json:"crop_max_x"`
	CropMaxY            int                  `json:"crop_max_y"`
	RobotPixelX         int                  `json:"robot_pixel_x"`
	RobotPixelY         int                  `json:"robot_pixel_y"`
	ChargerPixelX       int                  `json:"charger_pixel_x"`
	ChargerPixelY       int                  `json:"charger_pixel_y"`
	RobotAngle          int                  `json:"robot_angle"`
	Rooms               []string             `json:"rooms"`
	RoomNames           map[string]string    `json:"room_names"`
	VirtualRestrictions []VirtualRestriction `json:"virtual_restrictions"`
	PathPoints          []PathPoint          `json:"path_points"`
	Grid                [][]byte             `json:"grid"`
}

// Sound represents voice prompt IDs for the OZMO 930
type Sound int

const (
	BOOT_UP                               Sound = 0
	I_AM_SUSPENDED                        Sound = 3
	PLEASE_CHECK_THE_DRIVING_WHEELS       Sound = 4
	PLEASE_HELP_ME_OUT                    Sound = 5
	PLEASE_INSTALL_DUST_BIN               Sound = 6
	DING                                  Sound = 17
	MY_BATTERY_IS_LOW                     Sound = 18
	PLEASE_POWER_ME_ON_BEFORE_CHARGING    Sound = 29
	SOUND_I_AM_HERE                       Sound = 30
	BRUSH_IS_TANGLED_PLEASE_CLEAN_MY_BRUSH Sound = 31
	PLEASE_CLEAN_MY_ANTI_DROP_SENSORS     Sound = 35
	BRUSH_IS_TANGLED_PLEASE_CLEAN_MY_BRUSH_ALT Sound = 48
	I_AM_RELOCATING                       Sound = 55
	UPGRADE_SUCCEEDED                     Sound = 56
	I_AM_RETURNING_TO_THE_CHARGING_DOCK   Sound = 63
	CLEANING_PAUSED                       Sound = 65
	CONNECTED_PLEASE_GO_BACK_TO_THE_ECOVACS Sound = 69
	I_AM_RESTORING_THE_MAP_PLEASE_DONT_STAND_BESIDE_ME Sound = 71
	MY_BATTERY_IS_LOW_RETURNING_TO_THE_CHARGING_DOCK Sound = 73
	DIFFICULT_TO_LOCATE_I_AM_STARTING_A_NEW_CLEANING_CYCLE Sound = 74
	I_AM_RESUMING_THE_CLEAN               Sound = 75
	UPGRADE_FAILED_PLEASE_TRY_AGAIN       Sound = 76
	PLEASE_PLACE_ME_ON_THE_CHARGING_DOCK  Sound = 77
	RESUME_THE_CLEAN                      Sound = 79
	I_AM_STARTING_THE_CLEAN               Sound = 80
	I_AM_STARTING_THE_CLEAN_ALT           Sound = 81
	I_AM_STARTING_THE_CLEAN_ALT2          Sound = 82
	I_AM_READY_FOR_MOPPING                Sound = 84
	PLEASE_REMOVE_THE_MOPPING_PLATE_I_AM_BUILDING_THE_MAP Sound = 85
	CLEANING_IS_COMPLETE_RETURNING_TO_THE_CHARGING_DOCK Sound = 86
	LDS_MALFUNCTION_PLEASE_TRY_TO_TAP_THE_LDS Sound = 89
	I_AM_UPGRADING_PLEASE_WAIT            Sound = 90
	DO_NOT_COME                           Sound = 98
	IM_GUNNA_COME                         Sound = 99
)

var AvailableSounds = map[Sound]string{
	BOOT_UP:                               "Boot up chime",
	I_AM_SUSPENDED:                        "i am suspended",
	PLEASE_CHECK_THE_DRIVING_WHEELS:       "Please check the driving wheels",
	PLEASE_HELP_ME_OUT:                    "Please help me out",
	PLEASE_INSTALL_DUST_BIN:               "Please install dust bin",
	DING:                                  "ding",
	MY_BATTERY_IS_LOW:                     "my battery is low",
	PLEASE_POWER_ME_ON_BEFORE_CHARGING:    "please power me on before charging",
	SOUND_I_AM_HERE:                       "i am here",
	BRUSH_IS_TANGLED_PLEASE_CLEAN_MY_BRUSH: "brush is tangled please clean my brush",
	PLEASE_CLEAN_MY_ANTI_DROP_SENSORS:     "please clean my anti drop sensors",
	BRUSH_IS_TANGLED_PLEASE_CLEAN_MY_BRUSH_ALT: "brush is tangled please clean my brush",
	I_AM_RELOCATING:                       "I am relocating",
	UPGRADE_SUCCEEDED:                     "upgrade succeeded",
	I_AM_RETURNING_TO_THE_CHARGING_DOCK:   "i am returning to the charging dock",
	CLEANING_PAUSED:                       "cleaning paused",
	CONNECTED_PLEASE_GO_BACK_TO_THE_ECOVACS: "connected please go back to the ecovacs",
	I_AM_RESTORING_THE_MAP_PLEASE_DONT_STAND_BESIDE_ME: "i am restoring the map please don't stand beside me",
	MY_BATTERY_IS_LOW_RETURNING_TO_THE_CHARGING_DOCK: "my battery is low returning to the charging dock",
	DIFFICULT_TO_LOCATE_I_AM_STARTING_A_NEW_CLEANING_CYCLE: "difficult to locate i am starting a new cleaning cycle",
	I_AM_RESUMING_THE_CLEAN:               "i am resuming the clean",
	UPGRADE_FAILED_PLEASE_TRY_AGAIN:       "upgrade failed please try again",
	PLEASE_PLACE_ME_ON_THE_CHARGING_DOCK:  "please place me on the charging dock",
	RESUME_THE_CLEAN:                      "resume the clean",
	I_AM_STARTING_THE_CLEAN:               "i am starting the clean",
	I_AM_STARTING_THE_CLEAN_ALT:           "i am starting the clean",
	I_AM_STARTING_THE_CLEAN_ALT2:          "i am starting the clean",
	I_AM_READY_FOR_MOPPING:                "i am ready for mopping",
	PLEASE_REMOVE_THE_MOPPING_PLATE_I_AM_BUILDING_THE_MAP: "please remove the mopping plate i am building the map",
	CLEANING_IS_COMPLETE_RETURNING_TO_THE_CHARGING_DOCK: "cleaning is complete returning to the charging dock",
	LDS_MALFUNCTION_PLEASE_TRY_TO_TAP_THE_LDS: "LDS malfunction please try to tap the LDS",
	I_AM_UPGRADING_PLEASE_WAIT:            "I am upgrading please wait",
	DO_NOT_COME:                           "Do not come",
	IM_GUNNA_COME:                         "I'm gonna come",
}

type MapData byte

const (
	MapDataUnexplored MapData = 0
	MapDataFloor      MapData = 1
	MapDataObstacle   MapData = 2
)

func (m MapData) ColorMapping() color.RGBA {
	switch m {
	case MapDataUnexplored:
		return color.RGBA{0, 0, 0, 0}
	case MapDataFloor:
		return color.RGBA{255, 255, 255, 255}
	case MapDataObstacle:
		return color.RGBA{45, 45, 45, 255}
	default:
		return color.RGBA{200, 200, 200, 255}
	}
}
