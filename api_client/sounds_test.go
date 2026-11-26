package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kushagharahi/ecovacs-privacy-control/pkg/ecovacs"
)

func TestApiDynamicSoundLoading(t *testing.T) {
	os.Unsetenv("CUSTOM_SOUNDS")
	os.Unsetenv("CUSTOM_SOUNDS_FILE")
	os.Unsetenv("CUSTOM_SOUNDS_DIR")
	ecovacs.InitSounds()

	if len(ecovacs.AvailableSounds) != len(ecovacs.StockSounds) {
		t.Fatalf("Expected %d stock sounds, got %d", len(ecovacs.StockSounds), len(ecovacs.AvailableSounds))
	}
	if _, exists := ecovacs.AvailableSounds[ecovacs.Sound(98)]; exists {
		t.Fatalf("Sound 98 should not exist in baseline stock sounds")
	}

	os.Setenv("CUSTOM_SOUNDS", `{"98": "Do not come", "99": "I'm gonna come"}`)
	ecovacs.InitSounds()
	if ecovacs.AvailableSounds[ecovacs.Sound(98)] != "Do not come" {
		t.Errorf("Expected sound 98 to be 'Do not come', got '%s'", ecovacs.AvailableSounds[ecovacs.Sound(98)])
	}

	tmpDir := t.TempDir()
	os.Unsetenv("CUSTOM_SOUNDS")
	os.Setenv("CUSTOM_SOUNDS_DIR", tmpDir)

	_ = os.WriteFile(filepath.Join(tmpDir, "95_custom_greeting.ogg"), []byte("dummy"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "96.ogg"), []byte("dummy"), 0644)

	ecovacs.InitSounds()
	if ecovacs.AvailableSounds[ecovacs.Sound(95)] != "custom greeting" {
		t.Errorf("Expected sound 95 to be 'custom greeting', got '%s'", ecovacs.AvailableSounds[ecovacs.Sound(95)])
	}
	if ecovacs.AvailableSounds[ecovacs.Sound(96)] != "Custom Sound 96" {
		t.Errorf("Expected sound 96 to be 'Custom Sound 96', got '%s'", ecovacs.AvailableSounds[ecovacs.Sound(96)])
	}

	os.Unsetenv("CUSTOM_SOUNDS_DIR")
	ecovacs.InitSounds()
}
