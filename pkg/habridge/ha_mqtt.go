package habridge

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/kushagharahi/ecovacs-privacy-control/pkg/ecovacs"
)

type HADeviceInfo struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Model        string   `json:"model"`
	Manufacturer string   `json:"manufacturer"`
	SwVersion    string   `json:"sw_version"`
}

// PublishHADiscovery publishes all Home Assistant MQTT Auto-Discovery payloads for the OZMO 930.
func PublishHADiscovery(client mqtt.Client, haPrefix, topicPrefix, sn string) error {
	// First, clear old discovery topics so entity names stay clean
	ClearOldHADiscovery(client, haPrefix, sn)

	device := HADeviceInfo{
		Identifiers:  []string{sn, "ozmo_930"},
		Name:         "Ecovacs Deebot OZMO 930",
		Model:        "Deebot OZMO 930 (DR930)",
		Manufacturer: "Ecovacs",
		SwVersion:    "1.9.9",
	}

	// 1. Vacuum Entity (creates vacuum.ozmo_930)
	vacuumConfig := map[string]interface{}{
		"name":                  "OZMO 930",
		"unique_id":             "ozmo_930_vacuum",
		"object_id":             "ozmo_930",
		"device":                device,
		"schema":                "state",
		"state_topic":           fmt.Sprintf("%s/state", topicPrefix),
		"command_topic":         fmt.Sprintf("%s/command", topicPrefix),
		"json_attributes_topic": fmt.Sprintf("%s/vacuum_attributes", topicPrefix),
		"supported_features":    []string{"start", "pause", "stop", "return_home", "status", "locate"},
	}
	publishJSON(client, fmt.Sprintf("%s/vacuum/ozmo_930/config", haPrefix), vacuumConfig)

	// 2. Camera Entity for HA vacuum map cards (creates camera.ozmo_930_map_data)
	// Serves a PNG with an embedded ValetudoMap zTXt chunk.
	cameraConfig := map[string]interface{}{
		"name":                  "OZMO 930 Map Data",
		"unique_id":             "ozmo_930_map_data",
		"object_id":             "ozmo_930_map_data",
		"device":                device,
		"topic":                 fmt.Sprintf("%s/map_data", topicPrefix),
		"json_attributes_topic": fmt.Sprintf("%s/map_attributes", topicPrefix),
		"icon":                  "mdi:floor-plan",
	}
	publishJSON(client, fmt.Sprintf("%s/camera/ozmo_930_map_data/config", haPrefix), cameraConfig)

	// 3. Telemetry & Consumable Sensors
	publishSensor(client, haPrefix, topicPrefix, device, "battery", "Battery Level", "%", "mdi:battery")
	publishSensor(client, haPrefix, topicPrefix, device, "brush_lifespan", "Main Brush Lifespan", "%", "mdi:broom")
	publishSensor(client, haPrefix, topicPrefix, device, "side_brush_lifespan", "Side Brush Lifespan", "%", "mdi:broom")
	publishSensor(client, haPrefix, topicPrefix, device, "filter_lifespan", "Dust Filter Lifespan", "%", "mdi:air-filter")
	publishSensor(client, haPrefix, topicPrefix, device, "total_clean_count", "Total Clean Count", "times", "mdi:counter")
	publishSensor(client, haPrefix, topicPrefix, device, "total_clean_area", "Total Clean Area", "m²", "mdi:texture-box")
	publishSensor(client, haPrefix, topicPrefix, device, "total_clean_time", "Total Clean Time", "h", "mdi:clock-outline")

	// 4. Binary Sensor: Water Box Installed
	waterBoxConfig := map[string]interface{}{
		"name":         "OZMO Water Box Installed",
		"unique_id":    "ozmo_930_water_box",
		"object_id":    "ozmo_930_water_box",
		"device":       device,
		"device_class": "presence",
		"state_topic":  fmt.Sprintf("%s/water_box", topicPrefix),
		"payload_on":   "ON",
		"payload_off":  "OFF",
		"icon":         "mdi:water",
	}
	publishJSON(client, fmt.Sprintf("%s/binary_sensor/ozmo_930_wb/config", haPrefix), waterBoxConfig)

	// 5. Select Entity: Water Flow Rate
	waterFlowConfig := map[string]interface{}{
		"name":          "OZMO Water Flow Rate",
		"unique_id":     "ozmo_930_water_flow",
		"object_id":     "ozmo_930_water_flow",
		"device":        device,
		"state_topic":   fmt.Sprintf("%s/water_flow", topicPrefix),
		"command_topic": fmt.Sprintf("%s/set_water_flow", topicPrefix),
		"options":       []string{"Low", "Medium", "High", "Max"},
		"icon":          "mdi:water-pump",
	}
	publishJSON(client, fmt.Sprintf("%s/select/ozmo_930_wf/config", haPrefix), waterFlowConfig)

	// 5b. Switch Entity: Carpet Boost (BPC)
	carpetBoostConfig := map[string]interface{}{
		"name":          "OZMO Carpet Boost",
		"unique_id":     "ozmo_930_carpet_boost",
		"object_id":     "ozmo_930_carpet_boost",
		"device":        device,
		"state_topic":   fmt.Sprintf("%s/carpet_boost", topicPrefix),
		"command_topic": fmt.Sprintf("%s/set_carpet_boost", topicPrefix),
		"payload_on":    "ON",
		"payload_off":   "OFF",
		"icon":          "mdi:rug",
	}
	publishJSON(client, fmt.Sprintf("%s/switch/ozmo_930_cb/config", haPrefix), carpetBoostConfig)

	// 5c. Switch Entity: Continuous Cleaning (Breakpoint Resume)
	continuousCleanConfig := map[string]interface{}{
		"name":          "OZMO Continuous Cleaning",
		"unique_id":     "ozmo_930_continuous_clean",
		"object_id":     "ozmo_930_continuous_clean",
		"device":        device,
		"state_topic":   fmt.Sprintf("%s/continuous_clean", topicPrefix),
		"command_topic": fmt.Sprintf("%s/set_continuous_clean", topicPrefix),
		"payload_on":    "ON",
		"payload_off":   "OFF",
		"icon":          "mdi:play-pause",
	}
	publishJSON(client, fmt.Sprintf("%s/switch/ozmo_930_cc/config", haPrefix), continuousCleanConfig)

	// 5d. Switch Entity: Voice Reports (Sound Prompts)
	voiceReportsConfig := map[string]interface{}{
		"name":          "OZMO Voice Reports",
		"unique_id":     "ozmo_930_voice_reports",
		"object_id":     "ozmo_930_voice_reports",
		"device":        device,
		"state_topic":   fmt.Sprintf("%s/voice_reports", topicPrefix),
		"command_topic": fmt.Sprintf("%s/set_voice_reports", topicPrefix),
		"payload_on":    "ON",
		"payload_off":   "OFF",
		"icon":          "mdi:volume-source",
	}
	publishJSON(client, fmt.Sprintf("%s/switch/ozmo_930_vr/config", haPrefix), voiceReportsConfig)

	// 5e. Switch Entity: Do Not Disturb Mode (DND)
	dndConfig := map[string]interface{}{
		"name":          "OZMO Do Not Disturb",
		"unique_id":     "ozmo_930_dnd",
		"object_id":     "ozmo_930_dnd",
		"device":        device,
		"state_topic":   fmt.Sprintf("%s/dnd", topicPrefix),
		"command_topic": fmt.Sprintf("%s/set_dnd", topicPrefix),
		"payload_on":    "ON",
		"payload_off":   "OFF",
		"icon":          "mdi:bell-cancel",
	}
	publishJSON(client, fmt.Sprintf("%s/switch/ozmo_930_dnd/config", haPrefix), dndConfig)

	// 5f. Button Entities: Consumable Lifespan Resets
	for _, comp := range []struct{ id, name, topic, icon string }{
		{"reset_main_brush", "Reset Main Brush Lifespan", "reset_main_brush", "mdi:broom"},
		{"reset_side_brush", "Reset Side Brush Lifespan", "reset_side_brush", "mdi:broom"},
		{"reset_dust_filter", "Reset Dust Filter Lifespan", "reset_dust_filter", "mdi:air-filter"},
	} {
		btnConfig := map[string]interface{}{
			"name":          "OZMO " + comp.name,
			"unique_id":     "ozmo_930_" + comp.id,
			"object_id":     "ozmo_930_" + comp.id,
			"device":        device,
			"command_topic": fmt.Sprintf("%s/%s", topicPrefix, comp.topic),
			"icon":          comp.icon,
		}
		publishJSON(client, fmt.Sprintf("%s/button/ozmo_930_%s/config", haPrefix, comp.id), btnConfig)
	}

	// Clean up legacy non-functional fan speed select entity in Home Assistant
	client.Publish(fmt.Sprintf("%s/select/ozmo_930_fs/config", haPrefix), 0, true, "")

	// 6. Number Entity: Speaker Volume
	volumeConfig := map[string]interface{}{
		"name":          "OZMO Speaker Volume",
		"unique_id":     "ozmo_930_volume",
		"object_id":     "ozmo_930_volume",
		"device":        device,
		"state_topic":   fmt.Sprintf("%s/volume", topicPrefix),
		"command_topic": fmt.Sprintf("%s/set_volume", topicPrefix),
		"min":           0,
		"max":           100,
		"step":          5,
		"icon":          "mdi:volume-high",
	}
	publishJSON(client, fmt.Sprintf("%s/number/ozmo_930_vol/config", haPrefix), volumeConfig)

	// 7. Select Entity: Soundboard (Voice Prompts from ecovacs.AvailableSounds)
	soundOptions := make([]string, 0, len(ecovacs.AvailableSounds))
	for id, name := range ecovacs.AvailableSounds {
		soundOptions = append(soundOptions, fmt.Sprintf("%02d: %s", id, name))
	}
	sort.Strings(soundOptions)

	soundConfig := map[string]interface{}{
		"name":          "OZMO Voice Prompt Soundboard",
		"unique_id":     "ozmo_930_soundboard",
		"object_id":     "ozmo_930_soundboard",
		"device":        device,
		"state_topic":   fmt.Sprintf("%s/last_sound", topicPrefix),
		"command_topic": fmt.Sprintf("%s/play_sound", topicPrefix),
		"options":       soundOptions,
		"icon":          "mdi:account-voice",
	}
	publishJSON(client, fmt.Sprintf("%s/select/ozmo_930_snd/config", haPrefix), soundConfig)

	log.Printf("[HA-MQTT] Auto-Discovery published to %s/ (vacuum.ozmo_930, camera.ozmo_930_map_data)", haPrefix)
	return nil
}

// ClearOldHADiscovery sends empty retained messages to clear previously configured long-form entity IDs.
func ClearOldHADiscovery(client mqtt.Client, haPrefix, sn string) {
	oldTopics := []string{
		fmt.Sprintf("%s/vacuum/%s/config", haPrefix, sn),
		fmt.Sprintf("%s/camera/%s_map/config", haPrefix, sn),
		fmt.Sprintf("%s/sensor/%s_battery/config", haPrefix, sn),
		fmt.Sprintf("%s/sensor/%s_brush_lifespan/config", haPrefix, sn),
		fmt.Sprintf("%s/sensor/%s_side_brush_lifespan/config", haPrefix, sn),
		fmt.Sprintf("%s/sensor/%s_filter_lifespan/config", haPrefix, sn),
		fmt.Sprintf("%s/binary_sensor/%s_wb/config", haPrefix, sn),
		fmt.Sprintf("%s/select/%s_wf/config", haPrefix, sn),
		fmt.Sprintf("%s/number/%s_vol/config", haPrefix, sn),
		fmt.Sprintf("%s/select/%s_snd/config", haPrefix, sn),
		fmt.Sprintf("%s/select/ozmo_930_goto_snd/config", haPrefix),
		fmt.Sprintf("%s/goto_sound", sn),
	}
	for _, top := range oldTopics {
		client.Publish(top, 0, true, "")
	}
}

func publishSensor(client mqtt.Client, prefix, topicPrefix string, device HADeviceInfo, key, name, unit, icon string) {
	config := map[string]interface{}{
		"name":                fmt.Sprintf("OZMO %s", name),
		"unique_id":           fmt.Sprintf("ozmo_930_%s", key),
		"object_id":           fmt.Sprintf("ozmo_930_%s", key),
		"device":              device,
		"state_topic":         fmt.Sprintf("%s/%s", topicPrefix, key),
		"unit_of_measurement": unit,
		"icon":                icon,
	}
	publishJSON(client, fmt.Sprintf("%s/sensor/ozmo_930_%s/config", prefix, key), config)
}

func publishJSON(client mqtt.Client, topic string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[HA-MQTT] Marshal error for %s: %v", topic, err)
		return
	}
	client.Publish(topic, 0, true, string(data))
}
