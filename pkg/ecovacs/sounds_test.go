package ecovacs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDynamicSoundLoading(t *testing.T) {
	// Baseline: Stock sounds only
	os.Unsetenv("CUSTOM_SOUNDS")
	os.Unsetenv("CUSTOM_SOUNDS_FILE")
	os.Unsetenv("CUSTOM_SOUNDS_DIR")
	InitSounds()

	if len(AvailableSounds) != len(StockSounds) {
		t.Fatalf("Expected %d stock sounds, got %d", len(StockSounds), len(AvailableSounds))
	}
	if _, exists := AvailableSounds[Sound(98)]; exists {
		t.Fatalf("Sound 98 should not exist in baseline stock sounds")
	}
	if _, exists := AvailableSounds[Sound(99)]; exists {
		t.Fatalf("Sound 99 should not exist in baseline stock sounds")
	}

	// Dynamic test 1: Load from CUSTOM_SOUNDS environment variable
	os.Setenv("CUSTOM_SOUNDS", `{"98": "Do not come", "99": "I'm gonna come"}`)
	InitSounds()
	if AvailableSounds[Sound(98)] != "Do not come" {
		t.Errorf("Expected sound 98 to be 'Do not come', got '%s'", AvailableSounds[Sound(98)])
	}
	if AvailableSounds[Sound(99)] != "I'm gonna come" {
		t.Errorf("Expected sound 99 to be 'I'm gonna come', got '%s'", AvailableSounds[Sound(99)])
	}

	// Dynamic test 2: Load from directory with descriptive filenames
	tmpDir := t.TempDir()
	os.Unsetenv("CUSTOM_SOUNDS")
	os.Setenv("CUSTOM_SOUNDS_DIR", tmpDir)

	// Create test ogg files
	_ = os.WriteFile(filepath.Join(tmpDir, "95_custom_greeting.ogg"), []byte("dummy"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "96.ogg"), []byte("dummy"), 0644)

	InitSounds()
	if AvailableSounds[Sound(95)] != "custom greeting" {
		t.Errorf("Expected sound 95 to be 'custom greeting', got '%s'", AvailableSounds[Sound(95)])
	}
	if AvailableSounds[Sound(96)] != "Custom Sound 96" {
		t.Errorf("Expected sound 96 to be 'Custom Sound 96', got '%s'", AvailableSounds[Sound(96)])
	}

	// Reset clean baseline
	os.Unsetenv("CUSTOM_SOUNDS_DIR")
	InitSounds()
}
