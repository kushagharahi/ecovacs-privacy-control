package main

import (
	"net"
	"testing"
	"time"
)

func TestCleanReportAndChargeStateCollision(t *testing.T) {
	statusMu.Lock()
	origState := ChargingState
	origSpeed := CleanSpeed
	origBoost := CarpetBoost
	ChargingState = "Unknown"
	CleanSpeed = "Unknown"
	CarpetBoost = true
	statusMu.Unlock()
	defer func() {
		statusMu.Lock()
		ChargingState = origState
		CleanSpeed = origSpeed
		CarpetBoost = origBoost
		statusMu.Unlock()
	}()

	// 1. Firmware CleanReport/GetCleanState emits st="s" when cleaning is active
	err := handleResponse("CleanReport", map[string]interface{}{
		"ctl": map[string]interface{}{
			"clean": map[string]interface{}{
				"-st":    "s",
				"-speed": "standard",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleResponse(CleanReport s) failed: %v", err)
	}
	if ChargingState != "Cleaning" {
		t.Fatalf("ChargingState after st='s' = %q, want 'Cleaning'", ChargingState)
	}

	// 2. GetChargeState type="Idle" must NOT clobber active Cleaning state
	err = handleResponse("ChargeState", map[string]interface{}{
		"ctl": map[string]interface{}{
			"charge": map[string]interface{}{
				"-type": "Idle",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleResponse(ChargeState Idle) failed: %v", err)
	}
	if ChargingState != "Cleaning" {
		t.Fatalf("ChargeState Idle clobbered Cleaning -> %q", ChargingState)
	}

	// 3. CleanReport st="h" when off dock transitions Cleaning -> Idle
	err = handleResponse("CleanReport", map[string]interface{}{
		"ctl": map[string]interface{}{
			"clean": map[string]interface{}{
				"-st": "h",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleResponse(CleanReport h) failed: %v", err)
	}
	if ChargingState != "Idle" {
		t.Fatalf("ChargingState after st='h' off dock = %q, want 'Idle'", ChargingState)
	}

	// 4. Going (returning to dock) must NOT be clobbered by CleanReport st="h", and transitions back cleanly
	_ = handleResponse("ChargeState", map[string]interface{}{
		"ctl": map[string]interface{}{
			"charge": map[string]interface{}{
				"-type": "Going",
			},
		},
	})
	_ = handleResponse("CleanReport", map[string]interface{}{
		"ctl": map[string]interface{}{
			"clean": map[string]interface{}{
				"-st": "h",
			},
		},
	})
	if ChargingState != "Going" {
		t.Fatalf("CleanReport st='h' clobbered Going -> %q", ChargingState)
	}
	_ = handleResponse("ChargeState", map[string]interface{}{
		"ctl": map[string]interface{}{
			"charge": map[string]interface{}{
				"-type": "Idle",
			},
		},
	})
	if ChargingState != "Idle" {
		t.Fatalf("ChargingState after cancelled Going ChargeState Idle = %q, want 'Idle'", ChargingState)
	}

	// 5. SlotCharging takes effect immediately and is not overwritten by st="h"
	_ = handleResponse("ChargeState", map[string]interface{}{
		"ctl": map[string]interface{}{
			"charge": map[string]interface{}{
				"-type": "SlotCharging",
			},
		},
	})
	_ = handleResponse("CleanReport", map[string]interface{}{
		"ctl": map[string]interface{}{
			"clean": map[string]interface{}{
				"-st": "h",
			},
		},
	})
	if ChargingState != "SlotCharging" {
		t.Fatalf("ChargingState after SlotCharging + st='h' = %q, want 'SlotCharging'", ChargingState)
	}

	// 6. Unknown/untyped GetOnOff must NOT overwrite CarpetBoost
	_ = handleResponse("GetOnOff", map[string]interface{}{
		"ctl": map[string]interface{}{
			"-on": "0",
		},
	})
	if !CarpetBoost {
		t.Fatalf("Untyped GetOnOff unexpectedly overwrote CarpetBoost to false")
	}
}

func TestXmppWriteRawDeadSocketDoesNotDeadlock(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c2.Close()

	bot := &XmppBotSession{
		conn:    c1,
		isReady: true,
	}
	botLock.Lock()
	origBot := activeXmppBot
	activeXmppBot = bot
	botLock.Unlock()
	defer func() {
		botLock.Lock()
		activeXmppBot = origBot
		botLock.Unlock()
	}()

	// Peer stops reading on unbuffered pipe -> SetWriteDeadline must fail and unblock cleanly
	done := make(chan error, 1)
	go func() {
		done <- bot.WriteRaw("<iq/>")
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected deadline timeout error from stalled peer, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WriteRaw deadlocked instead of honoring 3s write deadline")
	}

	if bot.IsReady() {
		t.Fatal("expected closed session after WriteRaw timeout error")
	}
	if getActiveXmppBot() == bot {
		t.Fatal("expected activeXmppBot to be cleared after WriteRaw error")
	}
}
