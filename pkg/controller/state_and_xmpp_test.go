package controller

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

	allPts := make([]PathPoint, 450)
	for i := range allPts {
		allPts[i] = PathPoint{X: i * 10, Y: i * 20, Connected: true}
	}

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

func TestHandleLookupLoopbackAndRemoteValidation(t *testing.T) {
	origFlag := serverIPFlag
	origCfg := cfgGlobal
	defer func() {
		serverIPFlag = origFlag
		cfgGlobal = origCfg
	}()

	// 1. Remote caller without explicit SERVER_IP -> rejected when Host is localhost
	serverIPFlag = ""
	req := httptest.NewRequest("POST", "http://127.0.0.1:8007/lookup.do", strings.NewReader(`{"todo":"FindBest","service":"EcoMsgNew"}`))
	req.Host = "127.0.0.1:8007"
	req.RemoteAddr = "192.168.10.224:54321"
	rec := httptest.NewRecorder()
	handleLookup(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("remote caller with loopback Host expected 500, got %d", rec.Code)
	}

	// 2. Local loopback caller (on-robot medusa) -> allowed and compact JSON returned
	reqLocal := httptest.NewRequest("POST", "http://127.0.0.1:8007/lookup.do", strings.NewReader(`{"todo":"FindBest","service":"EcoMsgNew"}`))
	reqLocal.Host = "127.0.0.1:8007"
	reqLocal.RemoteAddr = "127.0.0.1:54321"
	recLocal := httptest.NewRecorder()
	handleLookup(recLocal, reqLocal)
	if recLocal.Code != http.StatusOK {
		t.Fatalf("loopback caller expected 200, got %d (%s)", recLocal.Code, recLocal.Body.String())
	}
	if strings.Contains(recLocal.Body.String(), " ") {
		t.Fatalf("expected compact JSON without spaces, got %q", recLocal.Body.String())
	}
	var resp LookupResponse
	if err := json.Unmarshal(recLocal.Body.Bytes(), &resp); err != nil || resp.IP != "127.0.0.1" || resp.Port != 5223 {
		t.Fatalf("unexpected lookup response: %+v err=%v", resp, err)
	}

	// 3. Explicit serverIPFlag=127.0.0.1 -> allowed
	serverIPFlag = "127.0.0.1"
	recExplicit := httptest.NewRecorder()
	handleLookup(recExplicit, req)
	if recExplicit.Code != http.StatusOK {
		t.Fatalf("explicit 127.0.0.1 expected 200, got %d", recExplicit.Code)
	}
}

func TestLocalSilentGotoAndGoToWithoutTelnet(t *testing.T) {
	tmpDir := t.TempDir()
	origSilent := silentGotoFilePath
	origTarget := gotoTargetFilePath
	origStop := gotoStopFilePath
	origLocal := localRobot
	origRobotIP := robotIPFlag
	silentGotoFilePath = filepath.Join(tmpDir, "silent_goto")
	gotoTargetFilePath = filepath.Join(tmpDir, "goto_target")
	gotoStopFilePath = filepath.Join(tmpDir, "goto_stop")
	localRobot = true
	robotIPFlag = "127.0.0.1"
	defer func() {
		silentGotoFilePath = origSilent
		gotoTargetFilePath = origTarget
		gotoStopFilePath = origStop
		localRobot = origLocal
		robotIPFlag = origRobotIP
	}()

	rec := httptest.NewRecorder()
	handleSilentGoto(rec, httptest.NewRequest("POST", "/silentGoto?enable=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("silentGoto enable=1 failed: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(silentGotoFilePath); err != nil {
		t.Fatalf("expected silent_goto file to exist: %v", err)
	}

	recDisable := httptest.NewRecorder()
	handleSilentGoto(recDisable, httptest.NewRequest("POST", "/silentGoto?enable=0", nil))
	if recDisable.Code != http.StatusOK {
		t.Fatalf("silentGoto enable=0 failed: %d", recDisable.Code)
	}
	if _, err := os.Stat(silentGotoFilePath); !os.IsNotExist(err) {
		t.Fatalf("expected silent_goto file to be removed, err=%v", err)
	}

	recGo := httptest.NewRecorder()
	handleGoTo(recGo, httptest.NewRequest("POST", "/goTo?x=1200&y=-450", nil))
	if recGo.Code != http.StatusOK {
		t.Fatalf("goTo start failed: %d %s", recGo.Code, recGo.Body.String())
	}
	data, err := os.ReadFile(gotoTargetFilePath)
	if err != nil || strings.TrimSpace(string(data)) != "1200 -450" {
		t.Fatalf("unexpected goto_target content %q err=%v", string(data), err)
	}

	recStop := httptest.NewRecorder()
	handleGoTo(recStop, httptest.NewRequest("POST", "/goTo?act=stop", nil))
	if recStop.Code != http.StatusOK {
		t.Fatalf("goTo stop failed: %d", recStop.Code)
	}
	if _, err := os.Stat(gotoStopFilePath); err != nil {
		t.Fatalf("expected goto_stop file to exist: %v", err)
	}
}

func TestGenerateSelfSignedTLSConfig(t *testing.T) {
	cacheDir := t.TempDir()
	tlsCfg, err := generateSelfSignedTLSConfig(cacheDir)
	if err != nil {
		t.Fatalf("generateSelfSignedTLSConfig failed: %v", err)
	}
	if len(tlsCfg.Certificates) != 1 || tlsCfg.MinVersion != tls.VersionTLS10 {
		t.Fatalf("unexpected tls config: certs=%d minVer=%d", len(tlsCfg.Certificates), tlsCfg.MinVersion)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "server.crt")); err != nil {
		t.Fatalf("expected cached server.crt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "server.key")); err != nil {
		t.Fatalf("expected cached server.key: %v", err)
	}
}

func TestDockedChargerPosValidAndSentinelVirtualWallFilter(t *testing.T) {
	statusMu.Lock()
	origState := ChargingState
	origRX, origRY, origRA, origRValid := RobotPosX, RobotPosY, RobotAngle, RobotPoseValid
	origCX, origCY, origCValid := ChargerPosX, ChargerPosY, ChargerPosValid
	ChargingState = "SlotCharging"
	RobotPosX, RobotPosY, RobotAngle, RobotPoseValid = 367, 417, 85, true
	ChargerPosX, ChargerPosY, ChargerPosValid = 0, 0, false
	statusMu.Unlock()

	mapMutex.Lock()
	origInfo := mapInfo
	grid := make([][]byte, 800)
	for i := range grid {
		grid[i] = make([]byte, 800)
	}
	grid[192][242] = 2
	grid[447][572] = 2
	grid[367][417] = 1
	mapInfo = MapInfo{mapGrid: grid}
	mapMutex.Unlock()

	restrictionsLock.Lock()
	origRestr := DiscoveredRestrictions
	DiscoveredRestrictions = nil
	restrictionsLock.Unlock()

	defer func() {
		statusMu.Lock()
		ChargingState = origState
		RobotPosX, RobotPosY, RobotAngle, RobotPoseValid = origRX, origRY, origRA, origRValid
		ChargerPosX, ChargerPosY, ChargerPosValid = origCX, origCY, origCValid
		statusMu.Unlock()

		mapMutex.Lock()
		mapInfo = origInfo
		mapMutex.Unlock()

		restrictionsLock.Lock()
		DiscoveredRestrictions = origRestr
		restrictionsLock.Unlock()
	}()

	// 1. Before ChargerPos arrives (ChargerPosValid == false), docked robot must NOT snap to 0 - minX
	rec := httptest.NewRecorder()
	handleMapGrid(rec, httptest.NewRequest("GET", "/mapGrid", nil))
	var mg MapGridResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &mg); err != nil {
		t.Fatalf("unmarshal mapGrid: %v", err)
	}
	if mg.ChargerPixelX != -1 || mg.ChargerPixelY != -1 {
		t.Fatalf("expected unset charger (-1,-1) before ChargerPos, got (%d,%d)", mg.ChargerPixelX, mg.ChargerPixelY)
	}
	if mg.RobotPixelX != 367-192 || mg.RobotPixelY != 417-242 {
		t.Fatalf("expected robot pixel (175,175), got (%d,%d)", mg.RobotPixelX, mg.RobotPixelY)
	}

	// 2. Once ChargerPos (-1631, 827) arrives, math.Round maps to (367, 417) -> cropped (175, 175)
	_ = handleResponse("ChargerPos", map[string]interface{}{"-p": "-1631,827", "-a": "-85"})
	rec2 := httptest.NewRecorder()
	handleMapGrid(rec2, httptest.NewRequest("GET", "/mapGrid", nil))
	if err := json.Unmarshal(rec2.Body.Bytes(), &mg); err != nil {
		t.Fatalf("unmarshal mapGrid after ChargerPos: %v", err)
	}
	if mg.ChargerPixelX != 175 || mg.ChargerPixelY != 175 || mg.RobotPixelX != 175 || mg.RobotPixelY != 175 {
		t.Fatalf("expected charger & robot at (175,175), got charger=(%d,%d) robot=(%d,%d)",
			mg.ChargerPixelX, mg.ChargerPixelY, mg.RobotPixelX, mg.RobotPixelY)
	}

	// 3. Sentinel virtual wall [-19000,-19000,-18000,-19000] must be filtered out on PullM
	handlePullMResponse(map[string]interface{}{
		"-id": "vw0",
		"-tp": "vw",
		"-m":  "[-19000,-19000,-18000,-19000]",
	})
	restrictionsLock.Lock()
	if len(DiscoveredRestrictions) != 0 {
		restrictionsLock.Unlock()
		t.Fatalf("expected sentinel virtual wall to be filtered, got %+v", DiscoveredRestrictions)
	}
	restrictionsLock.Unlock()
}

func TestMapStNoneClearsCachedMapAndServesNoMapPlaceholder(t *testing.T) {
	mapMutex.Lock()
	origInfo := mapInfo
	mapInfo.mapGrid = [][]byte{{1, 2}, {2, 1}}
	mapMutex.Unlock()
	defer func() {
		mapMutex.Lock()
		mapInfo = origInfo
		mapMutex.Unlock()
	}()

	noneIQ := Stanza{
		XMLName: xml.Name{Local: "iq"},
		Inner:   []byte(`<query xmlns='com:ctl'><ctl id='9999' ret='ok' st='none' info='noMap' method='auto'/></query>`),
	}
	handleIqStanza(&XmppBotSession{}, noneIQ)

	grid, _, _, _, _ := GetMapGridRaw()
	if len(grid) != 0 {
		t.Fatalf("expected empty map grid after MapSt st='none', got len=%d", len(grid))
	}

	rec := httptest.NewRecorder()
	getMapData(rec, httptest.NewRequest("GET", "/getMapData", nil))
	if rec.Header().Get("X-Map-Empty") != "true" {
		t.Fatalf("expected X-Map-Empty=true header when no map is loaded, got %q", rec.Header().Get("X-Map-Empty"))
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected non-empty JPEG placeholder body")
	}
}
