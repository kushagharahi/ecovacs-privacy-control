package habridge

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"reflect"
	"testing"

	"github.com/kushagharahi/ecovacs-privacy-control/pkg/ecovacs"
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

func TestGridToImagePremultipliedPathColor(t *testing.T) {
	grid := make([][]byte, 24)
	for x := range grid {
		grid[x] = make([]byte, 24)
		for y := range grid[x] {
			grid[x][y] = 4 // Room A
		}
	}

	// Path running horizontally from x=4 to x=12 at y=10 (with cropMin=(390,390) -> offsetWorld=(500,500))
	pts := []ecovacs.PathPoint{
		{X: 4*50 - 500, Y: 10*50 - 500, Connected: false},
		{X: 12*50 - 500, Y: 10*50 - 500, Connected: true},
	}

	img := GridToImage(grid, 20, 20, 20, 20, 90, nil, pts, 390, 390)
	pngBytes, err := EncodePNGWithValetudoMap(img, NewEmptyValetudoMap("SN123"))
	if err != nil || len(pngBytes) == 0 {
		t.Fatalf("EncodePNGWithValetudoMap failed: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("png.Decode failed: %v", err)
	}

	// Verify path pixel at (8, 10) encodes as sky-blue (128, 216, 255) without RGBA premultiplied overflow
	r, g, b, _ := decoded.At(8, 10).RGBA()
	if (r>>8) != 128 || (g>>8) != 216 || (b>>8) != 255 {
		t.Fatalf("path pixel at (8,10) corrupted: R=%d G=%d B=%d, want 128,216,255", r>>8, g>>8, b>>8)
	}
}

func TestNoMapPlaceholderReplacesSavedMapFallback(t *testing.T) {
	emptyImg := image.NewRGBA(image.Rect(0, 0, 1, 1))
	vMap := ExportImageToValetudoMap(emptyImg, "SN_EMPTY", 10, 10, 10, 10, 90)
	if len(vMap.Layers) != 0 || len(vMap.Entities) != 0 {
		t.Fatalf("expected empty ValetudoMap layers and entities when no map, got layers=%d entities=%d", len(vMap.Layers), len(vMap.Entities))
	}

	pngBytes, err := EncodePNGWithValetudoMap(emptyImg, vMap)
	if err != nil {
		t.Fatalf("EncodePNGWithValetudoMap failed: %v", err)
	}
	decodedPNG, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("png.Decode failed: %v", err)
	}
	if decodedPNG.Bounds().Dx() != ecovacs.NoMapPlaceholderWidth || decodedPNG.Bounds().Dy() != ecovacs.NoMapPlaceholderHeight {
		t.Fatalf("placeholder bounds = %v, want %dx%d", decodedPNG.Bounds(), ecovacs.NoMapPlaceholderWidth, ecovacs.NoMapPlaceholderHeight)
	}
}
