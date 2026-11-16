package state

import (
	"bytes"
	"cmp"
	"encoding/json"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/giskarda/matter_exporter/internal/matterws"
)

const (
	clusterBooleanState = 0x0045
	clusterSwitch       = 0x003B
)

// switchEventNames maps Switch cluster (0x003B) event IDs to label values.
var switchEventNames = map[uint32]string{
	0: "switch_latched",
	1: "initial_press",
	2: "long_press",
	3: "short_release",
	4: "long_release",
	5: "multi_press_ongoing",
	6: "multi_press_complete",
}

// EndpointKey identifies an endpoint of a node.
type EndpointKey struct {
	NodeID   uint64
	Endpoint uint16
}

// SwitchEventKey identifies a switch event counter.
type SwitchEventKey struct {
	NodeID   uint64
	Endpoint uint16
	Event    string
}

// AvailabilityKey identifies an availability transition counter. To is
// "available" or "unavailable".
type AvailabilityKey struct {
	NodeID uint64
	To     string
}

// Snapshot is a consistent, private copy of the store.
type Snapshot struct {
	// Connected is true while the WebSocket session is listening. When false,
	// Nodes is the last known picture and must not be presented as current.
	Connected bool
	// Nodes is sorted by ID.
	Nodes []Node

	Reconnects uint64
	Events     map[string]uint64 // by event type
	LastEvent  time.Time         // zero if no event was ever received

	SwitchEvents        map[SwitchEventKey]uint64
	ContactChanges      map[EndpointKey]uint64
	AvailabilityChanges map[AvailabilityKey]uint64
}

// Store is the node store. It implements matterws.Handler.
type Store struct {
	logger *slog.Logger
	now    func() time.Time

	mu            sync.Mutex
	connected     bool
	everConnected bool
	nodes         map[uint64]*Node

	reconnects uint64
	events     map[string]uint64
	lastEvent  time.Time

	// Per-node counters. They survive reconnects and are dropped when the
	// node is removed from the fabric.
	switchEvents   map[SwitchEventKey]uint64
	contactChanges map[EndpointKey]uint64
	availChanges   map[AvailabilityKey]uint64
}

var _ matterws.Handler = (*Store)(nil)

// New returns an empty store.
func New(logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{
		logger:         logger,
		now:            time.Now,
		nodes:          map[uint64]*Node{},
		events:         map[string]uint64{},
		switchEvents:   map[SwitchEventKey]uint64{},
		contactChanges: map[EndpointKey]uint64{},
		availChanges:   map[AvailabilityKey]uint64{},
	}
}

// Snapshot returns a copy of the current state.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := Snapshot{
		Connected:           s.connected,
		Nodes:               make([]Node, 0, len(s.nodes)),
		Reconnects:          s.reconnects,
		Events:              maps.Clone(s.events),
		LastEvent:           s.lastEvent,
		SwitchEvents:        maps.Clone(s.switchEvents),
		ContactChanges:      maps.Clone(s.contactChanges),
		AvailabilityChanges: maps.Clone(s.availChanges),
	}
	for _, n := range s.nodes {
		c := *n
		// Raw values are never mutated in place, so sharing them is safe.
		c.Attrs = maps.Clone(n.Attrs)
		snap.Nodes = append(snap.Nodes, c)
	}
	slices.SortFunc(snap.Nodes, func(a, b Node) int { return cmp.Compare(a.ID, b.ID) })
	return snap
}

// Connected implements matterws.Handler: it rebuilds the state from a fresh
// node dump. Nodes missing from the dump were removed while we were away.
func (s *Store) Connected(_ matterws.ServerInfo, nodes []matterws.Node) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.everConnected {
		s.reconnects++
	}
	s.everConnected = true
	s.connected = true

	fresh := make(map[uint64]*Node, len(nodes))
	for _, wn := range nodes {
		n := s.convert(wn)
		s.countChanges(s.nodes[n.ID], n)
		fresh[n.ID] = n
	}
	for id := range s.nodes {
		if _, ok := fresh[id]; !ok {
			s.dropCounters(id)
		}
	}
	s.nodes = fresh
}

// Disconnected implements matterws.Handler.
func (s *Store) Disconnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = false
}

// Event implements matterws.Handler.
func (s *Store) Event(ev matterws.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events[ev.Type]++
	s.lastEvent = s.now()

	var err error
	switch ev.Type {
	case matterws.EventNodeAdded, matterws.EventNodeUpdated:
		var wn matterws.Node
		if wn, err = ev.Node(); err == nil {
			n := s.convert(wn)
			s.countChanges(s.nodes[n.ID], n)
			s.nodes[n.ID] = n
		}
	case matterws.EventNodeRemoved:
		var id uint64
		if id, err = ev.NodeID(); err == nil {
			delete(s.nodes, id)
			s.dropCounters(id)
		}
	case matterws.EventAttributeUpdated:
		var u matterws.AttributeUpdate
		if u, err = ev.AttributeUpdate(); err == nil {
			err = s.applyAttribute(u)
		}
	case matterws.EventNodeEvent:
		var ne matterws.NodeEvent
		if ne, err = ev.NodeEvent(); err == nil {
			s.applyNodeEvent(ne)
		}
	case matterws.EventEndpointRemoved:
		var ref matterws.EndpointRef
		if ref, err = ev.Endpoint(); err == nil {
			s.removeEndpoint(ref)
		}
	case matterws.EventEndpointAdded, matterws.EventServerShutdown:
		// Nothing to do: attributes of a new endpoint arrive with the
		// node_updated that follows; shutdown is handled by the client.
	default:
		s.logger.Debug("ignoring unknown event type", "event", ev.Type)
	}
	if err != nil {
		s.logger.Warn("cannot apply event", "event", ev.Type, "err", err)
	}
}

func (s *Store) convert(wn matterws.Node) *Node {
	n := &Node{
		ID:        wn.NodeID,
		Available: wn.Available,
		IsBridge:  wn.IsBridge,
		Attrs:     make(map[Path]json.RawMessage, len(wn.Attributes)),
	}
	for k, v := range wn.Attributes {
		p, err := ParsePath(k)
		if err != nil {
			s.logger.Warn("skipping attribute", "node_id", wn.NodeID, "err", err)
			continue
		}
		n.Attrs[p] = v
	}
	return n
}

func (s *Store) applyAttribute(u matterws.AttributeUpdate) error {
	p, err := ParsePath(u.Path)
	if err != nil {
		return err
	}
	n, ok := s.nodes[u.NodeID]
	if !ok {
		s.logger.Debug("attribute update for unknown node", "node_id", u.NodeID, "path", u.Path)
		return nil
	}
	if isContactState(p) && contactChanged(n.Attrs[p], u.Value) {
		s.contactChanges[EndpointKey{n.ID, p.Endpoint}]++
	}
	n.Attrs[p] = u.Value
	return nil
}

func (s *Store) applyNodeEvent(ne matterws.NodeEvent) {
	if ne.ClusterID != clusterSwitch {
		return
	}
	if _, ok := s.nodes[ne.NodeID]; !ok {
		return
	}
	name, ok := switchEventNames[ne.EventID]
	if !ok {
		return
	}
	s.switchEvents[SwitchEventKey{ne.NodeID, ne.EndpointID, name}]++
}

func (s *Store) removeEndpoint(ref matterws.EndpointRef) {
	n, ok := s.nodes[ref.NodeID]
	if !ok {
		return
	}
	maps.DeleteFunc(n.Attrs, func(p Path, _ json.RawMessage) bool { return p.Endpoint == ref.EndpointID })
	maps.DeleteFunc(s.switchEvents, func(k SwitchEventKey, _ uint64) bool {
		return k.NodeID == ref.NodeID && k.Endpoint == ref.EndpointID
	})
	delete(s.contactChanges, EndpointKey{ref.NodeID, ref.EndpointID})
}

// countChanges updates the transition counters when a whole node object
// replaces the previous one (node_updated, or the dump after a reconnect).
// A node seen for the first time is not a transition.
func (s *Store) countChanges(old, fresh *Node) {
	if old == nil {
		return
	}
	if old.Available != fresh.Available {
		to := "unavailable"
		if fresh.Available {
			to = "available"
		}
		s.availChanges[AvailabilityKey{fresh.ID, to}]++
	}
	for p, v := range fresh.Attrs {
		if isContactState(p) && contactChanged(old.Attrs[p], v) {
			s.contactChanges[EndpointKey{fresh.ID, p.Endpoint}]++
		}
	}
}

func (s *Store) dropCounters(nodeID uint64) {
	maps.DeleteFunc(s.switchEvents, func(k SwitchEventKey, _ uint64) bool { return k.NodeID == nodeID })
	maps.DeleteFunc(s.contactChanges, func(k EndpointKey, _ uint64) bool { return k.NodeID == nodeID })
	maps.DeleteFunc(s.availChanges, func(k AvailabilityKey, _ uint64) bool { return k.NodeID == nodeID })
}

// isContactState reports whether p is BooleanState.StateValue.
func isContactState(p Path) bool {
	return p.Cluster == clusterBooleanState && p.Attribute == 0
}

// contactChanged reports a transition between two known boolean values. The
// first value ever seen and null (unknown) on either side do not count.
func contactChanged(old, fresh json.RawMessage) bool {
	var a, b bool
	if json.Unmarshal(old, &a) != nil || json.Unmarshal(fresh, &b) != nil {
		return false
	}
	if bytes.Equal(bytes.TrimSpace(old), jsonNull) || bytes.Equal(bytes.TrimSpace(fresh), jsonNull) {
		return false
	}
	return a != b
}
