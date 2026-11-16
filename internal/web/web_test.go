package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/giskarda/matter_exporter/internal/state"
)

type fakeSource struct{ snap state.Snapshot }

func (f fakeSource) Snapshot() state.Snapshot { return f.snap }

func node(id uint64, available bool, attrs map[uint32]string) state.Node {
	n := state.Node{ID: id, Available: available, Attrs: map[state.Path]json.RawMessage{}}
	for attr, v := range attrs {
		raw, _ := json.Marshal(v)
		n.Attrs[state.Path{Endpoint: 0, Cluster: state.ClusterBasicInfo, Attribute: attr}] = raw
	}
	return n
}

func get(t *testing.T, h http.Handler) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Code, rec.Body.String()
}

func TestDevicesHandler(t *testing.T) {
	nodes := []state.Node{
		node(4, true, map[uint32]string{1: "Eve Systems", 3: "Eve Energy", 5: "<b>plug</b>", 15: "SN1", 18: "UID-4"}),
		node(6, false, map[uint32]string{3: "Sensor", 18: "UID-6"}),
	}
	rooms := func(n state.Node) string {
		if n.ID == 4 {
			return "kitchen"
		}
		return "unassigned"
	}

	tests := []struct {
		name        string
		snap        state.Snapshot
		room        RoomFunc
		contains    []string
		notContains []string
	}{
		{
			name: "with rooms",
			snap: state.Snapshot{Connected: true, Nodes: nodes},
			room: rooms,
			contains: []string{
				"<td>4</td>", "UID-4", "SN1", "Eve Systems", "Eve Energy", "<th>room</th>",
				"<td>kitchen</td>", "<td>unassigned</td>", "UID-6", "<td>yes</td>", "<td>no</td>",
				"&lt;b&gt;plug&lt;/b&gt;", // NodeLabel is device-controlled text: must be escaped
			},
			notContains: []string{"<b>plug</b>", "Not connected"},
		},
		{
			name:        "enrichment disabled",
			snap:        state.Snapshot{Connected: true, Nodes: nodes},
			contains:    []string{"UID-4", "UID-6"},
			notContains: []string{"<th>room</th>", "kitchen"},
		},
		{
			name:     "disconnected shows the last known state with a warning",
			snap:     state.Snapshot{Connected: false, Nodes: nodes},
			contains: []string{"Not connected", "UID-4"},
		},
		{
			name:     "no nodes",
			snap:     state.Snapshot{Connected: true},
			contains: []string{"No nodes."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := get(t, DevicesHandler(fakeSource{tt.snap}, tt.room))
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			for _, s := range tt.contains {
				if !strings.Contains(body, s) {
					t.Errorf("body does not contain %q", s)
				}
			}
			for _, s := range tt.notContains {
				if strings.Contains(body, s) {
					t.Errorf("body contains %q", s)
				}
			}
		})
	}
}

func TestHealthyHandler(t *testing.T) {
	code, body := get(t, HealthyHandler())
	if code != http.StatusOK || !strings.Contains(body, "healthy") {
		t.Errorf("status = %d, body = %q", code, body)
	}
}
