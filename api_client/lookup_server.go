package main

import (
	"encoding/json"
	"io"
	"log"
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

func handleLookup(w http.ResponseWriter, r *http.Request) {
	serverIP := getEnv("SERVER_IP", getEnv("CONTROLLER_IP", "192.168.10.178"))
	xmppPortStr := getEnv("XMPP_PORT", "5223")
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
			r.ParseForm()
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
	w.Write([]byte(compactJSON))
}

func startLookupServer() {
	port := getEnv("LOOKUP_PORT", "8007")
	mux := http.NewServeMux()
	mux.HandleFunc("/lookup.do", handleLookup)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	addr := ":" + port
	log.Printf("[LOOKUP] Starting HTTP lookup server on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("[LOOKUP] Server error: %v", err)
	}
}
