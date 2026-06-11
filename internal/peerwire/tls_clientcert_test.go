package peerwire

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"wesync/internal/certid"
	"wesync/internal/sysinfo"
)

// These tests pin the wire's core security premise: identity IS the cert. A
// peer that does not present a client certificate must never be accepted onto
// the wire under a Hello-claimed identity. They exercise the real TLS path
// (httptest TLS server wrapping Hub.ServeWS) and assert BOTH observable state
// (did OnHello fire? with which identity?) AND stdout (the wire's log lines).

// syncBuffer is a concurrency-safe io.Writer — readInbound logs from a separate
// goroutine, so the capture buffer must be locked.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })
	return buf
}

// selfSigned makes an ECDSA P-384 self-signed cert, the same shape Syncthing
// (and thus our peers) use — so certid.DeviceIDFromCert derives a real ID.
func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "syncthing"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// startWireTLS serves h.ServeWS over TLS with the given client-auth policy.
// The server cert is httptest's own; the client uses InsecureSkipVerify, so the
// only thing under test is whether a CLIENT cert is required/handled.
func startWireTLS(t *testing.T, h *Hub, clientAuth tls.ClientAuthType) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(h.ServeWS))
	srv.TLS = &tls.Config{ClientAuth: clientAuth, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// dialWire opens a WSS connection. clientCert nil → present no client cert.
func dialWire(t *testing.T, srvURL string, clientCert *tls.Certificate) (*websocket.Conn, error) {
	t.Helper()
	tlsConf := &tls.Config{InsecureSkipVerify: true} //nolint:gosec — test only, server cert not under test
	if clientCert != nil {
		tlsConf.Certificates = []tls.Certificate{*clientCert}
	}
	d := websocket.Dialer{TLSClientConfig: tlsConf, HandshakeTimeout: 5 * time.Second}
	conn, _, err := d.Dial("wss"+strings.TrimPrefix(srvURL, "https"), nil)
	return conn, err
}

func newServerHub(onHello chan<- string) *Hub {
	return NewHub(peerB, "nodeB", 0, 0, sysinfo.DeviceInfo{}, nil, Callbacks{
		OnHello: func(fromDeviceID, _ string, _, _ int) {
			select {
			case onHello <- fromDeviceID:
			default:
			}
		},
	}, noOutgoing, nil, nil, nil)
}

//  1. A peer that presents a client cert connects, and its identity is taken
//     from the CERT — a spoofed Hello claim is ignored.
func TestWireTLS_ClientCertPeer_IdentifiedByCertNotClaim(t *testing.T) {
	logBuf := captureLog(t)
	helloID := make(chan string, 1)
	srv := startWireTLS(t, newServerHub(helloID), tls.RequireAnyClientCert)

	clientCert := selfSigned(t)
	conn, err := dialWire(t, srv.URL, &clientCert)
	if err != nil {
		t.Fatalf("legit cert-bearing peer was refused: %v", err)
	}
	defer conn.Close()

	// Claim to be peerA — must be overridden by the cert-derived identity.
	if err := conn.WriteJSON(Message{Type: Hello, DeviceID: peerA, Name: "spoof"}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	wantID := certid.DeviceIDFromCert(clientCert.Certificate[0])
	select {
	case got := <-helloID:
		if got == peerA {
			t.Fatal("OnHello used the Hello-claimed ID — cert identity was not enforced")
		}
		if got != wantID {
			t.Errorf("OnHello identity = %q, want cert-derived %q", got, wantID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnHello never fired for a legit cert-bearing peer")
	}
	if !strings.Contains(logBuf.String(), "cert verified OK") {
		t.Errorf("stdout missing 'cert verified OK'; got:\n%s", logBuf.String())
	}
}

//  2. With RequireAnyClientCert (the production setting), a certless peer can't
//     even complete the handshake — it's rejected at the TLS layer.
func TestWireTLS_RequireClientCert_CertlessRejectedAtHandshake(t *testing.T) {
	helloID := make(chan string, 1)
	srv := startWireTLS(t, newServerHub(helloID), tls.RequireAnyClientCert)

	conn, err := dialWire(t, srv.URL, nil)
	if err == nil {
		conn.Close()
		t.Fatal("certless peer completed the WSS handshake; RequireAnyClientCert must reject it")
	}
	// And nothing was ever dispatched.
	select {
	case from := <-helloID:
		t.Fatalf("OnHello fired for a certless peer (claimed %q) — spoof bypass", from)
	case <-time.After(300 * time.Millisecond):
	}
}

//  3. Defense in depth: even if the TLS flag were the loose RequestClientCert
//     (so the handshake succeeds without a cert), readInbound must refuse the
//     connection and never dispatch the Hello-claimed identity.
func TestWireTLS_RequestClientCert_CertlessRejectedAtAppLayer(t *testing.T) {
	logBuf := captureLog(t)
	helloID := make(chan string, 1)
	srv := startWireTLS(t, newServerHub(helloID), tls.RequestClientCert)

	conn, err := dialWire(t, srv.URL, nil)
	if err != nil {
		t.Fatalf("RequestClientCert should permit the handshake (we're testing the app-layer guard): %v", err)
	}
	defer conn.Close()

	// Spoof an identity with no cert behind it.
	_ = conn.WriteJSON(Message{Type: Hello, DeviceID: peerA, Name: "attacker"})

	select {
	case from := <-helloID:
		t.Fatalf("certless spoof was dispatched as %q — app-layer guard failed", from)
	case <-time.After(1 * time.Second):
		// good: readInbound returned before dispatch
	}

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logBuf.String(), "TLS active but peer sent no certificate") {
		if time.Now().After(deadline) {
			t.Errorf("stdout missing the app-layer rejection line; got:\n%s", logBuf.String())
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}
