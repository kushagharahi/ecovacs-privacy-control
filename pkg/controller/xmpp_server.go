package controller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	mxj "github.com/clbanning/mxj/v2"
)

type XmppBotSession struct {
	conn       net.Conn
	sn         string
	devClass   string
	resource   string
	jid        string
	isReady    bool
	closed     bool
	writeMu    sync.Mutex
	lastPingID int64
}

var (
	activeXmppBot *XmppBotSession
	botLock       sync.RWMutex
)

func getActiveXmppBot() *XmppBotSession {
	botLock.RLock()
	defer botLock.RUnlock()
	return activeXmppBot
}

func (s *XmppBotSession) IsReady() bool {
	botLock.RLock()
	defer botLock.RUnlock()
	return s != nil && !s.closed && s.isReady
}

func (s *XmppBotSession) RemoteIP() string {
	botLock.RLock()
	defer botLock.RUnlock()
	if s == nil || s.conn == nil {
		return ""
	}
	addr := s.conn.RemoteAddr().String()
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

func (s *XmppBotSession) closeConn() {
	if s == nil {
		return
	}
	s.writeMu.Lock()
	if !s.closed && s.conn != nil {
		_ = s.conn.Close()
		s.closed = true
	}
	s.writeMu.Unlock()

	botLock.Lock()
	if activeXmppBot == s {
		activeXmppBot = nil
	}
	s.isReady = false
	botLock.Unlock()
}

func (s *XmppBotSession) WriteRaw(data string) error {
	if s == nil {
		return fmt.Errorf("nil xmpp session")
	}
	s.writeMu.Lock()
	if s.closed || s.conn == nil {
		s.writeMu.Unlock()
		return fmt.Errorf("connection closed")
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	log.Printf("[XMPP-OUT] %s", data)
	_, err := s.conn.Write([]byte(data))
	if err != nil {
		_ = s.conn.Close()
		s.closed = true
		s.writeMu.Unlock()

		botLock.Lock()
		if activeXmppBot == s {
			activeXmppBot = nil
		}
		s.isReady = false
		botLock.Unlock()
		return err
	}
	s.writeMu.Unlock()
	return nil
}

func (s *XmppBotSession) SendCommand(msg XMLMsg) error {
	if !s.IsReady() {
		return fmt.Errorf("bot is not connected or ready")
	}

	reqID := strconv.FormatInt(time.Now().UnixNano(), 10)
	opts := mxj.Map{}
	for k, v := range msg.cmdOpts {
		opts[k] = v
	}
	opts["-td"] = msg.cmdName
	if _, ok := opts["-id"]; !ok {
		opts["-id"] = reqID[len(reqID)-4:]
	}

	xmlPayload, err := opts.XmlIndent("", " ", "ctl")
	if err != nil {
		return fmt.Errorf("failed to format XML: %w", err)
	}

	stanza := fmt.Sprintf(`<iq type="set" id="%s" to="%s" from="admin@ecouser.net"><query xmlns="com:ctl">%s</query></iq>`,
		reqID, s.jid, string(xmlPayload))

	return s.WriteRaw(stanza)
}

func (s *XmppBotSession) sendBootstrapPermissions() {
	// Add user and Set AC to prevent error 103 (permission denied)
	reqID1 := strconv.FormatInt(time.Now().UnixNano(), 10)
	addUserStanza := fmt.Sprintf(`<iq type="set" id="%s" from="admin@ecouser.net" to="%s"><query xmlns="com:ctl"><ctl td="AddUser" id="0001" jid="admin@ecouser.net"/></query></iq>`, reqID1, s.jid)
	_ = s.WriteRaw(addUserStanza)

	reqID2 := strconv.FormatInt(time.Now().UnixNano()+1, 10)
	setAcStanza := fmt.Sprintf(`<iq type="set" id="%s" from="admin@ecouser.net" to="%s"><query xmlns="com:ctl"><ctl td="SetAC" id="0002" jid="admin@ecouser.net"><acs><ac name="userman" allow="1"/><ac name="setting" allow="1"/><ac name="clean" allow="1"/></acs></ctl></query></iq>`, reqID2, s.jid)
	_ = s.WriteRaw(setAcStanza)

	// Confirm user setup
	reqID3 := strconv.FormatInt(time.Now().UnixNano()+2, 10)
	getUserInfoStanza := fmt.Sprintf(`<iq type="set" id="%s" from="admin@ecouser.net" to="%s"><query xmlns="com:ctl"><ctl td="GetUserInfo" id="4444"/><UserInfos/></query></iq>`, reqID3, s.jid)
	_ = s.WriteRaw(getUserInfoStanza)
}

func (s *XmppBotSession) sendInitialQueries() {
	_ = s.SendCommand(GetVersion)
	_ = s.SendCommand(GetBatteryInfo)
	_ = s.SendCommand(GetChargeState)
	_ = s.SendCommand(GetVolume)
	_ = s.SendCommand(GetWaterPermeability)
	_ = s.SendCommand(GetWaterBoxInfo)
	_ = s.SendCommand(GetSleepStatus)
	_ = s.SendCommand(GetMapM)
	_ = s.SendCommand(GetMapSet)
	_ = s.SendCommand(GetMapVirtualWalls)
	_ = s.SendCommand(IfHaveMap)
	_ = s.SendCommand(GetMapSt)
	_ = s.SendCommand(GetTrM)
	_ = s.SendCommand(SetMapAutoReport(true))
}

func (s *XmppBotSession) startPingLoop(stopChan <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			if !s.IsReady() {
				return
			}
			s.lastPingID++
			pingStanza := fmt.Sprintf(`<iq from='ecouser.net' to='%s' id='ping_%d' type='get'><ping xmlns='urn:xmpp:ping'/></iq>`, s.jid, s.lastPingID)
			if err := s.WriteRaw(pingStanza); err != nil {
				log.Printf("[XMPP-PING] Failed to ping bot (closing dead session): %v", err)
				s.closeConn()
				return
			}
		}
	}
}

func generateSelfSignedTLSConfig(cacheDir string) (*tls.Config, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate RSA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	now := time.Now().Add(-time.Hour)
	notAfter := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	if exp := now.Add(20 * 365 * 24 * time.Hour); exp.After(notAfter) {
		notAfter = exp
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "ecovacs-privacy-control",
			Organization: []string{"ecovacs-privacy-control"},
		},
		NotBefore:             now,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"*-ww.ecouser.net", "localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0755); err == nil {
			_ = os.WriteFile(filepath.Join(cacheDir, "server.crt"), certPEM, 0644)
			_ = os.WriteFile(filepath.Join(cacheDir, "server.key"), keyPEM, 0600)
		}
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load generated keypair: %w", err)
	}
	log.Println("[XMPP-TLS] Generated self-signed RSA-2048 XMPP TLS certificate")
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS10,
	}, nil
}

func getTlsConfig(cfg Config) (*tls.Config, error) {
	certFile := cfg.CertFile
	if certFile == "" {
		certFile = getEnv("CERT_FILE", "")
	}
	keyFile := cfg.KeyFile
	if keyFile == "" {
		keyFile = getEnv("KEY_FILE", "")
	}

	candidatePaths := [][]string{
		{certFile, keyFile},
	}
	if cfg.CertCacheDir != "" {
		candidatePaths = append(candidatePaths, []string{
			filepath.Join(cfg.CertCacheDir, "server.crt"),
			filepath.Join(cfg.CertCacheDir, "server.key"),
		})
	}
	candidatePaths = append(candidatePaths,
		[]string{"server.crt", "server.key"},
		[]string{"/certs/server.crt", "/certs/server.key"},
		[]string{"/mosquitto/config/server.crt", "/mosquitto/config/server.key"},
		[]string{"../server.crt", "../server.key"},
		[]string{"bumper.crt", "bumper.key"},
		[]string{"/certs/bumper.crt", "/certs/bumper.key"},
	)

	for _, pair := range candidatePaths {
		if pair[0] != "" && pair[1] != "" {
			if _, err1 := os.Stat(pair[0]); err1 == nil {
				if _, err2 := os.Stat(pair[1]); err2 == nil {
					cert, err := tls.LoadX509KeyPair(pair[0], pair[1])
					if err == nil {
						log.Printf("[XMPP-TLS] Loaded certificate from %s / %s", pair[0], pair[1])
						return &tls.Config{
							Certificates: []tls.Certificate{cert},
							MinVersion:   tls.VersionTLS10,
						}, nil
					}
					log.Printf("[XMPP-TLS] Failed loading certificate %s / %s: %v", pair[0], pair[1], err)
				}
			}
		}
	}

	return generateSelfSignedTLSConfig(cfg.CertCacheDir)
}

type Stanza struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Inner   []byte     `xml:",innerxml"`
}

func getAttr(attrs []xml.Attr, name string) string {
	for _, a := range attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func handleXmppClient(conn net.Conn, tlsConfig *tls.Config) {
	defer conn.Close()

	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(15 * time.Second)
	}

	remoteAddr := conn.RemoteAddr().String()
	log.Printf("[XMPP] New incoming connection from %s", remoteAddr)

	bot := &XmppBotSession{
		conn:     conn,
		devClass: "115",
		resource: "atom",
	}

	// Phase 1: Plain TCP initial handshake (before STARTTLS)
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		log.Printf("[XMPP] Error reading initial packet: %v", err)
		return
	}

	initialData := string(buf[:n])
	log.Printf("[XMPP-IN] Plain TCP: %s", initialData)

	if strings.Contains(initialData, "to=") {
		re := regexp.MustCompile(`to=["']([^."']+)`)
		if matches := re.FindStringSubmatch(initialData); len(matches) > 1 {
			bot.devClass = matches[1]
		}
	}

	// Acknowledge stream and require STARTTLS
	streamOpen := `<stream:stream xmlns:stream="http://etherx.jabber.org/streams" xmlns="jabber:client" version="1.0" id="1" from="ecouser.net">`
	starttlsFeature := `<stream:features><starttls xmlns="urn:ietf:params:xml:ns:xmpp-tls"><required/></starttls><mechanisms xmlns="urn:ietf:params:xml:ns:xmpp-sasl"><mechanism>PLAIN</mechanism></mechanisms></stream:features>`

	if err := bot.WriteRaw(streamOpen + starttlsFeature); err != nil {
		return
	}

	// Expect <starttls .../>
	n, err = conn.Read(buf)
	if err != nil {
		log.Printf("[XMPP] Error waiting for starttls request: %v", err)
		return
	}

	starttlsReq := string(buf[:n])
	log.Printf("[XMPP-IN] %s", starttlsReq)

	if !strings.Contains(starttlsReq, "starttls") {
		log.Printf("[XMPP] Expected starttls, got: %s", starttlsReq)
		return
	}

	// Send proceed
	if err := bot.WriteRaw(`<proceed xmlns='urn:ietf:params:xml:ns:xmpp-tls'/>`); err != nil {
		return
	}

	// Phase 2: Upgrade connection to TLS
	tlsConn := tls.Server(conn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		log.Printf("[XMPP-TLS] Handshake failed: %v", err)
		return
	}
	bot.writeMu.Lock()
	bot.conn = tlsConn
	_ = tlsConn.SetDeadline(time.Time{})
	bot.writeMu.Unlock()
	log.Printf("[XMPP-TLS] Connection from %s successfully upgraded to TLS!", remoteAddr)

	stopPingChan := make(chan struct{})
	defer close(stopPingChan)

	defer func() {
		bot.closeConn()
		log.Printf("[XMPP] Bot session ended for %s (%s)", bot.sn, remoteAddr)
	}()

	dec := xml.NewDecoder(tlsConn)

	// Inside TLS loop
	for {
		_ = tlsConn.SetReadDeadline(time.Now().Add(90 * time.Second))
		token, err := dec.Token()
		if err != nil {
			if err != io.EOF {
				log.Printf("[XMPP] Decoder token error: %v", err)
			}
			break
		}

		switch se := token.(type) {
		case xml.StartElement:
			log.Printf("[XMPP-TAG] <%s:%s>", se.Name.Space, se.Name.Local)

			switch se.Name.Local {
			case "stream":
				// Stream opened inside TLS
				if !bot.isReady && bot.sn == "" {
					// Need SASL Auth
					resp := `<stream:stream xmlns:stream="http://etherx.jabber.org/streams" xmlns="jabber:client" version="1.0" id="1" from="ecouser.net"><stream:features><mechanisms xmlns="urn:ietf:params:xml:ns:xmpp-sasl"><mechanism>PLAIN</mechanism></mechanisms></stream:features>`
					_ = bot.WriteRaw(resp)
				} else if !bot.isReady && bot.sn != "" {
					// Post-auth stream: offer bind & session
					resp := `<stream:stream xmlns:stream="http://etherx.jabber.org/streams" xmlns="jabber:client" version="1.0" id="1" from="ecouser.net"><stream:features><bind xmlns="urn:ietf:params:xml:ns:xmpp-bind"/><session xmlns="urn:ietf:params:xml:ns:xmpp-session"/></stream:features>`
					_ = bot.WriteRaw(resp)
				}

			case "auth":
				var authStanza Stanza
				if err := dec.DecodeElement(&authStanza, &se); err == nil {
					authPayload := strings.TrimSpace(string(authStanza.Inner))
					decodedBytes, _ := base64.StdEncoding.DecodeString(authPayload)
					decodedStr := string(decodedBytes)
					log.Printf("[XMPP-AUTH] Raw SASL payload: %q", decodedStr)

					// Parse SN
					parts := strings.Split(decodedStr, "\x00")
					if len(parts) > 1 && parts[1] != "" {
						bot.sn = parts[1]
					} else {
						slashParts := strings.Split(decodedStr, "/")
						if len(slashParts) > 0 && slashParts[0] != "" {
							bot.sn = slashParts[0]
						}
					}
					if bot.sn == "" {
						bot.sn = "E0000000000000000000" // Fallback to generic SN
					}

					log.Printf("[XMPP-AUTH] Authenticated bot SN: %s", bot.sn)
					_ = bot.WriteRaw(`<success xmlns="urn:ietf:params:xml:ns:xmpp-sasl"/>`)
				}

			case "iq":
				var iqStanza Stanza
				if err := dec.DecodeElement(&iqStanza, &se); err == nil {
					iqType := getAttr(iqStanza.Attrs, "type")
					iqID := getAttr(iqStanza.Attrs, "id")
					innerXml := string(iqStanza.Inner)
					log.Printf("[XMPP-IQ] Type=%s ID=%s Inner=%s", iqType, iqID, innerXml)

					if strings.Contains(innerXml, "<bind") {
						// Bind resource
						if strings.Contains(innerXml, "<resource>") {
							re := regexp.MustCompile(`<resource>([^<]+)</resource>`)
							if match := re.FindStringSubmatch(innerXml); len(match) > 1 {
								bot.resource = match[1]
							}
						}
						bot.jid = fmt.Sprintf("%s@%s.ecorobot.net/%s", bot.sn, bot.devClass, bot.resource)
						log.Printf("[XMPP-BIND] Bound JID: %s", bot.jid)

						resp := fmt.Sprintf(`<iq type="result" id="%s"><bind xmlns="urn:ietf:params:xml:ns:xmpp-bind"><jid>%s</jid></bind></iq>`, iqID, bot.jid)
						_ = bot.WriteRaw(resp)

					} else if strings.Contains(innerXml, "<session") {
						// Session result
						resp := fmt.Sprintf(`<iq type="result" id="%s"/>`, iqID)
						_ = bot.WriteRaw(resp)

					} else if strings.Contains(innerXml, "<ping") {
						// Ping from bot
						resp := fmt.Sprintf(`<iq type="result" id="%s" from="ecouser.net"/>`, iqID)
						_ = bot.WriteRaw(resp)

					} else {
						// Command / Telemetry IQ
						handleIqStanza(bot, iqStanza)
					}
				}

			case "presence":
				var presStanza Stanza
				if err := dec.DecodeElement(&presStanza, &se); err == nil {
					log.Printf("[XMPP-PRESENCE] %s", string(presStanza.Inner))
					// Send dummy presence
					_ = bot.WriteRaw(fmt.Sprintf(`<presence to="%s"> dummy </presence>`, bot.jid))

					// Robot is fully ready!
					var prevBot *XmppBotSession
					botLock.Lock()
					if activeXmppBot != nil && activeXmppBot != bot {
						prevBot = activeXmppBot
					}
					bot.isReady = true
					activeXmppBot = bot
					botLock.Unlock()
					if prevBot != nil {
						log.Printf("[XMPP] Closing superseded session for %s", prevBot.jid)
						prevBot.closeConn()
					}

					log.Printf("[XMPP] Robot %s is fully READY and online!", bot.jid)

					// Run initial permission bootstrap & info queries
					go func() {
						time.Sleep(200 * time.Millisecond)
						bot.sendBootstrapPermissions()
						time.Sleep(500 * time.Millisecond)
						bot.sendInitialQueries()
					}()

					// Start keep-alive ping loop
					go bot.startPingLoop(stopPingChan)
				}
			}
		}
	}
}

func handleIqStanza(bot *XmppBotSession, iq Stanza) {
	reconstructedXml, err := xml.Marshal(iq)
	if err != nil {
		log.Printf("[XMPP] Error marshaling stanza: %v", err)
		return
	}

	rawString := string(reconstructedXml)

	// Check for Error 103: permission denied
	if strings.Contains(rawString, "errno=\"103\"") || strings.Contains(rawString, "errno='103'") || strings.Contains(rawString, "permission denied") {
		log.Printf("[XMPP] Bot reported permission denied (Error 103). Automatically applying user AC permissions...")
		bot.sendBootstrapPermissions()
		return
	}

	// Parse XML to map using MXJ
	msgMap, err := mxj.NewMapXml(iq.Inner)
	if err != nil {
		// Try parsing from <ctl> directly if inside <query>
		msgMap, err = mxj.NewMapXml(reconstructedXml)
		if err != nil {
			log.Printf("[XMPP] Failed to parse XML into map: %v", err)
			return
		}
	}

	// Extract command name (td)
	cmdName := ""
	if ctlVal, ok := msgMap["ctl"]; ok {
		if ctlMap, ok := ctlVal.(map[string]interface{}); ok {
			if td, ok := ctlMap["-td"].(string); ok {
				cmdName = td
			}
		}
	} else if queryVal, ok := msgMap["query"]; ok {
		if qMap, ok := queryVal.(map[string]interface{}); ok {
			if ctlVal, ok := qMap["ctl"]; ok {
				if ctlMap, ok := ctlVal.(map[string]interface{}); ok {
					if td, ok := ctlMap["-td"].(string); ok {
						cmdName = td
					}
				}
			}
		}
	}

	if cmdName == "" {
		// Check regex fallback
		re := regexp.MustCompile(`td=["']([a-zA-Z0-9_]+)["']`)
		if matches := re.FindStringSubmatch(rawString); len(matches) > 1 {
			cmdName = matches[1]
		}
	}

	if cmdName == "" {
		// Fallback by checking child elements or attributes of <ctl>
		if strings.Contains(rawString, "<volume") {
			cmdName = "GetVolume"
		} else if strings.Contains(rawString, "<ver") {
			cmdName = "GetVersion"
		} else if strings.Contains(rawString, "<battery") {
			cmdName = "BatteryInfo"
		} else if strings.Contains(rawString, "<charge") {
			cmdName = "ChargeState"
		} else if strings.Contains(rawString, "<clean") {
			cmdName = "CleanReport"
		} else if strings.Contains(rawString, " r='") && strings.Contains(rawString, " c='") && strings.Contains(rawString, " m='") {
			cmdName = "MapM"
		} else if strings.Contains(rawString, "tp='sa'") || strings.Contains(rawString, "tp=\"sa\"") || strings.Contains(rawString, "tp='vw'") || strings.Contains(rawString, "tp=\"vw\"") {
			cmdName = "MapSet"
		} else if strings.Contains(rawString, "st='built'") || strings.Contains(rawString, "st=\"built\"") || strings.Contains(rawString, "st='building'") || strings.Contains(rawString, "st=\"building\"") || strings.Contains(rawString, "st='none'") || strings.Contains(rawString, "st=\"none\"") {
			cmdName = "MapSt"
		} else if strings.Contains(rawString, "t='p'") || strings.Contains(rawString, "t=\"p\"") {
			cmdName = "Pos"
		} else if (strings.Contains(rawString, " p='") || strings.Contains(rawString, " p=\"")) && (strings.Contains(rawString, " a='") || strings.Contains(rawString, " a=\"")) {
			if strings.Contains(rawString, "t='c'") || strings.Contains(rawString, "t=\"c\"") {
				cmdName = "ChargerPos"
			} else if strings.Contains(rawString, "valid=") {
				cmdName = "Pos"
			} else {
				cmdName = "ChargerPos"
			}
		} else if strings.Contains(rawString, "type='Brush'") || strings.Contains(rawString, "type='SideBrush'") || strings.Contains(rawString, "type='DustCaseHeap'") {
			cmdName = "GetLifeSpan"
		} else if (strings.Contains(rawString, " v='") || strings.Contains(rawString, " v=\"")) && !strings.Contains(rawString, "<volume") && !strings.Contains(rawString, "<battery") && !strings.Contains(rawString, "<charge") && !strings.Contains(rawString, "<clean") && !strings.Contains(rawString, "td=") {
			cmdName = "GetWaterPermeability"
		} else if strings.Contains(rawString, " tr='") || strings.Contains(rawString, " tr=\"") {
			cmdName = "GetTr"
		} else if strings.Contains(rawString, "trid") && (strings.Contains(rawString, "GetTrM") || strings.Contains(rawString, "TrM") || strings.Contains(rawString, " c='") || strings.Contains(rawString, " c=\"")) {
			cmdName = "GetTrM"
		} else if strings.Contains(rawString, " p='") && len(rawString) > 200 {
			// Base64 LZMA map piece payload
			cmdName = "PullMP"
		} else if strings.Contains(rawString, "id='onoff_") || strings.Contains(rawString, "id=\"onoff_") {
			cmdName = "GetOnOff"
		} else if strings.Contains(rawString, "id='cleansum") || strings.Contains(rawString, "id=\"cleansum") || ((strings.Contains(rawString, " a='") || strings.Contains(rawString, " a=\"")) && strings.Contains(rawString, " l='") && strings.Contains(rawString, " c='")) {
			cmdName = "GetCleanSum"
		} else if (strings.Contains(rawString, " m='") || strings.Contains(rawString, " m=\"")) && (strings.Contains(rawString, ";") || strings.Contains(rawString, "[")) {
			// Room/Spot Area polygon or virtual wall/no-go coordinates response
			cmdName = "PullM"
		}
	}

	if cmdName != "" {
		log.Printf("[XMPP-ROUTER] Routing command: %s", cmdName)
		if err := handleResponse(cmdName, msgMap); err != nil {
			log.Printf("[XMPP-ROUTER] handleResponse error for %s: %v", cmdName, err)
		}
	} else {
		log.Printf("[XMPP-ROUTER] Could not extract td from stanza: %s", rawString)
	}
}

func startXmppServer(ctx context.Context, cfg Config) error {
	port := cfg.XmppPort
	if port == "" {
		port = getEnv("XMPP_PORT", "5223")
	}
	addr := ":" + port

	tlsConfig, err := getTlsConfig(cfg)
	if err != nil {
		return fmt.Errorf("prepare TLS config: %w", err)
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	log.Printf("[XMPP] TLS XMPP server listening on %s", addr)

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					log.Printf("[XMPP] Accept error: %v", err)
					continue
				}
			}
			go handleXmppClient(conn, tlsConfig)
		}
	}()
	return nil
}
