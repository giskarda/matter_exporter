package matterws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/coder/websocket"
)

const startListeningID = "1"

// Handler receives everything the client learns from the server. Methods are
// called sequentially from the goroutine running Client.Run.
type Handler interface {
	// Connected is called once per connection with the full node dump. The
	// handler must replace any previous state with it.
	Connected(info ServerInfo, nodes []Node)
	// Event is called for every event received after the dump.
	Event(ev Event)
	// Disconnected is called when a connection that reached Connected ends.
	Disconnected()
}

// Client is a passive WebSocket client: it sends start_listening and nothing
// else, and reconnects with exponential backoff.
type Client struct {
	URL     string
	Handler Handler
	Logger  *slog.Logger

	// MinBackoff and MaxBackoff bound the reconnect delay (defaults 1s, 1m).
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// PingInterval is how often the connection is probed (default 30s).
	PingInterval time.Duration
}

// Run connects and keeps reconnecting until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	logger := c.Logger
	if logger == nil {
		logger = slog.Default()
	}
	minB, maxB := c.MinBackoff, c.MaxBackoff
	if minB <= 0 {
		minB = time.Second
	}
	if maxB < minB {
		maxB = max(minB, time.Minute)
	}

	backoff := minB
	for {
		listening, err := c.session(ctx, logger)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if listening {
			// The server was healthy: start over with a short delay.
			backoff = minB
		}
		// Jitter in [0.5, 1.0) of the nominal delay.
		delay := time.Duration(float64(backoff) * (0.5 + rand.Float64()/2))
		logger.Warn("connection to Matter server lost, reconnecting",
			"url", c.URL, "err", err, "retry_in", delay.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		backoff = min(backoff*2, maxB)
	}
}

// session runs one connection. listening reports whether the node dump was
// received, i.e. whether Handler.Connected was called.
func (c *Client) session(ctx context.Context, logger *slog.Logger) (listening bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dialCtx, c.URL, nil)
	dialCancel()
	if err != nil {
		return false, fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()
	// The node dump is a single frame and grows with the number of nodes.
	conn.SetReadLimit(-1)

	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, 60*time.Second)
	defer handshakeCancel()

	_, data, err := conn.Read(handshakeCtx)
	if err != nil {
		return false, fmt.Errorf("read server info: %w", err)
	}
	var info ServerInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return false, fmt.Errorf("decode server info: %w", err)
	}

	req := fmt.Sprintf(`{"message_id":%q,"command":"start_listening","args":{}}`, startListeningID)
	if err := conn.Write(handshakeCtx, websocket.MessageText, []byte(req)); err != nil {
		return false, fmt.Errorf("send start_listening: %w", err)
	}

	go c.keepalive(ctx, cancel, conn)

	for {
		readCtx := ctx
		if !listening {
			readCtx = handshakeCtx
		}
		_, data, err := conn.Read(readCtx)
		if err != nil {
			if listening {
				c.Handler.Disconnected()
			}
			return listening, fmt.Errorf("read: %w", err)
		}
		var msg message
		if err := json.Unmarshal(data, &msg); err != nil {
			logger.Warn("ignoring undecodable message", "err", err)
			continue
		}

		switch {
		case msg.MessageID == startListeningID && !listening:
			if msg.ErrorCode != nil {
				return false, fmt.Errorf("start_listening failed: error_code=%d details=%q", *msg.ErrorCode, msg.Details)
			}
			var nodes []Node
			if err := json.Unmarshal(msg.Result, &nodes); err != nil {
				return false, fmt.Errorf("decode node dump: %w", err)
			}
			listening = true
			logger.Info("listening to Matter server", "url", c.URL,
				"sdk_version", info.SDKVersion, "schema_version", info.SchemaVersion, "nodes", len(nodes))
			c.Handler.Connected(info, nodes)

		case msg.Event != "":
			if !listening {
				// The dump that follows is at least as fresh as this event.
				logger.Debug("dropping event received before the node dump", "event", msg.Event)
				continue
			}
			c.Handler.Event(Event{Type: msg.Event, Data: msg.Data})
			if msg.Event == EventServerShutdown {
				c.Handler.Disconnected()
				conn.Close(websocket.StatusNormalClosure, "")
				return true, errors.New("server is shutting down")
			}

		default:
			logger.Debug("ignoring unexpected message", "message_id", msg.MessageID)
		}
	}
}

// keepalive pings the server so that a silently dead connection is noticed
// even when no events flow. It cancels the session on failure.
func (c *Client) keepalive(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn) {
	interval := c.PingInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, interval)
			err := conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				cancel()
				return
			}
		}
	}
}
