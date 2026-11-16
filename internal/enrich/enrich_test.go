package enrich

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/giskarda/matter_exporter/internal/state"
)

type fakeSource struct{ snap state.Snapshot }

func (f fakeSource) Snapshot() state.Snapshot { return f.snap }

func node(id uint64, uniqueID, serial string) state.Node {
	n := state.Node{ID: id, Available: true, Attrs: map[state.Path]json.RawMessage{}}
	set := func(attr uint32, v string) {
		if v != "" {
			raw, _ := json.Marshal(v)
			n.Attrs[state.Path{Endpoint: 0, Cluster: state.ClusterBasicInfo, Attribute: attr}] = raw
		}
	}
	set(18, uniqueID)
	set(15, serial)
	return n
}

const roomsYAML = `
rooms:
  living_room:
    - "4F2A9C1E7B3D5A60"
    - sn-plug-1
  bathroom:
    - "9b1d3e5f7a2c4e68"
  bedroom:
    - 7
    - 17365112704004802477
`

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantIDs int
		wantErr string
	}{
		{name: "valid", yaml: roomsYAML, wantIDs: 5},
		{name: "empty file", yaml: ``, wantIDs: 0},
		{name: "no rooms", yaml: `rooms: {}`, wantIDs: 0},
		{name: "duplicate identifier", yaml: "rooms:\n  a: [X1]\n  b: [x1]\n", wantErr: "listed under both"},
		{name: "empty identifier", yaml: "rooms:\n  a: [\"\"]\n", wantErr: "empty identifier"},
		{name: "wrong shape", yaml: "rooms: [a, b]\n", wantErr: "cannot unmarshal"},
		{name: "not yaml", yaml: "rooms: [", wantErr: "yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Parse([]byte(tt.yaml))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(m.byID) != tt.wantIDs {
				t.Errorf("got %d identifiers, want %d", len(m.byID), tt.wantIDs)
			}
		})
	}
}

func TestLookup(t *testing.T) {
	m, err := Parse([]byte(roomsYAML))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		node     state.Node
		wantRoom string
		wantKind matchKind
	}{
		{"by unique id", node(1, "4F2A9C1E7B3D5A60", "whatever"), "living_room", matchUniqueID},
		{"unique id is case-insensitive", node(2, "9B1D3E5F7A2C4E68", ""), "bathroom", matchUniqueID},
		{"by serial", node(3, "UNKNOWN", "SN-PLUG-1"), "living_room", matchSerial},
		{"numeric serial written unquoted", node(4, "", "17365112704004802477"), "bedroom", matchSerial},
		{"by node id", node(7, "UNKNOWN", "UNKNOWN"), "bedroom", matchNodeID},
		{"unique id wins over node id", node(7, "9b1d3e5f7a2c4e68", ""), "bathroom", matchUniqueID},
		{"unmapped", node(9, "NOPE", "NOPE"), Unassigned, matchNone},
		{"no metadata", node(10, "", ""), Unassigned, matchNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			room, kind := m.lookup(tt.node)
			if room != tt.wantRoom || kind != tt.wantKind {
				t.Errorf("lookup = %q, %v; want %q, %v", room, kind, tt.wantRoom, tt.wantKind)
			}
		})
	}
}

func TestEnricher(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rooms.yaml")
	if err := os.WriteFile(path, []byte(roomsYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	src := &fakeSource{snap: state.Snapshot{Connected: true, Nodes: []state.Node{
		node(1, "4F2A9C1E7B3D5A60", ""),
		node(7, "", ""),
		node(9, "NOPE", ""),
	}}}
	e, err := New(path, src, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}

	const header = `
# HELP matter_node_room Room a node is assigned to in the rooms file; always 1.
# TYPE matter_node_room gauge
`
	want := header + `
matter_node_room{node_id="1",room="living_room"} 1
matter_node_room{node_id="7",room="bedroom"} 1
matter_node_room{node_id="9",room="unassigned"} 1
`
	for range 2 {
		if err := testutil.CollectAndCompare(e, strings.NewReader(want)); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(logs.String(), "room matched by node_id"); n != 1 {
		t.Errorf("node_id fallback warned %d times, want once:\n%s", n, logs.String())
	}

	// A broken file keeps the previous mapping.
	if err := os.WriteFile(path, []byte("rooms: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.Reload(); err == nil {
		t.Fatal("Reload of a broken file: want error")
	}
	if err := testutil.CollectAndCompare(e, strings.NewReader(want)); err != nil {
		t.Fatalf("after failed reload: %v", err)
	}

	// A valid reload replaces it.
	if err := os.WriteFile(path, []byte("rooms:\n  attic: [NOPE]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.Reload(); err != nil {
		t.Fatal(err)
	}
	want = header + `
matter_node_room{node_id="1",room="unassigned"} 1
matter_node_room{node_id="7",room="unassigned"} 1
matter_node_room{node_id="9",room="attic"} 1
`
	if err := testutil.CollectAndCompare(e, strings.NewReader(want)); err != nil {
		t.Fatalf("after reload: %v", err)
	}

	// Nothing is exported while disconnected.
	src.snap.Connected = false
	if n := testutil.CollectAndCount(e); n != 0 {
		t.Errorf("disconnected: %d metrics, want 0", n)
	}
}

func TestNewMissingFile(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "missing.yaml"), fakeSource{}, nil); err == nil {
		t.Fatal("want error for a missing file")
	}
}
