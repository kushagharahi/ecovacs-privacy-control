package valetudo

// ValetudoMap represents the official Valetudo Map JSON Schema (Version 2)
// As consumed by Home Assistant custom:valetudo-map-card and lovelace-xiaomi-vacuum-map-card.
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

// ValetudoState values conforming to Home Assistant / Valetudo state schema
type ValetudoState string

const (
	StateError     ValetudoState = "error"
	StateDocked    ValetudoState = "docked"
	StateCleaning  ValetudoState = "cleaning"
	StateReturning ValetudoState = "returning"
	StatePaused    ValetudoState = "paused"
	StateIdle      ValetudoState = "idle"
)

// MapEcovacsStateToValetudo converts Ecovacs ChargingState/CleanReport into standard ValetudoState
func MapEcovacsStateToValetudo(chargingState, cleanSpeed string) ValetudoState {
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
