package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/itchio/lzma"
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

func TestHandleMapGridExportsCroppedRoomPolygons(t *testing.T) {
	mapMutex.Lock()
	origGrid := mapInfo.mapGrid
	origMinX, origMinY := MapCropMinX, MapCropMinY
	origMaxX, origMaxY := MapCropMaxX, MapCropMaxY
	mapInfo.mapGrid = make([][]byte, 20)
	for x := range mapInfo.mapGrid {
		mapInfo.mapGrid[x] = make([]byte, 20)
	}
	mapInfo.mapGrid[10][12] = 1
	mapInfo.mapGrid[14][16] = 1
	mapMutex.Unlock()

	spotAreasLock.Lock()
	origPolys := spotAreaPolygons
	spotAreaPolygons = map[int][][2]int{
		0: {{10, 12}, {14, 12}, {14, 16}, {10, 16}},
	}
	spotAreasLock.Unlock()

	defer func() {
		mapMutex.Lock()
		mapInfo.mapGrid = origGrid
		MapCropMinX, MapCropMinY = origMinX, origMinY
		MapCropMaxX, MapCropMaxY = origMaxX, origMaxY
		mapMutex.Unlock()
		spotAreasLock.Lock()
		spotAreaPolygons = origPolys
		spotAreasLock.Unlock()
	}()

	req := httptest.NewRequest("GET", "/mapGrid", nil)
	rec := httptest.NewRecorder()
	handleMapGrid(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleMapGrid status = %d, want 200", rec.Code)
	}

	var resp MapGridResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	want := [][2]int{{0, 0}, {4, 0}, {4, 4}, {0, 4}}
	if !reflect.DeepEqual(resp.RoomPolygons["0"], want) {
		t.Fatalf("room_polygons[0] = %v, want %v", resp.RoomPolygons["0"], want)
	}
}

func encodeTestTracePoints(t *testing.T, pts []PathPoint) string {
	t.Helper()
	var raw bytes.Buffer
	for _, pt := range pts {
		var buf [5]byte
		binary.LittleEndian.PutUint16(buf[0:2], uint16(int16(pt.X/10)))
		binary.LittleEndian.PutUint16(buf[2:4], uint16(int16(pt.Y/10)))
		var flags byte
		if !pt.Connected {
			flags |= 0x80
		}
		flags |= byte(pt.Type & 1)
		buf[4] = flags
		raw.Write(buf[:])
	}
	var compressed bytes.Buffer
	zw := lzma.NewWriter(&compressed)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		t.Fatalf("lzma write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("lzma close: %v", err)
	}
	stdBytes := compressed.Bytes()
	if len(stdBytes) < 13 {
		t.Fatalf("unexpected lzma header len %d", len(stdBytes))
	}
	// Strip bytes [8:12] to produce Ecovacs 9-byte header format
	ecovacsBytes := append([]byte{}, stdBytes[:8]...)
	ecovacsBytes = append(ecovacsBytes, stdBytes[12:]...)
	return base64.StdEncoding.EncodeToString(ecovacsBytes)
}

func TestTraceBackfillRoutingChunkingAndOrdering(t *testing.T) {
	pathLock.Lock()
	origPath := DiscoveredPath
	origTrID := ActiveTrID
	origMap := tracePointsByIdx
	origPending := pendingGetTr
	origSeq := traceReqSeq
	origSender := sendTraceCmd
	resetTraceSessionLocked(0)
	traceReqSeq = 0
	pathLock.Unlock()

	var sentMu sync.Mutex
	var sent []XMLMsg
	sendDone := make(chan struct{}, 10)
	sendTraceCmd = func(msg XMLMsg) {
		sentMu.Lock()
		sent = append(sent, msg)
		sentMu.Unlock()
		sendDone <- struct{}{}
	}

	defer func() {
		pathLock.Lock()
		DiscoveredPath = origPath
		ActiveTrID = origTrID
		tracePointsByIdx = origMap
		pendingGetTr = origPending
		traceReqSeq = origSeq
		sendTraceCmd = origSender
		pathLock.Unlock()
	}()

	// 1. GetTrM reply without td attribute (matching firmware) with count=450 -> 3 chunks
	getTrMIQ := Stanza{
		XMLName: xml.Name{Local: "iq"},
		Inner:   []byte(`<query xmlns='com:ctl'><ctl id='4444' ret='ok' trid='279615' c='450'/></query>`),
	}
	handleIqStanza(&XmppBotSession{}, getTrMIQ)

	for i := 0; i < 3; i++ {
		select {
		case <-sendDone:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for chunk %d", i)
		}
	}

	sentMu.Lock()
	if len(sent) != 3 {
		t.Fatalf("expected 3 chunked GetTr requests, got %d", len(sent))
	}
	wantRanges := [][2]string{{"0", "199"}, {"200", "399"}, {"400", "449"}}
	var reqIDs []string
	for i, msg := range sent {
		if msg.cmdName != "GetTr" {
			t.Fatalf("sent[%d].cmdName = %q, want GetTr", i, msg.cmdName)
		}
		if msg.cmdOpts["-tf"] != wantRanges[i][0] || msg.cmdOpts["-tt"] != wantRanges[i][1] {
			t.Fatalf("sent[%d] range = %v..%v, want %v", i, msg.cmdOpts["-tf"], msg.cmdOpts["-tt"], wantRanges[i])
		}
		reqIDs = append(reqIDs, msg.cmdOpts["-id"].(string))
	}
	sentMu.Unlock()

	// Build 450 distinct points (index i -> X = i * 10)
	allPts := make([]PathPoint, 450)
	for i := range allPts {
		allPts[i] = PathPoint{X: i * 10, Y: i * 20, Connected: true}
	}

	// 2. Deliver GetTr replies OUT OF ORDER without td or trid (matching firmware FUN_0001a728)
	deliverChunk := func(reqID string, pts []PathPoint) {
		trB64 := encodeTestTracePoints(t, pts)
		iq := Stanza{
			XMLName: xml.Name{Local: "iq"},
			Inner:   []byte(`<query xmlns='com:ctl'><ctl id='` + reqID + `' ret='ok' tr='` + trB64 + `'/></query>`),
		}
		handleIqStanza(&XmppBotSession{}, iq)
	}
	deliverChunk(reqIDs[2], allPts[400:450])
	deliverChunk(reqIDs[0], allPts[0:200])
	deliverChunk(reqIDs[1], allPts[200:400])

	pathLock.Lock()
	gotLen := len(DiscoveredPath)
	if gotLen != 450 {
		pathLock.Unlock()
		t.Fatalf("DiscoveredPath len = %d, want 450", gotLen)
	}
	for i := 0; i < 450; i++ {
		if DiscoveredPath[i].X != allPts[i].X || DiscoveredPath[i].Y != allPts[i].Y {
			pathLock.Unlock()
			t.Fatalf("DiscoveredPath[%d] = %+v, want %+v", i, DiscoveredPath[i], allPts[i])
		}
	}
	pathLock.Unlock()

	// 3. Live TrM push c=451 followed by trace tf=450 should NOT trigger redundant GetTr
	sentMu.Lock()
	sent = nil
	sentMu.Unlock()
	liveTrMIQ := Stanza{
		XMLName: xml.Name{Local: "iq"},
		Inner:   []byte(`<query xmlns='com:ctl'><ctl td='TrM' trid='279615' c='451'/></query>`),
	}
	handleIqStanza(&XmppBotSession{}, liveTrMIQ)
	sentMu.Lock()
	if len(sent) != 0 {
		sentMu.Unlock()
		t.Fatalf("expected 0 GetTr calls for normal live TrM tip, got %d", len(sent))
	}
	sentMu.Unlock()

	pt450 := PathPoint{X: 4500, Y: 9000, Connected: true}
	tracePushIQ := Stanza{
		XMLName: xml.Name{Local: "iq"},
		Inner:   []byte(`<query xmlns='com:ctl'><ctl td='trace' trid='279615' tf='450' tt='450' tr='` + encodeTestTracePoints(t, []PathPoint{pt450}) + `'/></query>`),
	}
	handleIqStanza(&XmppBotSession{}, tracePushIQ)

	pathLock.Lock()
	if len(DiscoveredPath) != 451 || DiscoveredPath[450] != pt450 {
		pathLock.Unlock()
		t.Fatalf("expected point 450 appended cleanly, len=%d", len(DiscoveredPath))
	}
	pathLock.Unlock()

	// 4. New clean session (new trid) resets path cleanly and fetches new trace
	newTrMIQ := Stanza{
		XMLName: xml.Name{Local: "iq"},
		Inner:   []byte(`<query xmlns='com:ctl'><ctl td='TrM' trid='279616' c='2'/></query>`),
	}
	handleIqStanza(&XmppBotSession{}, newTrMIQ)
	select {
	case <-sendDone:
	case <-time.After(time.Second):
		t.Fatal("expected GetTr backfill for new trid")
	}
	pathLock.Lock()
	if ActiveTrID != 279616 || len(DiscoveredPath) != 0 {
		pathLock.Unlock()
		t.Fatalf("expected reset for new trid, ActiveTrID=%d len=%d", ActiveTrID, len(DiscoveredPath))
	}
	pathLock.Unlock()
}
