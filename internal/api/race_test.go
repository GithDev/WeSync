package api

import (
	"fmt"
	"sync"
	"testing"
)

// TestNoConcurrentMapAccess hammers the read paths (snapshot / listFolders /
// MaintainConnections / buildIncoming) against the writers (trustDevice /
// onPeerState) that mutate the shared maps in place under the lock.
//
// Before the fix, those read paths aliased the map header (`x := h.peers`),
// released the lock, then iterated the alias — i.e. read the live map while a
// writer mutated it. Go's runtime detects that as
// "fatal error: concurrent map read and map write" and HARD-KILLS the process
// (it is not a recoverable panic, and it fires even without the race detector,
// which we can't run here because cgo is unavailable). So this test acts as the
// guard: if the aliasing ever returns, the test binary crashes instead of
// passing. With the deep-copy fix it completes cleanly.
func TestNoConcurrentMapAccess(t *testing.T) {
	inst := newInstance(t, idA, "DeviceA")
	h := inst.handlers
	h.SetActive(true) // so MaintainConnections actually runs (not the background no-op)

	const workers = 4
	const iters = 3000
	var wg sync.WaitGroup

	// Writers: mutate trustedIDs / peers / theyTrustUs in place under h.mu.
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				id := fmt.Sprintf("DEV%d-%02d", w, i%23)
				h.trustDevice(id, "peer")
				h.onPeerState(id, "peer", nil)
			}
		}(w)
	}

	// Readers: the snapshot/folder/maintain paths that used to read the maps
	// after unlocking.
	for r := 0; r < workers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_, _ = h.snapshot()
				_ = h.listFolders()
				h.MaintainConnections()
			}
		}()
	}

	wg.Wait()
}
