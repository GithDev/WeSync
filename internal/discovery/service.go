package discovery

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

const (
	announceInterval = 5 * time.Second
	graceMultiplier  = 3 // miss this many announces before declaring gone
	gracePeriod      = announceInterval * graceMultiplier
)

type Service struct {
	announcer    *Announcer
	listener     *Listener
	Peers        chan Peer
	PeerGone     chan string // SIDs of peers that stopped announcing (for wire cleanup)
	wantAnnounce atomic.Bool
	foreground   atomic.Bool

	// Two independent inputs; actual announcing is COMPUTED from both (see
	// shouldAnnounce) so no flag is ever written by two owners. wantAnnounce is
	// the user's discoverability preference (persisted; set only by the visibility
	// toggle). foreground is the UI-open/lifecycle gate (set only by SetForeground).
	// We announce only when the user wants to AND the UI is open; going background
	// silences UDP without losing the preference.

	mu       sync.Mutex
	lastSeen map[string]time.Time // SID → last announce time
	// lastSeenAddr mirrors lastSeen but keyed by the announce SOURCE IP. The API
	// gates the "discoverable" list on PresentAddr so an unpaired device shows only
	// while we ACTUALLY hear it announce — not merely while a wire connection to it
	// lingers or gets re-dialed. The list is keyed by the cert-derived deviceID,
	// lastSeen by the ephemeral per-process SID; the source IP is the stable bridge
	// (it equals the wire connection's remote IP). So a peer that stops announcing
	// (discovery off) drops out of discovery after the grace period regardless of
	// what the wire is doing.
	lastSeenAddr map[string]time.Time // announce source IP → last announce time

	// announceNow asks Run to emit an announce immediately rather than waiting
	// out the periodic ticker. Kicked when announcing is (re)enabled — so a node
	// that just reopened its UI is heard by peers at once. A peer that hears it
	// nudges any wire connection sitting in reconnect backoff, so the whole mesh
	// recovers on network latency instead of up to announceInterval + backoff.
	// Buffered(1) and kicked non-blockingly, so kicks coalesce and never block.
	announceNow chan struct{}
}

func NewService(port int) (*Service, error) {
	ann, err := NewAnnouncer(port)
	if err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	return &Service{
		announcer:    ann,
		listener:     NewListener(ann.SID()),
		Peers:        make(chan Peer, 16),
		PeerGone:     make(chan string, 16),
		lastSeen:     make(map[string]time.Time),
		lastSeenAddr: make(map[string]time.Time),
		announceNow:  make(chan struct{}, 1),
	}, nil
}

func (s *Service) SID() string { return s.announcer.SID() }

// SetWantAnnounce records the user's discoverability preference — the ONLY writer
// of wantAnnounce. SetForeground records the UI-open/lifecycle gate — the ONLY
// writer of foreground. Either change kicks an immediate announce when the
// computed shouldAnnounce is now true.
func (s *Service) SetWantAnnounce(v bool) { s.wantAnnounce.Store(v); s.kickIf(s.shouldAnnounce()) }
func (s *Service) SetForeground(v bool)   { s.foreground.Store(v); s.kickIf(s.shouldAnnounce()) }
func (s *Service) WantAnnounce() bool     { return s.wantAnnounce.Load() }
func (s *Service) IsListening() bool      { return s.foreground.Load() }

// PresentAddr reports whether we are CURRENTLY hearing UDP announces from a peer
// at this source IP (within the grace period). The API gates the discoverable
// list on this so an unpaired device drops out of discovery when it stops
// announcing (e.g. discovery toggled off), even if a wire connection to it
// lingers or gets re-dialed. Keyed by source IP because the announce carries only
// an ephemeral SID while the list is keyed by the cert-derived deviceID; the IP
// is the stable bridge between the two.
func (s *Service) PresentAddr(addr string) bool {
	if addr == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.lastSeenAddr[addr]
	return ok && time.Since(t) <= gracePeriod
}

// shouldAnnounce is THE computed property: announce only when the user wants to
// be discoverable AND the UI is open. Re-evaluated wherever it's needed; never
// stored, so the two inputs can never disagree with a cached result.
func (s *Service) shouldAnnounce() bool { return s.wantAnnounce.Load() && s.foreground.Load() }

// kickIf asks Run to announce immediately when announcing was just turned on, so
// re-enabling discovery (UI reopened, or "visible" toggled on) is heard by peers
// at once instead of after up to announceInterval. Non-blocking: coalesces with
// any pending kick and never blocks the caller.
func (s *Service) kickIf(on bool) {
	if !on {
		return
	}
	select {
	case s.announceNow <- struct{}{}:
	default:
	}
}

func (s *Service) Run(ctx context.Context) error {
	internalPeers := make(chan Peer, 16)
	errc := make(chan error, 1)
	startListener := func() { go func() { errc <- s.listener.Run(ctx, internalPeers) }() }
	startListener()

	announce := time.NewTicker(announceInterval)
	expiry := time.NewTicker(announceInterval)
	defer announce.Stop()
	defer expiry.Stop()

	for {
		select {
		case <-announce.C:
			if s.shouldAnnounce() {
				s.announcer.send()
			}

		case <-s.announceNow:
			// Discovery was just (re)enabled — announce now instead of waiting for
			// the next tick, and realign the ticker so we don't immediately
			// double-send a tick later.
			if s.shouldAnnounce() {
				s.announcer.send()
				announce.Reset(announceInterval)
			}

		case peer := <-internalPeers:
			// Update last-seen for this SID and its source IP (the latter drives the
			// "is this peer currently announcing?" check the API uses to gate the
			// discoverable list). Always recorded — even when we're not discoverable
			// ourselves — so presence reflects what we hear, not our own state.
			now := time.Now()
			s.mu.Lock()
			s.lastSeen[peer.SID] = now
			if peer.Addr != "" {
				s.lastSeenAddr[peer.Addr] = now
			}
			s.mu.Unlock()
			// Pick up newly heard peers whenever the UI is open — even if WE are not
			// discoverable. Discovery is two-way: turning off our own announce hides
			// US from others (they gate their list on hearing our announce — see
			// PresentAddr) but must NOT stop US from seeing them. So the radar keeps
			// finding devices while we stay invisible.
			if s.foreground.Load() {
				select {
				case s.Peers <- peer:
				default:
				}
			}

		case <-expiry.C:
			// Check for SIDs that have been silent longer than the grace period.
			now := time.Now()
			s.mu.Lock()
			var expired []string
			for sid, t := range s.lastSeen {
				if now.Sub(t) > gracePeriod {
					expired = append(expired, sid)
				}
			}
			// Prune addr presence on the same schedule so PresentAddr stops
			// reporting a peer once we've stopped hearing it (and the map can't
			// grow unbounded over a long uptime with churning addresses).
			for addr, t := range s.lastSeenAddr {
				if now.Sub(t) > gracePeriod {
					delete(s.lastSeenAddr, addr)
				}
			}
			s.mu.Unlock()
			for _, sid := range expired {
				select {
				case s.PeerGone <- sid:
					// Delivered — now forget it. If the channel was full we keep
					// the SID in lastSeen so the next expiry tick retries: a dropped
					// PeerGone would otherwise strand the peer's wire connection.
					s.mu.Lock()
					delete(s.lastSeen, sid)
					s.mu.Unlock()
				default:
				}
			}

		case err := <-errc:
			if ctx.Err() != nil {
				return nil
			}
			// The listener loop died on a transient socket error (a NIC dropped,
			// the multicast group reset, etc.). Don't kill discovery — announces
			// and expiry must keep running — so log it and restart the listener
			// after a short delay.
			log.Printf("discovery: listener exited (%v) — restarting in %s", err, announceInterval)
			time.AfterFunc(announceInterval, startListener)
		case <-ctx.Done():
			return nil
		}
	}
}
