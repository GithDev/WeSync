package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A static page here leaves the window stuck on "Starting WeSync…" forever,
// since nothing re-navigates the webview after the backend comes up.
func TestLoadingPageReloadsItself(t *testing.T) {
	rec := httptest.NewRecorder()
	serveLoadingPage(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()

	if !strings.Contains(body, "location.reload()") {
		t.Error("loading page never reloads — the window would stay on 'Starting WeSync…' forever")
	}
	if !strings.Contains(body, "/api/status") {
		t.Error("loading page should poll /api/status so it only navigates once the backend answers")
	}
	if strings.Count(body, "setTimeout(poll") < 2 {
		t.Error("loading page must retry after a failed/!ok poll, not give up after one attempt")
	}
}

// Otherwise the reload lands on a cached copy of the loading page.
func TestLoadingPageIsNotCached(t *testing.T) {
	rec := httptest.NewRecorder()
	serveLoadingPage(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want it to contain no-store", cc)
	}
}
