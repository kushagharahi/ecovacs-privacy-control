package habridge

// ValetudoMap represents the Valetudo Map JSON Schema (Version 2) wire format
// consumed by Home Assistant custom:valetudo-map-card and lovelace-xiaomi-vacuum-map-card.
type ValetudoMap struct {
	Class     string      `json:"__class"`
	MetaData  MapMetaData `json:"metaData"`
	Size      MapSize     `json:"size"`
	PixelSize int         `json:"pixelSize"`
	Layers    []MapLayer  `json:"layers"`
	Entities  []MapEntity `json:"entities"`
}

type MapMetaData struct {
	Version int       `json:"version"`
	Nonce   string    `json:"nonce,omitempty"`
	Robot   RobotInfo `json:"robot"`
}

type RobotInfo struct {
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
}

type MapSize struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type LayerDimensions struct {
	X DimensionRange `json:"x"`
	Y DimensionRange `json:"y"`
}

type DimensionRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type MapLayer struct {
	Type       string          `json:"type"` // "floor", "wall", "segment"
	Pixels     []int           `json:"pixels"`
	Dimensions LayerDimensions `json:"dimensions"`
	MetaData   LayerMetaData   `json:"metaData,omitempty"`
}

type LayerMetaData struct {
	SegmentID string `json:"segmentId,omitempty"`
	Name      string `json:"name,omitempty"`
	Active    *bool  `json:"active,omitempty"`
}

type MapEntity struct {
	Type     string         `json:"type"` // "robot_position", "charger_location", "path", "virtual_wall"
	Points   []int          `json:"points"`
	MetaData EntityMetaData `json:"metaData,omitempty"`
}

type EntityMetaData struct {
	Angle int `json:"angle,omitempty"`
}

// CameraRoom represents one room entry in the format expected by
// lovelace-xiaomi-vacuum-map-card's camera attributes.rooms parser.
type CameraRoom struct {
	Name    string   `json:"name,omitempty"`
	X       *int     `json:"x,omitempty"`
	Y       *int     `json:"y,omitempty"`
	Outline [][2]int `json:"outline,omitempty"`
	X0      *int     `json:"x0,omitempty"`
	Y0      *int     `json:"y0,omitempty"`
	X1      *int     `json:"x1,omitempty"`
	Y1      *int     `json:"y1,omitempty"`
}

// VacuumState values conforming to the Home Assistant MQTT vacuum state schema.
type VacuumState string

const (
	StateError     VacuumState = "error"
	StateDocked    VacuumState = "docked"
	StateCleaning  VacuumState = "cleaning"
	StateReturning VacuumState = "returning"
	StatePaused    VacuumState = "paused"
	StateIdle      VacuumState = "idle"
)

// MapEcovacsState converts Ecovacs ChargingState/CleanReport into a Home Assistant VacuumState.
func MapEcovacsState(chargingState, cleanSpeed string) VacuumState {
	switch chargingState {
	case "SlotCharging", "Docked":
		return StateDocked
	case "Going", "Returning":
		return StateReturning
	case "Cleaning":
		return StateCleaning
	case "Paused":
		return StatePaused
	case "Idle":
		return StateIdle
	default:
		if cleanSpeed != "" && cleanSpeed != "Unknown" {
			return StateCleaning
		}
		return StateDocked
	}
}
