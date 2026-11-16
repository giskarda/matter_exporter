// Package enrich adds the optional room dimension, which Matter itself does
// not have. It is independent of the core collector: it reads a rooms.yaml
// keyed by stable device identifiers and exposes matter_node_room.
package enrich

import (
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"gopkg.in/yaml.v3"

	"github.com/giskarda/matter_exporter/internal/state"
)

// Unassigned is the room of nodes that are not in the mapping file.
const Unassigned = "unassigned"

// Source provides state snapshots; *state.Store implements it.
type Source interface {
	Snapshot() state.Snapshot
}

// file is the on-disk format:
//
//	rooms:
//	  living_room:
//	    - "4F2A9C1E7B3D5A60"
type file struct {
	Rooms map[string][]string `yaml:"rooms"`
}

// Mapping resolves device identifiers to rooms.
type Mapping struct {
	byID map[string]string // normalised identifier → room
}

// Parse reads a rooms.yaml document. An identifier listed under several
// rooms is an error.
func Parse(data []byte) (*Mapping, error) {
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	m := &Mapping{byID: map[string]string{}}
	rooms := make([]string, 0, len(f.Rooms))
	for room := range f.Rooms {
		rooms = append(rooms, room)
	}
	slices.Sort(rooms)
	for _, room := range rooms {
		if room == "" {
			return nil, fmt.Errorf("empty room name")
		}
		for _, id := range f.Rooms[room] {
			key := normalise(id)
			if key == "" {
				return nil, fmt.Errorf("room %q: empty identifier", room)
			}
			if other, dup := m.byID[key]; dup {
				return nil, fmt.Errorf("identifier %q is listed under both %q and %q", id, other, room)
			}
			m.byID[key] = room
		}
	}
	return m, nil
}

func normalise(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// matchKind says which identifier of a node matched the mapping.
type matchKind int

const (
	matchNone matchKind = iota
	matchUniqueID
	matchSerial
	matchNodeID
)

// lookup resolves a node: by UniqueID first, then SerialNumber, then node_id.
func (m *Mapping) lookup(n state.Node) (string, matchKind) {
	bi := n.BasicInfo()
	if bi.UniqueID != "" {
		if room, ok := m.byID[normalise(bi.UniqueID)]; ok {
			return room, matchUniqueID
		}
	}
	if bi.Serial != "" {
		if room, ok := m.byID[normalise(bi.Serial)]; ok {
			return room, matchSerial
		}
	}
	if room, ok := m.byID[strconv.FormatUint(n.ID, 10)]; ok {
		return room, matchNodeID
	}
	return Unassigned, matchNone
}

// Enricher holds the current mapping and exposes it as a collector.
type Enricher struct {
	path   string
	src    Source
	logger *slog.Logger

	mu      sync.Mutex
	mapping *Mapping
	// warned remembers the nodes already reported as matched by node_id, so
	// the warning is logged once per (re)load rather than once per scrape.
	warned map[uint64]struct{}
}

// New loads the mapping file; it fails if the file cannot be read or parsed.
func New(path string, src Source, logger *slog.Logger) (*Enricher, error) {
	if logger == nil {
		logger = slog.Default()
	}
	e := &Enricher{path: path, src: src, logger: logger}
	if err := e.Reload(); err != nil {
		return nil, err
	}
	return e, nil
}

// Reload re-reads the mapping file. On error the previous mapping is kept.
func (e *Enricher) Reload() error {
	data, err := os.ReadFile(e.path)
	if err != nil {
		return err
	}
	m, err := Parse(data)
	if err != nil {
		return fmt.Errorf("%s: %w", e.path, err)
	}
	e.mu.Lock()
	e.mapping = m
	e.warned = map[uint64]struct{}{}
	e.mu.Unlock()
	e.logger.Info("room mapping loaded", "file", e.path, "identifiers", len(m.byID))
	return nil
}

// Room returns the room of a node, or Unassigned.
func (e *Enricher) Room(n state.Node) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	room, kind := e.mapping.lookup(n)
	if kind == matchNodeID {
		if _, done := e.warned[n.ID]; !done {
			e.warned[n.ID] = struct{}{}
			e.logger.Warn("room matched by node_id, which changes on re-commissioning; prefer the UniqueID",
				"node_id", n.ID, "room", room, "unique_id", n.BasicInfo().UniqueID)
		}
	}
	return room
}

var nodeRoomDesc = prometheus.NewDesc("matter_node_room",
	"Room a node is assigned to in the rooms file; always 1.", []string{"node_id", "room"}, nil)

// Describe is intentionally empty: this is an unchecked collector.
func (e *Enricher) Describe(chan<- *prometheus.Desc) {}

// Collect emits matter_node_room for every known node.
func (e *Enricher) Collect(ch chan<- prometheus.Metric) {
	snap := e.src.Snapshot()
	if !snap.Connected {
		return
	}
	for _, n := range snap.Nodes {
		ch <- prometheus.MustNewConstMetric(nodeRoomDesc, prometheus.GaugeValue, 1,
			strconv.FormatUint(n.ID, 10), e.Room(n))
	}
}
