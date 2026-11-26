package controller

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type LookupRequest struct {
	Todo    string `json:"todo"`
	Service string `json:"service"`
}

type LookupResponse struct {
	Result string `json:"result"`
	IP     string `json:"ip"`
	Port   int    `json:"port"`
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func isLoopbackHost(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		return false
	}
	if strings.EqualFold(h, "localhost") || h == "127.0.0.1" || h == "::1" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return isLoopbackHost(host)
}

func handleLookup(w http.ResponseWriter, r *http.Request) {
	explicitServerIP := serverIPFlag
	if explicitServerIP == "" {
		explicitServerIP = getEnv("SERVER_IP", getEnv("CONTROLLER_IP", ""))
	}
	serverIP := explicitServerIP
	if serverIP == "" {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			serverIP = h
		} else {
			serverIP = host
		}
	}
	// Reject unconfigured loopback responses for remote clients, but allow explicit 127.0.0.1 or on-robot loopback callers.
	if serverIP == "" || (isLoopbackHost(serverIP) && explicitServerIP == "" && !isLoopbackRemote(r.RemoteAddr)) {
		log.Printf("[LOOKUP] Error: SERVER_IP or CONTROLLER_IP is required (set SERVER_IP env var or -server-ip flag)")
		http.Error(w, `{"result":"fail","message":"SERVER_IP environment variable or -server-ip flag is required"}`, http.StatusInternalServerError)
		return
	}
	xmppPortStr := cfgGlobal.XmppPort
	if xmppPortStr == "" {
		xmppPortStr = getEnv("XMPP_PORT", "5223")
	}
	xmppPort, _ := strconv.Atoi(xmppPortStr)
	if xmppPort == 0 {
		xmppPort = 5223
	}

	var req LookupRequest

	bodyBytes, err := io.ReadAll(r.Body)
	if err == nil && len(bodyBytes) > 0 {
		if strings.HasPrefix(strings.TrimSpace(string(bodyBytes)), "{") {
			_ = json.Unmarshal(bodyBytes, &req)
		} else {
			// Form urlencoded fallback
			_ = r.ParseForm()
			req.Todo = r.FormValue("todo")
			req.Service = r.FormValue("service")
		}
	}

	log.Printf("[LOOKUP] Received %s request from %s: todo=%s service=%s", r.Method, r.RemoteAddr, req.Todo, req.Service)

	resp := LookupResponse{
		Result: "ok",
		IP:     serverIP,
		Port:   xmppPort,
	}

	if req.Service == "EcoUpdate" {
		resp.Port = 8005
	}

	// Robot cjp JSON parser is strict: no spaces in JSON output!
	jsonBytes, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, `{"result":"fail"}`, http.StatusInternalServerError)
		return
	}

	compactJSON := strings.ReplaceAll(string(jsonBytes), " ", "")
	log.Printf("[LOOKUP] Responding to %s with: %s", r.RemoteAddr, compactJSON)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(compactJSON))
}

func startLookupServer(ctx context.Context, port string) error {
	if port == "" {
		port = getEnv("LOOKUP_PORT", "8007")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/lookup.do", handleLookup)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	addr := ":" + port
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	srv := &http.Server{Handler: mux}
	log.Printf("[LOOKUP] Starting HTTP lookup server on %s", addr)
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[LOOKUP] Server error: %v", err)
		}
	}()
	return nil
}
