package valetudo

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBuildCameraRoomsPolygonsAndFallback(t *testing.T) {
	// Grid: 6x6 with two rooms (id 0 -> pixel value 4, id 1 -> pixel value 5)
	grid := make([][]byte, 6)
	for x := range grid {
		grid[x] = make([]byte, 6)
	}
	// Room 0 pixels at (0,0), (0,1), (2,1) -> centroid (0, 0) using integer division (2/3=0, 2/3=0)
	grid[0][0] = 4
	grid[0][1] = 4
	grid[2][1] = 4
	// Room 1 pixels at (3,2), (5,4) -> bbox (3,2)..(5,4), centroid (4,3)
	grid[3][2] = 5
	grid[5][4] = 5

	vMap := ExportGridToValetudoMap(
		grid, "SN123", 1, 1, 1, 1, 0,
		[]string{"0", "1"},
		map[string]string{"0": "Living Room"},
		nil, nil, 100, 200,
	)

	roomPolys := map[string][][2]int{
		"0": {{0, 0}, {3, 0}, {3, 2}, {0, 2}},
		// Room "1" omitted -> should fall back to bounding box x0,y0,x1,y1
		// Room "2" has polygon only (no grid pixels yet)
		"2": {{10, 10}, {20, 10}, {20, 20}},
	}

	rooms := BuildCameraRooms(vMap, roomPolys)
	if len(rooms) != 3 {
		t.Fatalf("expected 3 rooms, got %d: %+v", len(rooms), rooms)
	}

	// Room 0 uses custom name + polygon outline + grid pixel centroid
	r0 := rooms["0"]
	if r0.Name != "Living Room" {
		t.Fatalf("room 0 name = %q, want Living Room", r0.Name)
	}
	if !reflect.DeepEqual(r0.Outline, roomPolys["0"]) {
		t.Fatalf("room 0 outline = %v, want %v", r0.Outline, roomPolys["0"])
	}
	if r0.X0 != nil || r0.Y0 != nil || r0.X1 != nil || r0.Y1 != nil {
		t.Fatalf("room 0 bbox fields should be nil when outline is set")
	}

	// Room 1 falls back to default name + x0,y0,x1,y1
	r1 := rooms["1"]
	if r1.Name != "Room B" {
		t.Fatalf("room 1 name = %q, want Room B", r1.Name)
	}
	if len(r1.Outline) != 0 {
		t.Fatalf("room 1 outline = %v, want empty", r1.Outline)
	}
	if r1.X0 == nil || *r1.X0 != 3 || r1.Y0 == nil || *r1.Y0 != 2 || r1.X1 == nil || *r1.X1 != 5 || r1.Y1 == nil || *r1.Y1 != 4 {
		t.Fatalf("room 1 bbox mismatch: %+v", r1)
	}
	if r1.X == nil || *r1.X != 4 || r1.Y == nil || *r1.Y != 3 {
		t.Fatalf("room 1 center mismatch: x=%v y=%v", r1.X, r1.Y)
	}

	// Room 2 uses polygon-only fallback
	r2 := rooms["2"]
	if r2.Name != "Room C" || !reflect.DeepEqual(r2.Outline, roomPolys["2"]) {
		t.Fatalf("room 2 mismatch: %+v", r2)
	}

	// Verify 0 coordinates survive JSON marshaling (not dropped by omitempty)
	grid0 := [][]byte{{5, 5}, {5, 5}}
	vMap0 := ExportGridToValetudoMap(grid0, "SN123", 0, 0, 0, 0, 0, []string{"1"}, nil, nil, nil, 0, 0)
	rooms0 := BuildCameraRooms(vMap0, nil)
	raw, err := json.Marshal(rooms0["1"])
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	for _, key := range []string{"x", "y", "x0", "y0", "x1", "y1"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("expected %q to be preserved when 0, JSON=%s", key, string(raw))
		}
	}
}
