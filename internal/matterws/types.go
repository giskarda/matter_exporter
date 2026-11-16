package matterws

import (
	"encoding/json"
	"fmt"
)

// Event types sent by the server.
const (
	EventNodeAdded        = "node_added"
	EventNodeUpdated      = "node_updated"
	EventNodeRemoved      = "node_removed"
	EventAttributeUpdated = "attribute_updated"
	EventNodeEvent        = "node_event"
	EventEndpointAdded    = "endpoint_added"
	EventEndpointRemoved  = "endpoint_removed"
	EventServerShutdown   = "server_shutdown"
)

// ServerInfo is the first message sent by the server on every connection.
type ServerInfo struct {
	FabricID                  uint64 `json:"fabric_id"`
	CompressedFabricID        uint64 `json:"compressed_fabric_id"`
	SchemaVersion             int    `json:"schema_version"`
	MinSupportedSchemaVersion int    `json:"min_supported_schema_version"`
	SDKVersion                string `json:"sdk_version"`
}

// Node is a node object as found in the start_listening dump and in
// node_added / node_updated events. Attribute values are kept raw so that
// 64-bit integers survive untouched.
type Node struct {
	NodeID     uint64                     `json:"node_id"`
	Available  bool                       `json:"available"`
	IsBridge   bool                       `json:"is_bridge"`
	Attributes map[string]json.RawMessage `json:"attributes"`
}

// AttributeUpdate is the payload of an attribute_updated event.
type AttributeUpdate struct {
	NodeID uint64
	Path   string // "<endpoint>/<cluster>/<attribute>"
	Value  json.RawMessage
}

// NodeEvent is the payload of a node_event event (a Matter cluster event).
type NodeEvent struct {
	NodeID     uint64          `json:"node_id"`
	EndpointID uint16          `json:"endpoint_id"`
	ClusterID  uint32          `json:"cluster_id"`
	EventID    uint32          `json:"event_id"`
	Data       json.RawMessage `json:"data"`
}

// EndpointRef is the payload of endpoint_added / endpoint_removed events.
type EndpointRef struct {
	NodeID     uint64 `json:"node_id"`
	EndpointID uint16 `json:"endpoint_id"`
}

// Event is a server event with its payload still encoded. Use the accessor
// matching Type to decode it.
type Event struct {
	Type string          `json:"event"`
	Data json.RawMessage `json:"data"`
}

// Node decodes the payload of node_added and node_updated.
func (e Event) Node() (Node, error) {
	var n Node
	err := json.Unmarshal(e.Data, &n)
	return n, err
}

// NodeID decodes the payload of node_removed.
func (e Event) NodeID() (uint64, error) {
	var id uint64
	err := json.Unmarshal(e.Data, &id)
	return id, err
}

// AttributeUpdate decodes the payload of attribute_updated.
func (e Event) AttributeUpdate() (AttributeUpdate, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal(e.Data, &parts); err != nil {
		return AttributeUpdate{}, err
	}
	if len(parts) != 3 {
		return AttributeUpdate{}, fmt.Errorf("attribute_updated: want 3 elements, got %d", len(parts))
	}
	var u AttributeUpdate
	if err := json.Unmarshal(parts[0], &u.NodeID); err != nil {
		return AttributeUpdate{}, fmt.Errorf("attribute_updated node id: %w", err)
	}
	if err := json.Unmarshal(parts[1], &u.Path); err != nil {
		return AttributeUpdate{}, fmt.Errorf("attribute_updated path: %w", err)
	}
	u.Value = parts[2]
	return u, nil
}

// NodeEvent decodes the payload of node_event.
func (e Event) NodeEvent() (NodeEvent, error) {
	var ev NodeEvent
	err := json.Unmarshal(e.Data, &ev)
	return ev, err
}

// Endpoint decodes the payload of endpoint_added and endpoint_removed.
func (e Event) Endpoint() (EndpointRef, error) {
	var ref EndpointRef
	err := json.Unmarshal(e.Data, &ref)
	return ref, err
}

// message is the envelope of anything the server sends after the server info.
type message struct {
	MessageID string          `json:"message_id"`
	Result    json.RawMessage `json:"result"`
	ErrorCode *int            `json:"error_code"`
	Details   string          `json:"details"`
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data"`
}
