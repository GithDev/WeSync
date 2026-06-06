package syncthing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WatchEvents long-polls the Syncthing event stream and calls onChange whenever
// a relevant state-changing event arrives. Runs until ctx is cancelled.
func (c *Client) WatchEvents(ctx context.Context, onChange func()) {
	since := 0
	// FolderScanProgress is decoded for its scan-% payload (see setScanProgress);
	// it does NOT fire onChange — the UI polls folder status on its own cadence.
	// The rest are state-changing and do fire onChange.
	types := "DeviceConnected,DeviceDisconnected,PendingDevicesChanged,PendingFoldersChanged,ConfigSaved,FolderScanProgress"
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		url := fmt.Sprintf("%s/rest/events?since=%d&types=%s&timeout=30", c.baseURL, since, types)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		req.Header.Set("X-API-Key", c.apiKey)

		resp, err := c.eventHTTP.Do(req)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				// Connection error — ST may have restarted and reset its event sequence.
				// Reset since to 0 so the next poll fetches from the beginning and the
				// onChange callback picks up any state changes that happened during restart.
				since = 0
				time.Sleep(2 * time.Second)
				continue
			}
		}

		// A non-200 (ST busy, restarting, auth hiccup) returns an error body that
		// would decode to zero events — without this guard the loop would spin
		// hammering ST with no pacing (the 30s long-poll only paces the 200 path).
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		var events []struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
			Data struct {
				Folder  string `json:"folder"`
				Current int64  `json:"current"`
				Total   int64  `json:"total"`
			} `json:"data"`
		}
		err = json.NewDecoder(resp.Body).Decode(&events)
		resp.Body.Close()
		if err != nil {
			// Garbled/partial body. Don't advance `since` or fire onChange (that
			// would silently stall event processing); back off and retry from the
			// same point instead of swallowing it.
			time.Sleep(2 * time.Second)
			continue
		}

		if len(events) == 0 {
			continue
		}
		since = events[len(events)-1].ID

		// Scan-progress events only refresh the cached %; everything else is a
		// state change the rest of the app needs to re-sync for.
		stateChanged := false
		for _, e := range events {
			if e.Type == "FolderScanProgress" {
				c.setScanProgress(e.Data.Folder, e.Data.Current, e.Data.Total)
			} else {
				stateChanged = true
			}
		}
		if stateChanged {
			onChange()
		}
	}
}
