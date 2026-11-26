package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kushagharahi/ecovacs-privacy-control/pkg/habridge"
)

func TestCancelActiveGoToSingleCleanupAndNoOp(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.URL.RequestURI())
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := habridge.NewBridge(habridge.Config{
		ControllerURL: srv.URL,
		RobotSN:       "SN_TEST",
		MQTTBroker:    "tcp://127.0.0.1:1883",
	}, srv.Client())

	// 1. No active GoTo -> zero HTTP calls
	b.CancelActiveGoTo()
	mu.Lock()
	if len(calls) != 0 {
		t.Fatalf("expected 0 calls when no GoTo active, got %v", calls)
	}
	mu.Unlock()

	// 2. Active watcher cancelled -> single stop + silentGoto=0 sequence
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	b.SetActiveGoToForTest(cancel, done)

	go func() {
		defer close(done)
		<-ctx.Done()
	}()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.CancelActiveGoTo()
		}()
	}
	wg.Wait()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not exit after CancelActiveGoTo")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0] != "/clean?act=stop" || calls[1] != "/silentGoto?enable=0" {
		t.Fatalf("unexpected cleanup HTTP calls: %v", calls)
	}
}
