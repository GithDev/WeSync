package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	uiPingInterval = 30 * time.Second
	uiPongWait     = 60 * time.Second
	uiWriteWait    = 10 * time.Second
	// uiSendBuffer is how many messages a client may fall behind before we drop
	// it. A client that can't keep up (stuck socket) fills this, then Broadcast
	// closes it instead of blocking — so one slow client never delays the rest.
	uiSendBuffer = 16
)

var upgrader = websocket.Upgrader{
	// Only allow connections from localhost — blocks CSRF from pages on the LAN.
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // non-browser client (curl, tests)
		}
		return isLocalOrigin(origin)
	},
}

// uiClient is one browser WebSocket. All writes to its socket go through the
// single writePump goroutine via send — gorilla forbids concurrent writes to a
// connection, and a lone writer goroutine is the simplest way to guarantee that
// (no per-conn write lock needed, and the hub's map lock is never held during
// I/O). Reads happen on the ServeWS goroutine; concurrent read+write is allowed.
type uiClient struct {
	conn *websocket.Conn
	send chan []byte
}

// writePump is the sole writer for c.conn: it drains queued messages and emits
// periodic pings. It exits on stop (reader gone), on a closed send channel, or
// on the first write error — closing the socket on its way out so the reader
// unblocks and runs cleanup. A ping doubles as liveness: with the read deadline
// + pong handler, a hard-killed app is detected within one ping cycle instead of
// lingering as a ghost client.
func (c *uiClient) writePump(stop <-chan struct{}) {
	ping := time.NewTicker(uiPingInterval)
	defer ping.Stop()
	for {
		select {
		case data, ok := <-c.send:
			if !ok {
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(uiWriteWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				c.conn.Close()
				return
			}
		case <-ping.C:
			c.conn.SetWriteDeadline(time.Now().Add(uiWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.conn.Close()
				return
			}
		case <-stop:
			return
		}
	}
}

type Hub struct {
	mu       sync.Mutex
	clients  map[*uiClient]struct{}
	onChange func(hasClients bool) // called when active client count crosses zero
}

func NewHub() *Hub {
	return &Hub{clients: make(map[*uiClient]struct{})}
}

// OnActiveChange registers a callback that fires when the UI goes from
// no-clients to has-clients (true) or vice versa (false).
func (h *Hub) OnActiveChange(fn func(hasClients bool)) {
	h.mu.Lock()
	h.onChange = fn
	h.mu.Unlock()
}

// Broadcast queues data for every connected client. It only ever does
// non-blocking channel sends under the lock — never socket I/O — so it can't be
// stalled by a slow client, and the hub stays responsive to connects/disconnects
// no matter how a single client behaves. A client whose buffer is full is too
// far behind: we drop it (close the socket outside the lock) and let its ServeWS
// goroutine run the normal cleanup (map removal + onChange).
func (h *Hub) Broadcast(data []byte) {
	var slow []*uiClient
	h.mu.Lock()
	for c := range h.clients {
		select {
		case c.send <- data:
		default:
			slow = append(slow, c)
		}
	}
	h.mu.Unlock()

	for _, c := range slow {
		c.conn.Close()
	}
}

func (h *Hub) ServeWS(handlers *Handlers) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		client := &uiClient{conn: conn, send: make(chan []byte, uiSendBuffer)}

		h.mu.Lock()
		h.clients[client] = struct{}{}
		onChange := h.onChange
		h.mu.Unlock()

		// A UI client connecting means the app window is open → foreground.
		// Signal it on EVERY connect, not just the 0→1 transition: reopening the
		// app reuses a backend that may have gone background, and a stale/ghost
		// client (or the still-connected hidden webview) can keep the count above
		// zero — so a "crossed zero" check would miss the reopen and leave the
		// node silent. SetForeground is idempotent, so re-signaling is safe; this
		// is the robust recovery path whenever the UI (re)connects.
		if onChange != nil {
			onChange(true)
		}

		// Start the sole writer, then queue the initial snapshot through it so it
		// can never race a concurrent Broadcast on this socket.
		stop := make(chan struct{})
		go client.writePump(stop)
		if snap, err := handlers.snapshot(); err == nil {
			client.send <- snap // buffer has room (just created) — won't block
		}

		// Expect a pong within one full ping cycle; the handler resets the
		// deadline each time the client answers a ping from writePump.
		conn.SetReadDeadline(time.Now().Add(uiPongWait))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(uiPongWait))
			return nil
		})

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
		close(stop) // stop the writer

		h.mu.Lock()
		delete(h.clients, client)
		nowEmpty := len(h.clients) == 0
		onChange = h.onChange
		h.mu.Unlock()

		if nowEmpty && onChange != nil {
			onChange(false) // last client disconnected
		}

		conn.Close()
	}
}
