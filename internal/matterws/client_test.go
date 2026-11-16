package matterws_test

import (
	"bytes"
	"sync"
	"testing"

	"github.com/giskarda/matter_exporter/internal/matterws"
	"github.com/giskarda/matter_exporter/internal/matterws/wstest"
)

const capturePath = "../../testdata/capture.jsonl"

// recorder is a Handler that records what it is told.
type recorder struct {
	mu          sync.Mutex
	infos       []matterws.ServerInfo
	dumps       [][]matterws.Node
	events      []matterws.Event
	disconnects int
}

func (r *recorder) Connected(info matterws.ServerInfo, nodes []matterws.Node) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.infos = append(r.infos, info)
	r.dumps = append(r.dumps, nodes)
}

func (r *recorder) Event(ev matterws.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) Disconnected() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disconnects++
}

func (r *recorder) counts() (dumps, events, disconnects int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.dumps), len(r.events), r.disconnects
}

func TestClientReplaysCapture(t *testing.T) {
	capture := wstest.LoadCapture(t, capturePath)
	srv := wstest.ReplayOnce(t, capture)
	rec := &recorder{}
	wstest.Run(t, srv.URL(), rec, nil)

	wantEvents := len(capture) - 2
	wstest.WaitFor(t, "all events", func() bool { _, n, _ := rec.counts(); return n == wantEvents })

	rec.mu.Lock()
	defer rec.mu.Unlock()

	if len(rec.dumps) != 1 {
		t.Fatalf("Connected called %d times, want 1", len(rec.dumps))
	}
	info := rec.infos[0]
	if info.SchemaVersion != 14 || info.SDKVersion != "matter-server/1.5.5 (matter.js/0.18.0)" {
		t.Errorf("unexpected server info: %+v", info)
	}
	// Above 2^53: must not have gone through a float64.
	if info.CompressedFabricID != 16505699021504046171 {
		t.Errorf("compressed fabric id = %d", info.CompressedFabricID)
	}

	nodes := rec.dumps[0]
	var ids []uint64
	for _, n := range nodes {
		ids = append(ids, n.NodeID)
		if !n.Available {
			t.Errorf("node %d: want available", n.NodeID)
		}
	}
	if len(ids) != 5 || ids[0] != 4 || ids[4] != 8 {
		t.Fatalf("node ids = %v, want [4 5 6 7 8]", ids)
	}
	if got := string(nodes[0].Attributes["0/40/1"]); got != `"Eve Systems"` {
		t.Errorf("node 4 vendor = %s", got)
	}

	// First event of the capture.
	u, err := rec.events[0].AttributeUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if rec.events[0].Type != matterws.EventAttributeUpdated || u.NodeID != 5 || u.Path != "2/144/4" || string(u.Value) != "211500" {
		t.Errorf("first event = %s %+v", rec.events[0].Type, u)
	}

	// The capture contains exactly one node_event.
	var nodeEvents []matterws.NodeEvent
	for _, ev := range rec.events {
		if ev.Type == matterws.EventNodeEvent {
			ne, err := ev.NodeEvent()
			if err != nil {
				t.Fatal(err)
			}
			nodeEvents = append(nodeEvents, ne)
		}
	}
	if len(nodeEvents) != 1 {
		t.Fatalf("got %d node_events, want 1", len(nodeEvents))
	}
	if ne := nodeEvents[0]; ne.NodeID != 5 || ne.EndpointID != 2 || ne.ClusterID != 145 || ne.EventID != 0 {
		t.Errorf("node_event = %+v", ne)
	}
}

func TestClientReconnects(t *testing.T) {
	capture := wstest.LoadCapture(t, capturePath)
	// First connection: dump, one event, then the server hangs up.
	// Later connections: dump only, stay up.
	srv := wstest.Replay(t, func(conn int) ([][]byte, bool) {
		if conn == 1 {
			return capture[:3], true
		}
		return capture[:2], false
	})
	rec := &recorder{}
	wstest.Run(t, srv.URL(), rec, nil)

	wstest.WaitFor(t, "second dump", func() bool { d, _, _ := rec.counts(); return d == 2 })
	dumps, events, disconnects := rec.counts()
	if dumps != 2 || events != 1 || disconnects != 1 {
		t.Errorf("dumps=%d events=%d disconnects=%d, want 2, 1, 1", dumps, events, disconnects)
	}
}

func TestClientServerMessages(t *testing.T) {
	info := []byte(`{"schema_version":14,"sdk_version":"test"}`)
	dump := []byte(`{"message_id":"1","result":[]}`)

	tests := []struct {
		name string
		// first is the session of the first connection; later ones get a
		// plain empty dump.
		first           [][]byte
		wantDumps       int
		wantEvents      int
		wantDisconnects int
	}{
		{
			name:      "start_listening error is retried",
			first:     [][]byte{info, []byte(`{"message_id":"1","error_code":5,"details":"nope"}`)},
			wantDumps: 1, wantEvents: 0, wantDisconnects: 0,
		},
		{
			name:      "garbage server info is retried",
			first:     [][]byte{[]byte(`not json`)},
			wantDumps: 1, wantEvents: 0, wantDisconnects: 0,
		},
		{
			name: "events before the dump are dropped",
			first: [][]byte{info,
				[]byte(`{"event":"attribute_updated","data":[1,"0/1/2",3]}`),
				dump,
				[]byte(`{"event":"node_removed","data":1}`),
				[]byte(`{"event":"server_shutdown","data":null}`),
			},
			// The shutdown is delivered, then the client reconnects.
			wantDumps: 2, wantEvents: 2, wantDisconnects: 1,
		},
		{
			name: "undecodable and unexpected messages are skipped",
			first: [][]byte{info, dump,
				[]byte(`[1,2,3]`),
				[]byte(`{"message_id":"99","result":null}`),
				[]byte(`{"event":"node_removed","data":1}`),
				[]byte(`{"event":"server_shutdown","data":null}`),
			},
			wantDumps: 2, wantEvents: 2, wantDisconnects: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := wstest.Replay(t, func(conn int) ([][]byte, bool) {
				if conn == 1 {
					return tt.first, true
				}
				return [][]byte{info, dump}, false
			})
			rec := &recorder{}
			wstest.Run(t, srv.URL(), rec, nil)

			wstest.WaitFor(t, "expected dumps", func() bool { d, _, _ := rec.counts(); return d == tt.wantDumps })
			if srv.Connections.Load() < 2 {
				t.Errorf("expected a reconnection, got %d connections", srv.Connections.Load())
			}
			dumps, events, disconnects := rec.counts()
			if dumps != tt.wantDumps || events != tt.wantEvents || disconnects != tt.wantDisconnects {
				t.Errorf("dumps=%d events=%d disconnects=%d, want %d, %d, %d",
					dumps, events, disconnects, tt.wantDumps, tt.wantEvents, tt.wantDisconnects)
			}
		})
	}
}

func TestClientMessageSizeLimit(t *testing.T) {
	info := []byte(`{"schema_version":14,"sdk_version":"test"}`)
	dump := []byte(`{"message_id":"1","result":[]}`)
	oversized := append(append([]byte(`{"event":"attribute_updated","data":[1,"0/40/5","`),
		bytes.Repeat([]byte("x"), 4096)...), `"]}`...)

	// First connection: an oversized event. Later ones: nothing after the dump.
	srv := wstest.Replay(t, func(conn int) ([][]byte, bool) {
		if conn == 1 {
			return [][]byte{info, dump, oversized}, false
		}
		return [][]byte{info, dump}, false
	})
	rec := &recorder{}
	wstest.Run(t, srv.URL(), rec, func(c *matterws.Client) { c.MaxMessageBytes = 1024 })

	wstest.WaitFor(t, "reconnection", func() bool { d, _, _ := rec.counts(); return d == 2 })
	dumps, events, disconnects := rec.counts()
	if dumps != 2 || events != 0 || disconnects != 1 {
		t.Errorf("dumps=%d events=%d disconnects=%d, want 2, 0, 1: the oversized message must end the connection undelivered",
			dumps, events, disconnects)
	}
}
