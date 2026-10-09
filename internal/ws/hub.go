// Package ws broadcasts versioned simulator events to browsers.
// Each client has a bounded queue. A client that falls behind is disconnected
// instead of letting memory grow. The hub closes every connection on shutdown.
package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"baccarat-live-simulator/internal/state"
)

const (
	maxClients   = 100
	sendQueue    = 16
	writeTimeout = 5 * time.Second
	pingInterval = 25 * time.Second
	readLimit    = 4096
)

// Hub fans out events to connected browsers.
type Hub struct {
	logger  *slog.Logger
	mu      sync.Mutex
	clients map[*client]struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	closed  bool
}

type client struct {
	conn *websocket.Conn
	send chan []byte
	once sync.Once
}

// New starts a hub that accepts clients until Close.
func New(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		logger:  logger,
		clients: map[*client]struct{}{},
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Accept upgrades an HTTP request and blocks until the socket closes.
// initial is the first message and is queued ahead of later broadcasts.
func (h *Hub) Accept(w http.ResponseWriter, r *http.Request, initial []byte) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"127.0.0.1:*", "localhost:*"},
	})
	if err != nil {
		h.logger.Warn("websocket rejected", "err", err.Error())
		return
	}
	h.Serve(conn, initial)
}

// Serve registers an already accepted connection and blocks on its reader.
func (h *Hub) Serve(conn *websocket.Conn, initial []byte) {
	c := &client{conn: conn, send: make(chan []byte, sendQueue)}
	h.mu.Lock()
	if h.closed || len(h.clients) >= maxClients {
		h.mu.Unlock()
		_ = conn.Close(websocket.StatusPolicyViolation, "unavailable")
		return
	}
	c.send <- initial
	h.clients[c] = struct{}{}
	h.wg.Add(1)
	go c.write(h)
	h.mu.Unlock()
	c.read(h)
}

// Publish marshals an event and queues it for every client.
// Slow clients are removed when their queue is full.
func (h *Hub) Publish(env state.Envelope) {
	payload, err := json.Marshal(env)
	if err != nil {
		h.logger.Error("marshal event", "err", err.Error())
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for c := range h.clients {
		select {
		case c.send <- payload:
		default:
			delete(h.clients, c)
			c.shutdown()
		}
	}
}

// Close disconnects every client and waits for writer goroutines to finish.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	h.cancel()
	clients := h.clients
	h.clients = map[*client]struct{}{}
	for c := range clients {
		c.shutdown()
	}
	h.mu.Unlock()
	h.wg.Wait()
}

func (c *client) read(h *Hub) {
	defer h.remove(c)
	c.conn.SetReadLimit(readLimit)
	for {
		_, _, err := c.conn.Read(h.ctx)
		if err != nil {
			return
		}
	}
}

func (c *client) write(h *Hub) {
	defer h.wg.Done()
	defer func() { _ = c.conn.Close(websocket.StatusGoingAway, "") }()
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(h.ctx, writeTimeout)
			err := c.conn.Ping(ctx)
			cancel()
			if err != nil {
				return
			}
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(h.ctx, writeTimeout)
			err := c.conn.Write(ctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; !ok {
		c.shutdown()
		return
	}
	delete(h.clients, c)
	c.shutdown()
}

func (c *client) shutdown() {
	c.once.Do(func() { close(c.send) })
}
