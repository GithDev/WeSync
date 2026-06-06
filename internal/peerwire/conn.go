package peerwire

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"wesync/internal/certid"

	"github.com/gorilla/websocket"
)

const (
	reconnectBase  = 2 * time.Second
	reconnectMax   = 60 * time.Second
	writeTimeout   = 10 * time.Second
	dialTimeout    = 15 * time.Second // don't wait for OS's 75s default
	pingInterval   = 20 * time.Second // send ping this often
	pongDeadline   = 10 * time.Second // close if no pong within this window
	maxMessageSize = 512 * 1024       // 512 KB — rejects oversized/malicious frames
)

type peerConn struct {
	// deviceID is the map key under which the hub tracks this connection. It
	// starts as a temporary key (SID or "addr:port") and is migrated to the real
	// cert-derived ID by the hub's adopt* methods, possibly from another
	// goroutine — so it is guarded by mu. Read it via id(), write it via setID().
	deviceID         string
	addr             string
	port             int
	hub              *Hub
	remoteCertFP     string
	remoteDeviceID   string // derived from remote cert — authoritative, never from Hello claim

	mu        sync.Mutex // guards ws, deviceID, remoteCertFP, remoteDeviceID
	ws        *websocket.Conn
	writeMu   sync.Mutex
	connected atomic.Bool // true only while ws is open

	sendCh chan Message
	done   chan struct{}
	wake   chan struct{} // buffered 1; nudges run() to skip its reconnect backoff and retry now
}

func shortKey(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// remoteIPOf returns the peer's IP from a WebSocket connection, stripping any
// IPv6 zone suffix (e.g. "fe80::1%eth0" → "fe80::1"). Returns "" if the remote
// address can't be parsed.
func remoteIPOf(ws *websocket.Conn) string {
	host, _, err := net.SplitHostPort(ws.RemoteAddr().String())
	if err != nil {
		return ""
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	return host
}

func newPeerConn(deviceID, addr string, port int, hub *Hub) *peerConn {
	return &peerConn{
		deviceID: deviceID,
		addr:     addr,
		port:     port,
		hub:      hub,
		sendCh:   make(chan Message, 64),
		done:     make(chan struct{}),
		wake:     make(chan struct{}, 1),
	}
}

// nudge asks a connection sitting in reconnect backoff to retry immediately.
// Called when fresh evidence arrives that the peer is reachable again (a UDP
// announce or an inbound hello), so we don't wait out the exponential backoff.
// Non-blocking and idempotent.
func (c *peerConn) nudge() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// id returns the connection's current device-ID key under the lock. deviceID is
// migrated to the real cert-derived ID by the hub's adopt* methods (which may run
// on another peer's inbound goroutine), so the connection's own goroutine must
// read it synchronized.
func (c *peerConn) id() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deviceID
}

// setID updates the device-ID key. Callers (adoptConn/adoptConnByAddr) already
// hold h.mu; taking c.mu here keeps readers on the connection's goroutine
// race-free. Lock order is always h.mu → c.mu.
func (c *peerConn) setID(id string) {
	c.mu.Lock()
	c.deviceID = id
	c.mu.Unlock()
}

func (c *peerConn) close() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	c.mu.Lock()
	if c.ws != nil {
		c.ws.Close()
	}
	c.mu.Unlock()
}

func (c *peerConn) send(msg Message) {
	select {
	case c.sendCh <- msg:
	default:
		log.Printf("peerwire [outbound %s]: send queue full — dropping %s", shortKey(c.id()), msg.Type)
	}
}

// sendSync writes directly to the WS, bypassing the send channel.
func (c *peerConn) sendSync(msg Message, timeout time.Duration) error {
	c.mu.Lock()
	ws := c.ws
	c.mu.Unlock()
	if ws == nil {
		return fmt.Errorf("not connected")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if timeout > writeTimeout {
		timeout = writeTimeout
	}
	ws.SetWriteDeadline(time.Now().Add(timeout))
	return ws.WriteMessage(websocket.TextMessage, data)
}

func (c *peerConn) run() {
	delay := reconnectBase
	for {
		select {
		case <-c.done:
			return
		default:
		}
		if err := c.connect(); err != nil {
			if delay >= 8*time.Second {
				log.Printf("peerwire: %s unreachable (%v), retry in %s", shortKey(c.id()), err, delay)
			}
			jitter := time.Duration(rand.Int63n(int64(delay / 4)))
			select {
			case <-c.done:
				return
			case <-c.wake:
				// Fresh evidence the peer is back — retry now, reset the backoff.
				delay = reconnectBase
			case <-time.After(delay + jitter):
				if delay < reconnectMax {
					delay *= 2
				}
			}
			continue
		}
		delay = reconnectBase
	}
}

// connect dials the peer, sends our Hello, and runs the read/write/ping loops
// until the connection drops, then tears them down. It returns nil on a clean
// disconnect and an error only when the dial itself failed (so run() can back
// off and retry).
func (c *peerConn) connect() error {
	ws, err := c.dialWS()
	if err != nil {
		return err
	}
	ws.SetReadLimit(maxMessageSize)

	c.mu.Lock()
	c.ws = ws
	c.mu.Unlock()
	c.connected.Store(true)
	// key is our current map key (real cert-derived ID once the dial's cert
	// callback adopted us, else the temporary SID). Snapshot it once for logging
	// and offer lookups.
	key := c.id()
	log.Printf("peerwire: connected to %s", shortKey(key))

	c.sendHello(ws, key)

	// connCtx is cancelled the instant this connection ends (read loop exits, for
	// any reason — including the peer closing our inbound socket). Both the ping
	// and write goroutines watch it so they tear down immediately instead of the
	// ping goroutine sleeping up to a full pingInterval on its ticker. Without
	// this, connect()'s cleanup blocks on <-pingDone for up to pingInterval, which
	// stalls run()'s reconnect loop and makes a nudge a no-op — so a peer that
	// dropped us (e.g. went background) wouldn't be redialed for ~20s.
	connCtx, connCancel := context.WithCancel(context.Background())
	defer connCancel()

	pingDone := c.startPingLoop(ws, connCtx)
	writeDone := c.startWriteLoop(ws, connCtx)

	c.readLoop(ws) // blocks until the connection drops

	ws.Close()
	c.connected.Store(false)
	connCancel() // stop ping + write goroutines at once — avoids waiting out the
	<-writeDone  // ping ticker (which would block run()'s reconnect for ~pingInterval)
	<-pingDone
	c.mu.Lock()
	c.ws = nil
	c.mu.Unlock()
	log.Printf("peerwire: disconnected from %s", shortKey(c.id()))
	return nil
}

// dialWS opens the WebSocket to the peer, trying each candidate source IP for
// multi-homed reachability, and pins the peer's TLS cert when TLS is in use.
func (c *peerConn) dialWS() (*websocket.Conn, error) {
	c.hub.mu.RLock()
	tlsCert := c.hub.tlsCert
	c.hub.mu.RUnlock()

	scheme := "ws"
	var tlsConf *tls.Config
	if tlsCert != nil {
		scheme = "wss"
		tlsConf = &tls.Config{
			Certificates:       []tls.Certificate{*tlsCert},
			InsecureSkipVerify: true, //nolint:gosec — fingerprint verified below
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				if len(rawCerts) == 0 {
					return fmt.Errorf("peerwire: peer presented no certificate")
				}
				h := certid.CertHash(rawCerts[0])
				fp := hex.EncodeToString(h[:])
				devID := certid.DeviceIDFromCert(rawCerts[0])
				c.mu.Lock()
				c.remoteCertFP = fp
				c.remoteDeviceID = devID
				c.mu.Unlock()
				// If we connected without a known deviceID (addr:port key),
				// migrate to the real deviceID now that cert is verified.
				if c.id() != devID {
					c.hub.adoptConn(c.id(), devID)
				}
				log.Printf("peerwire [outbound %s]: TLS OK — certFP %.16s…", shortKey(devID), fp)
				return nil
			},
		}
	}

	// IPv6 link-local addresses carry a zone ID (e.g. "fe80::1%Wi-Fi"). The "%"
	// is an invalid percent-escape inside a URL, so url.Parse — which the WS
	// dialer runs — rejects it. Percent-encode the zone delimiter ("%" → "%25")
	// so it survives parsing; the dialer decodes it back before net.Dial.
	host := strings.Replace(c.addr, "%", "%25", 1)
	url := fmt.Sprintf("%s://%s/peer/ws", scheme, net.JoinHostPort(host, strconv.Itoa(c.port)))

	// Multi-homed dial: on a host with more than one active NIC, the OS may
	// egress a directly-connected peer out the wrong interface (e.g. a LAN cable
	// whose subnet overlaps WiFi), so the dial never reaches the peer even though
	// another NIC could. We try binding the source IP to each local interface on
	// the peer's subnet first — which forces the dial out that NIC — then fall
	// back to an unbound dial (OS default route). First success wins, so this can
	// only ADD reachable paths; it never removes the previous behavior.
	candidates := dialSourceIPs(c.addr)
	perAttempt := dialTimeout
	if len(candidates) > 1 {
		perAttempt = 6 * time.Second // bounded so trying several stays well under the reconnect cadence
	}
	var lastErr error
	for _, src := range candidates {
		nd := &net.Dialer{Timeout: perAttempt}
		if src != nil {
			nd.LocalAddr = &net.TCPAddr{IP: src}
		}
		d := &websocket.Dialer{NetDialContext: nd.DialContext}
		if tlsConf != nil {
			d.TLSClientConfig = tlsConf
		}
		ws, resp, err := d.Dial(url, nil)
		if resp != nil {
			resp.Body.Close() // gorilla hands back a response on failure — don't leak it
		}
		if err == nil {
			return ws, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// sendHello sends the initial Hello (identity + name + sysinfo + trust state)
// followed by any queued folder offers for this peer. key is the connection's
// current map key.
//
// trusted: true  = we have you in our ST config (isTrusted)
// trusted: false = we EXPLICITLY removed you (receiver cascades the removal)
// trusted: nil   = we don't have you yet — say nothing
//
// Trust is resolved against the cert-derived identity when known (authoritative),
// falling back to key for no-TLS/tests. Crucially we send false ONLY when the
// peer was explicitly removed — never merely because this conn isn't adopted to a
// real deviceID yet. A bare trusted:false from a churning or SID-keyed connection
// would otherwise cascade-remove an established trust (the "both devices reset to
// new" bug).
func (c *peerConn) sendHello(ws *websocket.Conn, key string) {
	info := c.hub.selfInfo
	c.mu.Lock()
	trustID := c.remoteDeviceID
	c.mu.Unlock()
	if trustID == "" {
		trustID = key
	}
	var trusted *bool
	switch {
	case c.hub.isTrusted != nil && c.hub.isTrusted(trustID):
		t := true
		trusted = &t
		log.Printf("peerwire [outbound %s]: sending Hello trusted:true", shortKey(key))
	case c.hub.isRemoved != nil && c.hub.isRemoved(trustID):
		f := false
		trusted = &f
		log.Printf("peerwire [outbound %s]: sending Hello trusted:false (explicitly removed)", shortKey(key))
	default:
		trusted = nil // unknown / not-yet-adopted — omit so we never trigger a removal
	}
	// DeviceID is only needed when there is no TLS cert (dev/test fallback).
	// When TLS is available, the receiver derives our ID from the cert.
	helloDeviceID := ""
	if c.hub.selfCertFP == "" {
		helloDeviceID = c.hub.selfID
	}
	c.writeMsg(ws, Message{
		Type:     Hello,
		DeviceID: helloDeviceID,
		Port:     c.hub.selfPort, STPort: c.hub.selfSTPort,
		CertFP:   c.hub.selfCertFP,
		Name:     c.hub.selfName, Info: &info,
		Trusted:  trusted,
	})
	// Folder state — send if there's anything pending. Security: untrusted peers
	// have no pending IDs/offers so nothing leaks. No DB check here — avoid
	// blocking the read-loop startup.
	folderIDs := c.hub.PendingFolderIDs(key)
	if len(folderIDs) > 0 {
		c.writeMsg(ws, Message{Type: FolderSync, DeviceID: c.hub.selfID, FolderIDs: folderIDs})
	}
	for _, msg := range c.hub.DrainOffers(key) {
		c.writeMsg(ws, msg)
		log.Printf("peerwire [outbound %s]: flushed queued %s", shortKey(key), msg.Type)
	}
}

// startPingLoop launches the ping goroutine and returns a channel closed when it
// exits. Pings detect silent TCP drops (NAT timeout, captive portal, etc.).
func (c *peerConn) startPingLoop(ws *websocket.Conn, connCtx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-c.done:
				return
			case <-connCtx.Done():
				return
			case <-ticker.C:
				c.writeMu.Lock()
				ws.SetWriteDeadline(time.Now().Add(writeTimeout))
				err := ws.WriteMessage(websocket.PingMessage, nil)
				c.writeMu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()
	return done
}

// startWriteLoop launches the goroutine that drains the send channel to the WS
// and returns a channel closed when it exits. It is tied to this connection via
// connCtx so it never outlives its ws.
func (c *peerConn) startWriteLoop(ws *websocket.Conn, connCtx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-c.done:
				ws.Close()
				return
			case <-connCtx.Done():
				return
			case msg := <-c.sendCh:
				c.writeMsg(ws, msg)
			}
		}
	}()
	return done
}

// readLoop reads frames until the connection drops. On the first Hello it pins
// the peer's cert fingerprint and adopts the connection under the real device ID;
// every message is then dispatched to the hub. Returns when the read fails.
func (c *peerConn) readLoop(ws *websocket.Conn) {
	// Pong handler resets the read deadline, proving the other side is alive;
	// the initial deadline requires a pong within one full ping cycle.
	ws.SetPongHandler(func(_ string) error {
		ws.SetReadDeadline(time.Now().Add(pingInterval + pongDeadline))
		return nil
	})
	ws.SetReadDeadline(time.Now().Add(pingInterval + pongDeadline))

	remoteIP := remoteIPOf(ws)
	certVerified := false
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		// Each received frame resets the deadline.
		ws.SetReadDeadline(time.Now().Add(pingInterval + pongDeadline))

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("peerwire [outbound %s]: malformed message — %v", shortKey(c.id()), err)
			continue
		}
		if msg.Type == Hello && !certVerified {
			c.mu.Lock()
			remoteCertFP := c.remoteCertFP
			remoteDevID := c.remoteDeviceID
			c.mu.Unlock()
			cur := c.id()
			id := shortKey(cur)
			if remoteCertFP != "" {
				if msg.CertFP != "" && msg.CertFP != remoteCertFP {
					log.Printf("peerwire [outbound %s]: REJECTED — certFP mismatch", id)
					ws.Close()
					return
				}
				log.Printf("peerwire [outbound %s]: cert verified OK", id)
				if c.hub.cb.OnPeerVerified != nil {
					c.hub.cb.OnPeerVerified(cur, remoteCertFP)
				}
			} else {
				log.Printf("peerwire [outbound %s]: no TLS — skipping cert verification", id)
			}
			certVerified = true
			// Use cert-derived ID if available; fall back to Hello-claimed ID (no-TLS).
			if remoteDevID != "" {
				msg.DeviceID = remoteDevID
			}
			// Adopt connection under the real device ID (cert-derived or Hello-claimed).
			if msg.DeviceID != "" && msg.DeviceID != cur {
				c.hub.adoptConn(cur, msg.DeviceID)
			}
		} else if certVerified {
			c.mu.Lock()
			remoteDevID := c.remoteDeviceID
			c.mu.Unlock()
			if remoteDevID != "" {
				msg.DeviceID = remoteDevID
			}
		}
		c.hub.dispatch(msg, remoteIP)
	}
}

func (c *peerConn) writeMsg(ws *websocket.Conn, msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	ws.WriteMessage(websocket.TextMessage, data) //nolint:errcheck
}

