// Package statetest provides fixtures for tests of packages that consume
// state snapshots.
package statetest

import (
	"encoding/json"
	"testing"

	"github.com/giskarda/matter_exporter/internal/state"
)

// Source is a state.Source serving a snapshot the test controls.
type Source struct {
	Snap state.Snapshot
}

// Snapshot implements state.Source.
func (s *Source) Snapshot() state.Snapshot { return s.Snap }

// Node builds a node from attribute path ("endpoint/cluster/attribute") to
// raw JSON value pairs.
func Node(t testing.TB, id uint64, available bool, attrs map[string]string) state.Node {
	t.Helper()
	n := state.Node{ID: id, Available: available, Attrs: make(map[state.Path]json.RawMessage, len(attrs))}
	for path, raw := range attrs {
		p, err := state.ParsePath(path)
		if err != nil {
			t.Fatal(err)
		}
		n.Attrs[p] = json.RawMessage(raw)
	}
	return n
}
