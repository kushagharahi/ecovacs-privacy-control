package main

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestUniqueStrings(t *testing.T) {
	got := uniqueStrings([]string{"valetudo/SN1", "ha-bridge/SN1", "valetudo/SN1", "", "ha-bridge/ozmo_930", "ha-bridge/SN1"})
	want := []string{"valetudo/SN1", "ha-bridge/SN1", "ha-bridge/ozmo_930"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uniqueStrings = %v, want %v", got, want)
	}
}

func TestWorldCoordsToRobotMM(t *testing.T) {
	cropOffsetLock.Lock()
	origMinX, origMinY, origW, origH := lastCropMinX, lastCropMinY, lastMapW, lastMapH
	lastCropMinX, lastCropMinY, lastMapW, lastMapH = 200, 300, 100, 120
	cropOffsetLock.Unlock()
	defer func() {
		cropOffsetLock.Lock()
		lastCropMinX, lastCropMinY, lastMapW, lastMapH = origMinX, origMinY, origW, origH
		cropOffsetLock.Unlock()
	}()

	// offsetWorldX = (400 - 200) * 50 = 10000
	// offsetWorldY = (400 - 300) * 50 = 5000

	// 1. Ratio coordinates (<= 1.0)
	ratioPts := worldCoordsToRobotMM([2]float64{0.5, 0.5})
	// x = 0.5*100*50 - 10000 = -7500, y = 0.5*120*50 - 5000 = -2000
	if ratioPts[0] != [2]int{-7500, -2000} {
		t.Fatalf("ratio conversion = %v, want [-7500 -2000]", ratioPts[0])
	}

	// 2. Pixel coordinates (<= 400)
	pxPts := worldCoordsToRobotMM([2]float64{220, 310})
	// x = 220*50 - 10000 = 1000, y = 310*50 - 5000 = 10500
	if pxPts[0] != [2]int{1000, 10500} {
		t.Fatalf("pixel conversion = %v, want [1000 10500]", pxPts[0])
	}

	// 3. World mm coordinates (> 400)
	mmPts := worldCoordsToRobotMM([2]float64{10500, 5250})
	if mmPts[0] != [2]int{500, 250} {
		t.Fatalf("mm conversion = %v, want [500 250]", mmPts[0])
	}
}

func TestDebounceWithin(t *testing.T) {
	var mu sync.Mutex
	var last time.Time

	if !debounceWithin(&mu, &last, 50*time.Millisecond) {
		t.Fatal("first call should pass debounce")
	}
	if debounceWithin(&mu, &last, 50*time.Millisecond) {
		t.Fatal("immediate second call should be debounced")
	}
	time.Sleep(60 * time.Millisecond)
	if !debounceWithin(&mu, &last, 50*time.Millisecond) {
		t.Fatal("call after window should pass debounce")
	}
}
