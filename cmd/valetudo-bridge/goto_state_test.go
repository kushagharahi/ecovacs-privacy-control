package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
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

	origURL := *controllerURL
	*controllerURL = srv.URL
	defer func() { *controllerURL = origURL }()

	cli := srv.Client()

	// 1. No active GoTo -> zero HTTP calls
	activeGoToLock.Lock()
	activeGoToCancel = nil
	activeGoToDone = nil
	activeGoToLock.Unlock()

	cancelActiveGoTo(cli)
	mu.Lock()
	if len(calls) != 0 {
		t.Fatalf("expected 0 calls when no GoTo active, got %v", calls)
	}
	mu.Unlock()

	// 2. Active watcher cancelled -> exactly one stop + silentGoto=0 sequence
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	activeGoToLock.Lock()
	activeGoToID++
	myID := activeGoToID
	activeGoToCancel = cancel
	activeGoToDone = done
	activeGoToLock.Unlock()

	go func() {
		defer func() {
			activeGoToLock.Lock()
			if activeGoToID == myID {
				activeGoToCancel = nil
				activeGoToDone = nil
			}
			activeGoToLock.Unlock()
			close(done)
		}()
		<-ctx.Done()
	}()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cancelActiveGoTo(cli)
		}()
	}
	wg.Wait()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not exit after cancelActiveGoTo")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0] != "/clean?act=stop" || calls[1] != "/silentGoto?enable=0" {
		t.Fatalf("unexpected cleanup HTTP calls: %v", calls)
	}
}
