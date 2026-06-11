package peerwire

import (
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
	"wesync/internal/certid"
	"wesync/internal/ratelimit"
	"wesync/internal/sysinfo"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	// Peer-to-peer connections come from other WeSync instances, not browsers,
	// so Origin is typically empty. Block browser-originated cross-site requests.
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		return origin == "" // reject any browser-originated connection
	},
}

// Hub manages all outbound peer WebSocket connections and accepts inbound ones.
type Hub struct {
	mu                sync.RWMutex
	selfID            string
	selfPort          int
	selfName          string
	selfSTPort        int
	selfInfo          sysinfo.DeviceInfo
	tlsCert           *tls.Certificate     // nil = no TLS (tests)
	selfCertFP        string               // hex SHA-256 of our TLS cert, empty when no TLS
	conns             map[string]*peerConn // deviceID → outbound connection
	pendingOffers     map[string][]Message // deviceID → messages to deliver on next connect
	cb                Callbacks
	getOutgoing       func() []string
	isTrusted         func(deviceID string) bool // nil = never trusted
	isMutuallyTrusted func(deviceID string) bool // true = mutual trust confirmed this session
	isRemoved         func(deviceID string) bool // true = was explicitly removed, re-notify with trusted:false
	acceptFilter      func(deviceID string) bool // nil = accept all
	// connLimiter caps inbound WS connections per IP to block connection-flooding.
	connLimiter *ratelimit.Limiter

	// accepting gates inbound connections. When false (app hidden/background) we
	// reject new inbound and close existing ones, so the wire goes fully silent —
	// not just our outbound side. Default true.
	accepting atomic.Bool
	// inbound tracks live inbound connections so SetAccepting(false) can close
	// them immediately. Guarded by mu.
	inbound map[*websocket.Conn]struct{}
}

func NewHub(selfID, selfName string, selfPort, selfSTPort int, selfInfo sysinfo.DeviceInfo, tlsCert *tls.Certificate, cb Callbacks, getOutgoing func() []string, isTrusted func(deviceID string) bool, isRemoved func(deviceID string) bool, isMutuallyTrusted func(deviceID string) bool) *Hub {
	var selfCertFP string
	if tlsCert != nil && len(tlsCert.Certificate) > 0 {
		h := certid.CertHash(tlsCert.Certificate[0])
		selfCertFP = hex.EncodeToString(h[:])
	}
	h := &Hub{
		selfID:            selfID,
		selfPort:          selfPort,
		selfName:          selfName,
		selfSTPort:        selfSTPort,
		selfInfo:          selfInfo,
		tlsCert:           tlsCert,
		selfCertFP:        selfCertFP,
		conns:             make(map[string]*peerConn),
		pendingOffers:     make(map[string][]Message),
		cb:                cb,
		getOutgoing:       getOutgoing,
		isTrusted:         isTrusted,
		isMutuallyTrusted: isMutuallyTrusted,
		isRemoved:         isRemoved,
		// 10 new inbound WS connections per IP per minute.
		connLimiter: ratelimit.New(10, time.Minute),
		inbound:     make(map[*websocket.Conn]struct{}),
	}
	h.accepting.Store(true)
	return h
}

// SetAccepting gates inbound peerwire connections. Pass false (app hidden) to
// reject new inbound and immediately close every live inbound connection, so a
// peer can't keep us connected against our will and instead sees the link drop
// at once. Pass true (app foreground) to accept again.
func (h *Hub) SetAccepting(v bool) {
	h.accepting.Store(v)
	if v {
		return
	}
	h.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(h.inbound))
	for c := range h.inbound {
		conns = append(conns, c)
	}
	h.inbound = make(map[*websocket.Conn]struct{})
	h.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

// QueueOffer stores a message to be delivered to deviceID on the next
// successful connection. Deduplicates by FolderID — safe to call multiple times.
func (h *Hub) QueueOffer(deviceID string, msg Message) {
	h.mu.Lock()
	existing := h.pendingOffers[deviceID]
	for i, m := range existing {
		if m.FolderID == msg.FolderID {
			existing[i] = msg // update in place
			h.pendingOffers[deviceID] = existing
			h.mu.Unlock()
			return
		}
	}
	h.pendingOffers[deviceID] = append(existing, msg)
	h.mu.Unlock()
}

// DrainOffers returns and removes all pending messages for deviceID.
func (h *Hub) DrainOffers(deviceID string) []Message {
	h.mu.Lock()
	msgs := h.pendingOffers[deviceID]
	delete(h.pendingOffers, deviceID)
	h.mu.Unlock()
	return msgs
}

// PendingFolderIDs returns the folderIDs of all queued offers for a device.
func (h *Hub) PendingFolderIDs(deviceID string) []string {
	h.mu.RLock()
	msgs := h.pendingOffers[deviceID]
	h.mu.RUnlock()
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.FolderID != "" {
			ids = append(ids, m.FolderID)
		}
	}
	return ids
}

// CancelOffer removes a specific folderID offer queued for a device.
func (h *Hub) CancelOffer(deviceID, folderID string) {
	h.mu.Lock()
	msgs := h.pendingOffers[deviceID]
	kept := msgs[:0]
	for _, m := range msgs {
		if m.FolderID != folderID {
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 {
		delete(h.pendingOffers, deviceID)
	} else {
		h.pendingOffers[deviceID] = kept
	}
	h.mu.Unlock()
}

// Connect establishes (or reconnects) an outbound WS to a peer.
// Safe to call repeatedly. If a connection is already active, it is never
// interrupted — a live connection takes priority over a new addr/port hint.
// A new connection is only opened when there is no existing connection or
// the existing one is down (e.g. still in reconnect backoff).
func (h *Hub) Connect(deviceID, addr string, port int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.conns[deviceID]; ok {
		if c.connected.Load() {
			return // already live — don't interrupt with a potentially worse addr
		}
		if c.addr == addr && c.port == port {
			c.nudge() // same target in backoff — fresh hello, retry now
			return
		}
		c.close()
	}
	c := newPeerConn(deviceID, addr, port, h)
	h.conns[deviceID] = c
	go c.run()
}

// ConnectBySID opens an outbound connection to addr:port, keyed by the peer's
// ephemeral session ID. The connection migrates to device ID once cert is verified.
// Returns true when a new connection goroutine was started, false when one already exists.
func (h *Hub) ConnectBySID(sid, addr string, port int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Already have a connection to this endpoint (live or reconnecting).
	if c, ok := h.conns[sid]; ok && c.connected.Load() {
		return false
	}
	for _, c := range h.conns {
		if c.addr == addr && c.port == port {
			if !c.connected.Load() {
				// In reconnect backoff — this fresh announce proves the peer is
				// back, so wake it to retry now instead of waiting out the backoff.
				c.nudge()
			}
			return false
		}
	}
	if c, ok := h.conns[sid]; ok {
		c.close()
	}
	c := newPeerConn(sid, addr, port, h)
	h.conns[sid] = c
	go c.run()
	return true
}

// DropUntrustedSID closes the wire connection for a SID if the connected device
// is not trusted. Called when a peer stops announcing via UDP.
func (h *Hub) DropUntrustedSID(sid string, isTrusted func(string) bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.conns[sid]
	if !ok {
		return // already adopted to deviceID key or gone
	}
	// Get device ID if known.
	c.mu.Lock()
	devID := c.remoteDeviceID
	if devID == "" {
		devID = c.deviceID // might still be the SID itself
	}
	c.mu.Unlock()
	if devID != "" && devID != sid && isTrusted(devID) {
		return // trusted — keep connected
	}
	c.close()
	delete(h.conns, sid)
}

// ConnectTo opens an outbound connection to addr:port without a known device ID.
// Kept for backward compat; prefer ConnectBySID when SID is available.
func (h *Hub) ConnectTo(addr string, port int) {
	h.ConnectBySID(fmt.Sprintf("%s:%d", addr, port), addr, port)
}

// adoptConn migrates a connection from its temporary key to the real device ID.
func (h *Hub) adoptConn(oldKey, deviceID string) {
	if oldKey == deviceID || oldKey == "" || deviceID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.conns[oldKey]
	if !ok {
		return
	}
	if existing, exists := h.conns[deviceID]; exists && existing.connected.Load() {
		c.close()
		delete(h.conns, oldKey)
		return
	}
	delete(h.conns, oldKey)
	c.setID(deviceID)
	h.conns[deviceID] = c
}

// adoptConnByAddr finds an outbound connection to addr (any port) and migrates
// it to deviceID. Used by readInbound to resolve ConnectTo connections.
func (h *Hub) adoptConnByAddr(addr, deviceID string) {
	if addr == "" || deviceID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// If already keyed by deviceID, nothing to do.
	if _, exists := h.conns[deviceID]; exists {
		return
	}
	for key, c := range h.conns {
		if c.addr != addr {
			continue
		}
		// Skip connections that are already identified as a different device.
		c.mu.Lock()
		remID := c.remoteDeviceID
		c.mu.Unlock()
		if remID != "" && remID != deviceID {
			continue
		}
		delete(h.conns, key)
		c.setID(deviceID)
		h.conns[deviceID] = c
		return
	}
}

// PeerAddr returns the address of an outbound connection and whether it is
// currently live. ok is false when the conn is in the reconnect backoff.
// Also checks connections that were started without a known deviceID (addr:port key).
func (h *Hub) PeerAddr(deviceID string) (addr string, port int, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if c, exists := h.conns[deviceID]; exists {
		return c.addr, c.port, c.connected.Load()
	}
	// Also search connections keyed by addr:port where remoteDeviceID is now known.
	for _, c := range h.conns {
		c.mu.Lock()
		remID := c.remoteDeviceID
		c.mu.Unlock()
		if remID == deviceID {
			return c.addr, c.port, c.connected.Load()
		}
	}
	return "", 0, false
}

// SelfPort returns the port this hub's peer server listens on.
func (h *Hub) SelfPort() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.selfPort
}

// SetSelfName updates the name broadcast in future peer-state messages.
func (h *Hub) SetSelfName(name string) {
	h.mu.Lock()
	h.selfName = name
	h.mu.Unlock()
}

// BroadcastPeerState sends the current name + sysinfo to every connected peer.
// Safe to call for all peers — contains no folder/secret state.
func (h *Hub) BroadcastPeerState() {
	h.mu.RLock()
	info := h.selfInfo
	msg := Message{
		Type:     PeerState,
		DeviceID: h.selfID,
		Name:     h.selfName,
		Info:     &info,
	}
	conns := make([]*peerConn, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
		c.send(msg)
	}
}

// Disconnect closes and removes the outbound connection to a peer.
func (h *Hub) Disconnect(deviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.conns[deviceID]; ok {
		c.close()
		delete(h.conns, deviceID)
	}
}

// DisconnectAll closes every outbound peerwire connection.
func (h *Hub) DisconnectAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, c := range h.conns {
		c.close()
		delete(h.conns, id)
		log.Printf("peerwire: disconnected %s (discovery off)", shortKey(id))
	}
}

// DisconnectExcept closes outbound connections to devices NOT in keepIDs.
// Used when discovery is disabled to keep connections to paired devices alive.
func (h *Hub) DisconnectExcept(keepIDs map[string]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, c := range h.conns {
		if !keepIDs[id] {
			c.close()
			delete(h.conns, id)
			log.Printf("peerwire: disconnected %s (not paired, discovery off)", shortKey(id))
		}
	}
}

// SetAcceptFilter sets a function that gates inbound connections.
// If f returns false for a deviceID, that inbound connection is closed after
// the first hello. Pass nil to accept all (discovery enabled).
func (h *Hub) SetAcceptFilter(f func(deviceID string) bool) {
	h.mu.Lock()
	h.acceptFilter = f
	h.mu.Unlock()
}

// SendSync delivers a message to a peer synchronously, waiting up to timeout
// for the connection to be established and the write to complete.
// Used for time-sensitive notifications (accepted, cancelled) where the caller
// needs to know the message was delivered before proceeding.
func (h *Hub) SendSync(deviceID string, msg Message, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		h.mu.RLock()
		c, ok := h.conns[deviceID]
		h.mu.RUnlock()
		if !ok {
			return fmt.Errorf("peerwire: no connection to %s", shortKey(deviceID))
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("peerwire: timeout sending to %s", shortKey(deviceID))
		}
		if err := c.sendSync(msg, remaining); err == nil {
			return nil
		}
		// Not connected yet — wait briefly before retrying. Cap the wait at the
		// remaining budget so we never sleep past the deadline; the loop top
		// returns the timeout error once it elapses.
		wait := 10 * time.Millisecond
		if remaining < wait {
			wait = remaining
		}
		time.Sleep(wait)
	}
}

// SendHelloTo sends Hello synchronously to a specific peer, waiting up to timeout.
// Use this when the caller needs to ensure the target receives the updated outgoing
// list before the next action (e.g., pair request on the other side).
func (h *Hub) SendHelloTo(deviceID string, timeout time.Duration) error {
	h.mu.RLock()
	info := h.selfInfo
	var trusted *bool
	if h.isTrusted != nil && h.isTrusted(deviceID) {
		t := true
		trusted = &t
	} else {
		f := false
		trusted = &f
	}
	helloDeviceID := ""
	if h.selfCertFP == "" {
		helloDeviceID = h.selfID
	}
	msg := Message{
		Type:     Hello,
		DeviceID: helloDeviceID,
		Port:     h.selfPort,
		STPort:   h.selfSTPort,
		CertFP:   h.selfCertFP,
		Name:     h.selfName,
		Info:     &info,
		Trusted:  trusted,
	}
	h.mu.RUnlock()
	return h.SendSync(deviceID, msg, timeout)
}

// BroadcastHello re-sends a hello to every connected peer with the current
// outgoing set. Call this whenever the outgoing (pairing) list changes.
// Hello carries only identity/pairing fields — name/info travel via BroadcastPeerState.
func (h *Hub) BroadcastHello() {
	h.mu.RLock()
	info := h.selfInfo
	helloDeviceID := ""
	if h.selfCertFP == "" {
		helloDeviceID = h.selfID
	}
	msg := Message{
		Type:     Hello,
		DeviceID: helloDeviceID,
		Port:     h.selfPort,
		STPort:   h.selfSTPort,
		CertFP:   h.selfCertFP,
		Name:     h.selfName,
		Info:     &info,
	}
	conns := make([]*peerConn, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
		c.send(msg)
	}
}

// ServeWS is an http.Handler for inbound peer WebSocket connections.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	if !h.accepting.Load() {
		// App hidden/background — stay silent on the wire.
		http.Error(w, "not accepting", http.StatusServiceUnavailable)
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !h.connLimiter.Allow(ip) {
		log.Printf("peerwire: rate limit — rejected connection flood from %s", ip)
		http.Error(w, "too many connections", http.StatusTooManyRequests)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	go h.readInbound(conn, r.TLS)
}

func (h *Hub) readInbound(conn *websocket.Conn, tlsState *tls.ConnectionState) {
	defer conn.Close()

	// Track this inbound connection so SetAccepting(false) can close it at once.
	h.mu.Lock()
	h.inbound[conn] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.inbound, conn)
		h.mu.Unlock()
	}()

	conn.SetReadLimit(maxMessageSize)
	// Expect pong within one full ping cycle; pong handler resets this.
	conn.SetReadDeadline(time.Now().Add(pingInterval + pongDeadline))
	// Ping frames from the outbound side bypass ReadMessage — reset the deadline
	// here so the inbound connection stays alive during quiet periods.
	conn.SetPingHandler(func(_ string) error {
		conn.SetReadDeadline(time.Now().Add(pingInterval + pongDeadline))
		return conn.WriteControl(websocket.PongMessage, []byte{}, time.Now().Add(writeTimeout))
	})
	conn.SetPongHandler(func(_ string) error {
		conn.SetReadDeadline(time.Now().Add(pingInterval + pongDeadline))
		return nil
	})

	remoteIP := remoteIPOf(conn)

	// Extract cert fingerprint and derive device ID from peer's TLS cert.
	peerCertFP := ""
	peerDeviceID := ""
	if tlsState != nil && len(tlsState.PeerCertificates) > 0 {
		raw := tlsState.PeerCertificates[0].Raw
		fp := certid.CertHash(raw)
		peerCertFP = hex.EncodeToString(fp[:])
		peerDeviceID = certid.DeviceIDFromCert(raw) // authoritative — never from Hello claim
		log.Printf("peerwire [inbound %s]: TLS cert received, fingerprint %.16s…", remoteIP, peerCertFP)
	} else if tlsState != nil {
		// TLS is active but the peer presented no client cert. With
		// RequireAnyClientCert on the server this is unreachable (the handshake
		// fails first), but reject defensively so the Hello-claimed-identity
		// fallback below can NEVER run in a TLS deployment — it exists only for
		// the no-TLS dev/test mode (tlsState == nil) handled by the else branch.
		log.Printf("peerwire [inbound %s]: REJECTED — TLS active but peer sent no certificate", remoteIP)
		return
	} else {
		log.Printf("peerwire [inbound %s]: no TLS — skipping cert verification", remoteIP)
	}

	verified := false
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		// Reset deadline on every received frame, not just pongs.
		conn.SetReadDeadline(time.Now().Add(pingInterval + pongDeadline))

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("peerwire [inbound %s]: malformed message — %v", remoteIP, err)
			continue
		}
		// Override DeviceID with cert-derived identity on every message.
		// If no cert (no-TLS dev/test mode), fall back to the claimed ID from Hello.
		if peerDeviceID != "" {
			msg.DeviceID = peerDeviceID
		}
		if msg.Type == Hello && !verified {
			id := peerDeviceID
			if id == "" {
				id = msg.DeviceID
			}
			id = shortKey(id)
			if peerCertFP != "" {
				log.Printf("peerwire [inbound %s]: hello from %s", remoteIP, id)
				// If CertFP is included in Hello, verify it matches TLS (backward compat check).
				if msg.CertFP != "" && msg.CertFP != peerCertFP {
					log.Printf("peerwire [inbound %s]: REJECTED — certFP mismatch (TLS=%.16s… hello=%.16s…)", remoteIP, peerCertFP, msg.CertFP)
					return
				}
				// Cert-pinning: reject if this device previously connected with a different fingerprint.
				if h.cb.OnValidateCertFP != nil && !h.cb.OnValidateCertFP(msg.DeviceID, peerCertFP) {
					log.Printf("peerwire [inbound %s]: REJECTED %s — cert fingerprint changed", remoteIP, id)
					return
				}
				log.Printf("peerwire [inbound %s]: cert verified OK for %s", remoteIP, id)
			} else {
				log.Printf("peerwire [inbound %s]: no TLS — hello from %s (no cert)", remoteIP, id)
			}
			verified = true

			// Check the accept filter FIRST — rejects unpaired devices when
			// discovery is off. This must run before adopt/OnPeerVerified so a
			// rejected peer leaves no connection-state side effects behind.
			h.mu.RLock()
			filter := h.acceptFilter
			h.mu.RUnlock()
			if filter != nil && !filter(msg.DeviceID) {
				log.Printf("peerwire [inbound %s]: rejected %s — discovery off, device not paired", remoteIP, id)
				return
			}

			// Adopt any outbound connection to this peer's address under the real device ID.
			// This handles ConnectTo connections (temp key = "addr:port").
			if msg.DeviceID != "" {
				h.adoptConnByAddr(remoteIP, msg.DeviceID)
			}

			// Notify handlers that this peer's identity has been TLS-confirmed.
			if h.cb.OnPeerVerified != nil {
				h.cb.OnPeerVerified(msg.DeviceID, peerCertFP)
			}
		}
		h.dispatch(msg, remoteIP)
	}
}

func (h *Hub) dispatch(msg Message, remoteIP string) {
	switch msg.Type {
	case Hello:
		if h.cb.OnHello != nil {
			h.cb.OnHello(msg.DeviceID, remoteIP, msg.Port, msg.STPort)
		}
		// Name/info are now included in the initial Hello — deliver them immediately.
		if h.cb.OnPeerState != nil && msg.Name != "" {
			h.cb.OnPeerState(msg.DeviceID, msg.Name, msg.Info)
		}
		if h.cb.OnTrusted != nil && msg.Trusted != nil {
			h.cb.OnTrusted(msg.DeviceID, *msg.Trusted)
		}
	case PeerState:
		if h.cb.OnPeerState != nil {
			h.cb.OnPeerState(msg.DeviceID, msg.Name, msg.Info)
		}
	case Accepted:
		if h.cb.OnAccepted != nil {
			h.cb.OnAccepted(msg.DeviceID)
		}
	case Cancelled:
		if h.cb.OnCancelled != nil {
			h.cb.OnCancelled(msg.DeviceID)
		}
	case FolderOffer:
		if h.cb.OnFolderOffer != nil {
			h.cb.OnFolderOffer(msg.DeviceID, msg.FolderID, msg.FolderLabel, msg.FolderType)
		}
	case FolderAccept:
		if h.cb.OnFolderAccept != nil {
			h.cb.OnFolderAccept(msg.DeviceID, msg.FolderID, msg.FolderPath)
		}
	case FolderDecline:
		if h.cb.OnFolderDecline != nil {
			h.cb.OnFolderDecline(msg.DeviceID, msg.FolderID)
		}
	case FolderRemove:
		if h.cb.OnFolderRemove != nil {
			h.cb.OnFolderRemove(msg.DeviceID, msg.FolderID, msg.TargetDeviceID)
		}
	case FolderSync:
		if h.cb.OnFolderSync != nil {
			h.cb.OnFolderSync(msg.DeviceID, msg.FolderIDs)
		}
	}
}
