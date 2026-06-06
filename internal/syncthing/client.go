package syncthing

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Client struct {
	baseURL   string
	apiKey    string
	http      *http.Client
	eventHTTP *http.Client // separate client for long-poll /rest/events — no timeout

	// scanPct holds the latest scan completion (0–100) per folder, fed by the
	// FolderScanProgress events in WatchEvents. db/status carries no scan
	// percentage, so this event-sourced cache is the only source. Entries are
	// cleared by GetFolderStatus once a folder leaves the scanning state.
	scanMu  sync.Mutex
	scanPct map[string]float64
}

func NewClient(baseURL, apiKey string) *Client {
	mkTransport := func() *http.Transport {
		tr := &http.Transport{}
		// Syncthing's GUI uses a self-signed cert by default. When the
		// user has HTTPS enabled (the typical case for Syncthing-Fork on
		// Android), we have no way to install that cert into the system
		// trust store from another app's sandbox — and we're talking to
		// 127.0.0.1 anyway, so skipping verify is safe here. http:// URLs
		// go through unchanged.
		if strings.HasPrefix(baseURL, "https://") {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec — see comment above
		}
		return tr
	}
	return &Client{
		baseURL:   baseURL,
		apiKey:    apiKey,
		http:      &http.Client{Transport: mkTransport(), Timeout: 10 * time.Second},
		eventHTTP: &http.Client{Transport: mkTransport()}, // no timeout — context controls cancellation
		scanPct:   make(map[string]float64),
	}
}

// setScanProgress records a folder's scan completion (0–100) from a
// FolderScanProgress event. total == 0 means "size not known yet" — we skip it
// so GetFolderStatus falls back to the indeterminate bar rather than showing 0%.
func (c *Client) setScanProgress(folderID string, current, total int64) {
	if total <= 0 {
		return
	}
	pct := float64(current) / float64(total) * 100
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	c.scanMu.Lock()
	c.scanPct[folderID] = pct
	c.scanMu.Unlock()
}

// takeScanProgress returns the cached scan percentage for a folder. When the
// folder is no longer scanning the caller passes scanning=false, which clears
// the stale entry so a later scan starts fresh from the indeterminate bar.
func (c *Client) takeScanProgress(folderID string, scanning bool) float64 {
	c.scanMu.Lock()
	defer c.scanMu.Unlock()
	if !scanning {
		delete(c.scanPct, folderID)
		return 0
	}
	return c.scanPct[folderID]
}

// HTTPError is returned when Syncthing responds with a non-2xx status. Callers
// can errors.As() it to branch on the status code (e.g. treat 404 as "not found"
// rather than a hard failure).
type HTTPError struct {
	Method     string
	Path       string
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("syncthing: %s %s returned %d", e.Method, e.Path, e.StatusCode)
}

func (c *Client) do(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return fmt.Errorf("syncthing: encode %s %s: %w", method, path, err)
		}
		reader = &buf
	}

	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("syncthing: build request %s %s: %w", method, path, err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("syncthing: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Method: method, Path: path, StatusCode: resp.StatusCode}
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("syncthing: decode %s %s: %w", method, path, err)
		}
	}
	return nil
}

func (c *Client) get(path string, out any) error    { return c.do(http.MethodGet, path, nil, out) }
func (c *Client) patch(path string, body any) error { return c.do(http.MethodPatch, path, body, nil) }
func (c *Client) del(path string) error             { return c.do(http.MethodDelete, path, nil, nil) }
func (c *Client) post(path string, body, out any) error {
	return c.do(http.MethodPost, path, body, out)
}

type SystemStatus struct {
	MyID  string `json:"myID"`
	Tilde string `json:"tilde"`
}

func (c *Client) SystemStatus() (SystemStatus, error) {
	var s SystemStatus
	return s, c.get("/rest/system/status", &s)
}
