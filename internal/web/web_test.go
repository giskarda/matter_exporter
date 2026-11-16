package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/giskarda/matter_exporter/internal/state"
	"github.com/giskarda/matter_exporter/internal/state/statetest"
)

func get(t *testing.T, h http.Handler) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Code, rec.Body.String()
}

func TestDevicesHandler(t *testing.T) {
	nodes := []state.Node{
		statetest.Node(t, 4, true, map[string]string{
			"0/40/1": `"Eve Systems"`, "0/40/3": `"Eve Energy"`, "0/40/5": `"<b>plug</b>"`,
			"0/40/15": `"SN1"`, "0/40/18": `"UID-4"`,
		}),
		statetest.Node(t, 6, false, map[string]string{"0/40/3": `"Sensor"`, "0/40/18": `"UID-6"`}),
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
			contains: []string{`<td colspan="7">No nodes.</td>`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := get(t, DevicesHandler(&statetest.Source{Snap: tt.snap}, tt.room))
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
