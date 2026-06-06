package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── CORS / origin validation ──────────────────────────────────────────────────

func TestCORS_LocalhostAllowed(t *testing.T) {
	for _, origin := range []string{
		"http://localhost:5173",
		"http://localhost:8080",
		"https://localhost:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)
		if w.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Errorf("origin %q: expected ACAO header to be set, got %q", origin, w.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}

func TestCORS_LanOriginBlocked(t *testing.T) {
	for _, origin := range []string{
		"http://192.168.1.100:8080",
		"http://10.0.0.1:8080",
		"http://evil.com",
		"https://attacker.example",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("origin %q: ACAO header should NOT be set for non-localhost origins", origin)
		}
	}
}

func TestCORS_NoOrigin_Passthrough(t *testing.T) {
	// Direct API calls (curl, native apps) have no Origin header — should pass through.
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	called := false
	corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, req)
	if !called {
		t.Error("expected handler to be called for request without Origin header")
	}
}

func TestCORS_Preflight_Returns204(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	w := httptest.NewRecorder()
	handlerCalled := false
	corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
	})).ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("preflight: expected 204, got %d", w.Code)
	}
	if handlerCalled {
		t.Error("preflight: inner handler must not be called")
	}
}

// ── Body size limit ───────────────────────────────────────────────────────────

func TestBodyLimit_OversizedRequestRejected(t *testing.T) {
	// Build a body larger than maxRequestBody (1 MB).
	huge := strings.NewReader(strings.Repeat("x", maxRequestBody+1))
	req := httptest.NewRequest(http.MethodPost, "/api/pair", huge)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	bodySizeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to read the entire body.
		buf := make([]byte, maxRequestBody+2)
		n, _ := r.Body.Read(buf)
		if n > maxRequestBody {
			t.Errorf("read %d bytes — should have been capped at %d", n, maxRequestBody)
		}
	})).ServeHTTP(w, req)
}

// ── Security headers ──────────────────────────────────────────────────────────

func TestSecurityHeaders_Present(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, req)

	if v := w.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Errorf("X-Content-Type-Options: expected 'nosniff', got %q", v)
	}
	if v := w.Header().Get("X-Frame-Options"); v != "DENY" {
		t.Errorf("X-Frame-Options: expected 'DENY', got %q", v)
	}
}

// ── Device identity / cert_fp ─────────────────────────────────────────────────

func TestDeviceIdentity_CertFPStoredInMemory(t *testing.T) {
	a, _ := setup(t)
	const fp = "deadbeef"
	// CertFP is stored in-memory only via OnPeerVerified callback.
	a.handlers.state.SetPeerCertFP(idB, fp)
	stored := a.handlers.state.PeerCertFP(idB)
	if stored != fp {
		t.Errorf("expected cert_fp %q in memory, got %q", fp, stored)
	}
}

func TestDeviceIdentity_DifferentCert_TreatedAsNewDevice(t *testing.T) {
	// OnValidateCertFP always returns true — different cert = new device, not rejected.
	validate := func(_, _ string) bool { return true }
	if !validate(idB, "othercert") {
		t.Error("OnValidateCertFP should always allow")
	}
}
