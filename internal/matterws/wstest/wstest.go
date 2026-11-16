// Package wstest provides a fake Matter server that replays captured
// sessions, for tests.
package wstest

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/giskarda/matter_exporter/internal/matterws"
)

// LoadCapture reads a JSONL capture: one WebSocket message per line, the
// first being the server info and the second the start_listening result.
func LoadCapture(t testing.TB, path string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines [][]byte
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) > 0 {
			lines = append(lines, bytes.Clone(sc.Bytes()))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// Server is a fake Matter server.
type Server struct {
	srv *httptest.Server
	// Connections counts the WebSocket connections accepted so far.
	Connections atomic.Int32
}

// URL returns the ws:// URL of the server.
func (s *Server) URL() string {
	return "ws" + strings.TrimPrefix(s.srv.URL, "http")
}

// Replay starts a server that, on every connection, sends the first message
// of the session, waits for the client's command, then sends the rest.
// session is called with the 1-based connection number and returns the
// messages to play and whether to hang up afterwards (otherwise the
// connection stays open until the client leaves or the test ends).
func Replay(t testing.TB, session func(conn int) (messages [][]byte, hangUp bool)) *Server {
	t.Helper()
	s := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		n := int(s.Connections.Add(1))
		messages, hangUp := session(n)
		if len(messages) == 0 {
			return
		}
		if err := c.Write(ctx, websocket.MessageText, messages[0]); err != nil {
			return
		}
		// Wait for start_listening.
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
		for _, m := range messages[1:] {
			if err := c.Write(ctx, websocket.MessageText, m); err != nil {
				return
			}
		}
		if hangUp {
			c.Close(websocket.StatusGoingAway, "bye")
			return
		}
		// Keep reading so that pings are answered, until the peer leaves.
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}))
	t.Cleanup(func() {
		cancel()
		s.srv.Close()
	})
	return s
}

// ReplayOnce is Replay with the same session on every connection, never
// hanging up.
func ReplayOnce(t testing.TB, messages [][]byte) *Server {
	return Replay(t, func(int) ([][]byte, bool) { return messages, false })
}

// Run starts a client against url with fast reconnects and stops it when the
// test ends. configure, if not nil, can adjust the client before it starts.
func Run(t testing.TB, url string, h matterws.Handler, configure func(*matterws.Client)) {
	t.Helper()
	c := &matterws.Client{
		URL:        url,
		Handler:    h,
		Logger:     slog.New(slog.DiscardHandler),
		MinBackoff: time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
	}
	if configure != nil {
		configure(c)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// WaitFor polls cond until it holds, failing the test after a few seconds.
func WaitFor(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
