package habridge

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestUniqueStrings(t *testing.T) {
	got := UniqueStrings([]string{"valetudo/SN1", "valetudo/ozmo_930", "valetudo/SN1", "", "valetudo/ozmo_930"})
	want := []string{"valetudo/SN1", "valetudo/ozmo_930"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UniqueStrings = %v, want %v", got, want)
	}
}

func TestWorldCoordsToRobotMM(t *testing.T) {
	b := NewBridge(Config{
		RobotSN:    "SN_TEST",
		MQTTBroker: "tcp://127.0.0.1:1883",
	}, nil)
	b.SetMapViewport(200, 300, 100, 120)

	// 1. Ratio coordinates (<= 1.0)
	ratioPts := b.WorldCoordsToRobotMM([2]float64{0.5, 0.5})
	if ratioPts[0] != [2]int{-7500, -2000} {
		t.Fatalf("ratio conversion = %v, want [-7500 -2000]", ratioPts[0])
	}

	// 2. Pixel coordinates (<= 400)
	pxPts := b.WorldCoordsToRobotMM([2]float64{220, 310})
	if pxPts[0] != [2]int{1000, 10500} {
		t.Fatalf("pixel conversion = %v, want [1000 10500]", pxPts[0])
	}

	// 3. World mm coordinates (> 400)
	mmPts := b.WorldCoordsToRobotMM([2]float64{10500, 5250})
	if mmPts[0] != [2]int{500, 250} {
		t.Fatalf("mm conversion = %v, want [500 250]", mmPts[0])
	}
}

func TestDebounceWithin(t *testing.T) {
	var mu sync.Mutex
	var last time.Time

	if !DebounceWithin(&mu, &last, 50*time.Millisecond) {
		t.Fatal("first call should pass debounce")
	}
	if DebounceWithin(&mu, &last, 50*time.Millisecond) {
		t.Fatal("immediate second call should be debounced")
	}
	time.Sleep(60 * time.Millisecond)
	if !DebounceWithin(&mu, &last, 50*time.Millisecond) {
		t.Fatal("call after window should pass debounce")
	}
}
