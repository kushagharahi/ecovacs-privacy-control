package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApiDynamicSoundLoading(t *testing.T) {
	os.Unsetenv("CUSTOM_SOUNDS")
	os.Unsetenv("CUSTOM_SOUNDS_FILE")
	os.Unsetenv("CUSTOM_SOUNDS_DIR")
	InitSounds()

	if len(AvailableSounds) != len(StockSounds) {
		t.Fatalf("Expected %d stock sounds, got %d", len(StockSounds), len(AvailableSounds))
	}
	if _, exists := AvailableSounds["98"]; exists {
		t.Fatalf("Sound 98 should not exist in baseline stock sounds")
	}
	if _, exists := AvailableSounds["99"]; exists {
		t.Fatalf("Sound 99 should not exist in baseline stock sounds")
	}

	// Test CUSTOM_SOUNDS
	os.Setenv("CUSTOM_SOUNDS", `{"98": "Do not come", "99": "I'm gonna come"}`)
	InitSounds()
	if AvailableSounds["98"] != "Do not come" {
		t.Errorf("Expected sound 98 to be 'Do not come', got '%s'", AvailableSounds["98"])
	}
	if AvailableSounds["99"] != "I'm gonna come" {
		t.Errorf("Expected sound 99 to be 'I'm gonna come', got '%s'", AvailableSounds["99"])
	}

	// Test directory scanning
	tmpDir := t.TempDir()
	os.Unsetenv("CUSTOM_SOUNDS")
	os.Setenv("CUSTOM_SOUNDS_DIR", tmpDir)

	_ = os.WriteFile(filepath.Join(tmpDir, "95_custom_greeting.ogg"), []byte("dummy"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "96.ogg"), []byte("dummy"), 0644)

	InitSounds()
	if AvailableSounds["95"] != "custom greeting" {
		t.Errorf("Expected sound 95 to be 'custom greeting', got '%s'", AvailableSounds["95"])
	}
	if AvailableSounds["96"] != "Custom Sound 96" {
		t.Errorf("Expected sound 96 to be 'Custom Sound 96', got '%s'", AvailableSounds["96"])
	}

	os.Unsetenv("CUSTOM_SOUNDS_DIR")
	InitSounds()
}
