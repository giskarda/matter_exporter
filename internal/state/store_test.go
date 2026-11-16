package state

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/giskarda/matter_exporter/internal/matterws"
	"github.com/giskarda/matter_exporter/internal/matterws/wstest"
)

func newTestStore() *Store {
	return New(slog.New(slog.DiscardHandler))
}

// wsNode builds a wire node from "path": value pairs.
func wsNode(id uint64, available bool, attrs map[string]string) matterws.Node {
	n := matterws.Node{NodeID: id, Available: available, Attributes: map[string]json.RawMessage{}}
	for k, v := range attrs {
		n.Attributes[k] = json.RawMessage(v)
	}
	return n
}

func event(typ, data string) matterws.Event {
	return matterws.Event{Type: typ, Data: json.RawMessage(data)}
}

func nodeJSON(id uint64, available bool, attrs string) string {
	return fmt.Sprintf(`{"node_id":%d,"available":%t,"attributes":{%s}}`, id, available, attrs)
}

func TestParsePath(t *testing.T) {
	tests := []struct {
		in      string
		want    Path
		wantErr bool
	}{
		{in: "0/40/1", want: Path{0, 40, 1}},
		{in: "2/1029/0", want: Path{2, 1029, 0}},
		{in: "1/319486977/65533", want: Path{1, 319486977, 65533}},
		{in: "0/40", wantErr: true},
		{in: "0/40/1/2", wantErr: true},
		{in: "a/40/1", wantErr: true},
		{in: "0/-1/1", wantErr: true},
		{in: "70000/40/1", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParsePath(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %t", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
			if err == nil && got.String() != tt.in {
				t.Errorf("String() = %q, want %q", got.String(), tt.in)
			}
		})
	}
}

func TestNodeAccessors(t *testing.T) {
	s := newTestStore()
	s.Connected(matterws.ServerInfo{}, []matterws.Node{wsNode(1, true, map[string]string{
		"0/29/0":   `[{"0":22,"1":2},{"0":17,"1":1},{"0":22,"1":2}]`,
		"0/29/1":   `[29,40,53]`,
		"0/40/1":   `"ACME"`,
		"0/40/2":   `4874`,
		"0/40/5":   `""`,
		"0/40/15":  `null`,
		"0/40/18":  `"ABCDEF"`,
		"1/29/1":   `[1026]`,
		"1/1026/0": `1993`,
		"1/1026/1": `null`,
		"bogus":    `1`,
	})})
	n := s.Snapshot().Nodes[0]

	if got := n.Endpoints(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("Endpoints() = %v", got)
	}
	if got := n.DeviceTypes(0); len(got) != 2 || got[0] != 17 || got[1] != 22 {
		t.Errorf("DeviceTypes(0) = %v, want [17 22]", got)
	}
	if !n.HasCluster(0, 53) || n.HasCluster(0, 1026) || !n.HasCluster(1, 1026) || n.HasCluster(9, 29) {
		t.Error("HasCluster mismatch")
	}
	if f, ok := n.Float(1, 1026, 0); !ok || f != 1993 {
		t.Errorf("Float = %v, %t", f, ok)
	}
	if _, ok := n.Float(1, 1026, 1); ok {
		t.Error("null attribute must be reported as unknown")
	}
	if _, ok := n.Float(1, 1026, 2); ok {
		t.Error("absent attribute must be reported as unknown")
	}
	want := BasicInfo{VendorName: "ACME", VendorID: "4874", UniqueID: "ABCDEF"}
	if got := n.BasicInfo(); got != want {
		t.Errorf("BasicInfo() = %+v, want %+v", got, want)
	}
}

func TestStoreEvents(t *testing.T) {
	contactNode := func(available bool, state string) string {
		return nodeJSON(1, available, `"1/69/0":`+state)
	}

	type want struct {
		nodes        []uint64
		availChanges map[AvailabilityKey]uint64
		contact      map[EndpointKey]uint64
		switches     map[SwitchEventKey]uint64
	}
	tests := []struct {
		name   string
		events []matterws.Event
		check  func(t *testing.T, snap Snapshot)
		want   want
	}{
		{
			name:   "node_added adds a node without counting a transition",
			events: []matterws.Event{event("node_added", nodeJSON(2, true, ""))},
			want:   want{nodes: []uint64{1, 2}},
		},
		{
			name: "node_updated counts availability transitions",
			events: []matterws.Event{
				event("node_updated", contactNode(false, "false")),
				event("node_updated", contactNode(false, "false")),
				event("node_updated", contactNode(true, "false")),
				event("node_updated", contactNode(false, "false")),
			},
			want: want{nodes: []uint64{1}, availChanges: map[AvailabilityKey]uint64{
				{1, "unavailable"}: 2, {1, "available"}: 1,
			}},
		},
		{
			name: "attribute_updated replaces the value",
			events: []matterws.Event{
				event("attribute_updated", `[1,"1/1026/0",2105]`),
				event("attribute_updated", `[1,"3/1026/0",null]`),
			},
			check: func(t *testing.T, snap Snapshot) {
				n := snap.Nodes[0]
				if f, ok := n.Float(1, 1026, 0); !ok || f != 2105 {
					t.Errorf("1/1026/0 = %v, %t", f, ok)
				}
				if _, ok := n.Float(3, 1026, 0); ok {
					t.Error("3/1026/0: null must be unknown")
				}
			},
			want: want{nodes: []uint64{1}},
		},
		{
			name: "contact changes count real transitions only",
			events: []matterws.Event{
				event("attribute_updated", `[1,"1/69/0",false]`), // same value
				event("attribute_updated", `[1,"1/69/0",true]`),
				event("attribute_updated", `[1,"1/69/0",null]`), // unknown
				event("attribute_updated", `[1,"1/69/0",false]`),
				event("attribute_updated", `[1,"1/69/0",true]`),
				event("attribute_updated", `[1,"2/69/0",true]`), // first value on a new endpoint
				event("node_updated", contactNode(true, "false")),
			},
			want: want{nodes: []uint64{1}, contact: map[EndpointKey]uint64{{1, 1}: 3}},
		},
		{
			name: "switch events are counted by type",
			events: []matterws.Event{
				event("node_event", `{"node_id":1,"endpoint_id":3,"cluster_id":59,"event_id":1,"data":{"newPosition":1}}`),
				event("node_event", `{"node_id":1,"endpoint_id":3,"cluster_id":59,"event_id":3,"data":{"previousPosition":1}}`),
				event("node_event", `{"node_id":1,"endpoint_id":3,"cluster_id":59,"event_id":1,"data":{"newPosition":1}}`),
				event("node_event", `{"node_id":1,"endpoint_id":3,"cluster_id":59,"event_id":99,"data":{}}`), // unknown event
				event("node_event", `{"node_id":1,"endpoint_id":2,"cluster_id":145,"event_id":0,"data":{}}`), // other cluster
				event("node_event", `{"node_id":7,"endpoint_id":3,"cluster_id":59,"event_id":1,"data":{}}`),  // unknown node
			},
			want: want{nodes: []uint64{1}, switches: map[SwitchEventKey]uint64{
				{1, 3, "initial_press"}: 2, {1, 3, "short_release"}: 1,
			}},
		},
		{
			name: "node_removed drops the node and its counters",
			events: []matterws.Event{
				event("attribute_updated", `[1,"1/69/0",true]`),
				event("node_updated", contactNode(false, "true")),
				event("node_event", `{"node_id":1,"endpoint_id":3,"cluster_id":59,"event_id":1,"data":{}}`),
				event("node_removed", `1`),
			},
			want: want{nodes: []uint64{}},
		},
		{
			name: "endpoint_removed drops the endpoint",
			events: []matterws.Event{
				event("attribute_updated", `[1,"1/69/0",true]`),
				event("endpoint_removed", `{"node_id":1,"endpoint_id":1}`),
			},
			check: func(t *testing.T, snap Snapshot) {
				if eps := snap.Nodes[0].Endpoints(); len(eps) != 1 || eps[0] != 0 {
					t.Errorf("endpoints = %v, want [0]", eps)
				}
			},
			want: want{nodes: []uint64{1}},
		},
		{
			name: "malformed and unknown events are ignored",
			events: []matterws.Event{
				event("attribute_updated", `[1,"1/69"]`),
				event("attribute_updated", `[1,"nope",1]`),
				event("attribute_updated", `[42,"1/69/0",true]`),
				event("node_updated", `"x"`),
				event("node_removed", `{}`),
				event("something_new", `{}`),
			},
			want: want{nodes: []uint64{1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore()
			s.Connected(matterws.ServerInfo{}, []matterws.Node{wsNode(1, true, map[string]string{
				"0/40/1": `"ACME"`,
				"1/69/0": `false`,
			})})
			for _, ev := range tt.events {
				s.Event(ev)
			}
			snap := s.Snapshot()

			var ids []uint64
			for _, n := range snap.Nodes {
				ids = append(ids, n.ID)
			}
			if fmt.Sprint(ids) != fmt.Sprint(tt.want.nodes) {
				t.Errorf("nodes = %v, want %v", ids, tt.want.nodes)
			}
			compareCounters(t, "availability changes", snap.AvailabilityChanges, tt.want.availChanges)
			compareCounters(t, "contact changes", snap.ContactChanges, tt.want.contact)
			compareCounters(t, "switch events", snap.SwitchEvents, tt.want.switches)

			var total uint64
			for _, n := range snap.Events {
				total += n
			}
			if total != uint64(len(tt.events)) {
				t.Errorf("events counted = %d, want %d", total, len(tt.events))
			}
			if tt.check != nil {
				tt.check(t, snap)
			}
		})
	}
}

func compareCounters[K comparable](t *testing.T, what string, got, want map[K]uint64) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
		return
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", what, got, want)
			return
		}
	}
}

func TestStoreReconnect(t *testing.T) {
	s := newTestStore()
	now := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return now }

	if snap := s.Snapshot(); snap.Connected || len(snap.Nodes) != 0 || !snap.LastEvent.IsZero() {
		t.Fatalf("fresh store: %+v", snap)
	}

	s.Connected(matterws.ServerInfo{}, []matterws.Node{
		wsNode(1, true, map[string]string{"1/69/0": `false`}),
		wsNode(2, true, map[string]string{"1/69/0": `false`}),
	})
	s.Event(event("attribute_updated", `[1,"1/69/0",true]`))
	s.Event(event("attribute_updated", `[2,"1/69/0",true]`))
	s.Disconnected()

	snap := s.Snapshot()
	if snap.Connected || len(snap.Nodes) != 2 || snap.Reconnects != 0 {
		t.Fatalf("after disconnect: connected=%t nodes=%d reconnects=%d", snap.Connected, len(snap.Nodes), snap.Reconnects)
	}
	if !snap.LastEvent.Equal(now) {
		t.Errorf("LastEvent = %v, want %v", snap.LastEvent, now)
	}

	// While we were away: node 2 was removed, node 1 went offline and its
	// contact opened, node 3 was commissioned.
	s.Connected(matterws.ServerInfo{}, []matterws.Node{
		wsNode(1, false, map[string]string{"1/69/0": `false`}),
		wsNode(3, true, map[string]string{"1/69/0": `true`}),
	})
	snap = s.Snapshot()
	if !snap.Connected || snap.Reconnects != 1 {
		t.Errorf("connected=%t reconnects=%d, want true, 1", snap.Connected, snap.Reconnects)
	}
	if len(snap.Nodes) != 2 || snap.Nodes[0].ID != 1 || snap.Nodes[1].ID != 3 {
		t.Errorf("nodes after reconnect: %+v", snap.Nodes)
	}
	compareCounters(t, "contact changes", snap.ContactChanges, map[EndpointKey]uint64{{1, 1}: 2})
	compareCounters(t, "availability changes", snap.AvailabilityChanges, map[AvailabilityKey]uint64{{1, "unavailable"}: 1})
}

func TestSnapshotIsIsolated(t *testing.T) {
	s := newTestStore()
	s.Connected(matterws.ServerInfo{}, []matterws.Node{wsNode(1, true, map[string]string{"1/1026/0": `1000`})})
	before := s.Snapshot()
	s.Event(event("attribute_updated", `[1,"1/1026/0",2000]`))
	s.Event(event("node_removed", `1`))

	if len(before.Nodes) != 1 {
		t.Fatal("snapshot lost its node")
	}
	if f, _ := before.Nodes[0].Float(1, 1026, 0); f != 1000 {
		t.Errorf("snapshot value changed to %v", f)
	}
	if len(before.Events) != 0 {
		t.Errorf("snapshot counters changed: %v", before.Events)
	}
}

// TestReplayCapture feeds the store through the real client from the fake
// server replaying the live capture.
func TestReplayCapture(t *testing.T) {
	capture := wstest.LoadCapture(t, "../../testdata/capture.jsonl")
	srv := wstest.ReplayOnce(t, capture)
	s := newTestStore()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	client := &matterws.Client{URL: srv.URL(), Handler: s, Logger: slog.New(slog.DiscardHandler)}
	go func() {
		defer close(done)
		_ = client.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	wantEvents := uint64(len(capture) - 2)
	deadline := time.Now().Add(5 * time.Second)
	var snap Snapshot
	for {
		snap = s.Snapshot()
		var total uint64
		for _, n := range snap.Events {
			total += n
		}
		if total == wantEvents {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %d of %d events applied", total, wantEvents)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if !snap.Connected || len(snap.Nodes) != 5 {
		t.Fatalf("connected=%t nodes=%d", snap.Connected, len(snap.Nodes))
	}
	if snap.Events["attribute_updated"] != 25 || snap.Events["node_event"] != 1 {
		t.Errorf("events = %v", snap.Events)
	}

	byID := map[uint64]Node{}
	for _, n := range snap.Nodes {
		byID[n.ID] = n
	}
	// Last values in the capture win over the dump.
	finals := []struct {
		node     uint64
		endpoint uint16
		cluster  uint32
		attr     uint32
		want     float64
	}{
		{4, 2, 144, 4, 212000},
		{5, 2, 144, 8, 79300},
		{6, 1, 1026, 0, 2001},
		{8, 2, 1029, 0, 5857},
	}
	for _, f := range finals {
		got, ok := byID[f.node].Float(f.endpoint, f.cluster, f.attr)
		if !ok || got != f.want {
			t.Errorf("node %d %d/%d/%d = %v (%t), want %v", f.node, f.endpoint, f.cluster, f.attr, got, ok, f.want)
		}
	}
	if bi := byID[6].BasicInfo(); bi.VendorName != "IKEA of Sweden" || bi.ProductName != "TIMMERFLOTTE temp/hmd sensor" || bi.UniqueID == "" {
		t.Errorf("node 6 basic info = %+v", bi)
	}
}
