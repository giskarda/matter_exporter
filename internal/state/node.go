// Package state keeps the in-memory picture of the Matter fabric as reported
// by the server, and hands out immutable snapshots of it.
package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Cluster and attribute IDs used for discovery.
const (
	ClusterDescriptor = 0x001D
	ClusterBasicInfo  = 0x0028

	attrDeviceTypeList = 0
	attrServerList     = 1
)

// Path identifies an attribute on a node.
type Path struct {
	Endpoint  uint16
	Cluster   uint32
	Attribute uint32
}

// ParsePath parses the server's "<endpoint>/<cluster>/<attribute>" notation.
func ParsePath(s string) (Path, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		return Path{}, fmt.Errorf("attribute path %q: want endpoint/cluster/attribute", s)
	}
	ep, err := strconv.ParseUint(parts[0], 10, 16)
	if err != nil {
		return Path{}, fmt.Errorf("attribute path %q: bad endpoint", s)
	}
	cl, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return Path{}, fmt.Errorf("attribute path %q: bad cluster", s)
	}
	at, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil {
		return Path{}, fmt.Errorf("attribute path %q: bad attribute", s)
	}
	return Path{Endpoint: uint16(ep), Cluster: uint32(cl), Attribute: uint32(at)}, nil
}

func (p Path) String() string {
	return fmt.Sprintf("%d/%d/%d", p.Endpoint, p.Cluster, p.Attribute)
}

// Node is one commissioned node. Values returned by Store.Snapshot are
// private copies and never change.
type Node struct {
	ID        uint64
	Available bool
	IsBridge  bool
	// Attrs holds the raw JSON value of every known attribute.
	Attrs map[Path]json.RawMessage
}

var jsonNull = []byte("null")

// Raw returns the raw value of an attribute. ok is false when the attribute
// is absent or null (null means "unknown" in Matter).
func (n Node) Raw(endpoint uint16, cluster, attribute uint32) (json.RawMessage, bool) {
	v, ok := n.Attrs[Path{endpoint, cluster, attribute}]
	if !ok || bytes.Equal(bytes.TrimSpace(v), jsonNull) {
		return nil, false
	}
	return v, true
}

// Decode unmarshals an attribute into dst; it reports false when the
// attribute is absent, null or of an unexpected shape.
func (n Node) Decode(endpoint uint16, cluster, attribute uint32, dst any) bool {
	v, ok := n.Raw(endpoint, cluster, attribute)
	if !ok {
		return false
	}
	return json.Unmarshal(v, dst) == nil
}

// Float returns a numeric attribute.
func (n Node) Float(endpoint uint16, cluster, attribute uint32) (float64, bool) {
	var f float64
	ok := n.Decode(endpoint, cluster, attribute, &f)
	return f, ok
}

// Bool returns a boolean attribute.
func (n Node) Bool(endpoint uint16, cluster, attribute uint32) (bool, bool) {
	var b bool
	ok := n.Decode(endpoint, cluster, attribute, &b)
	return b, ok
}

// Text returns an attribute as a label-friendly string: strings as they are,
// numbers in their exact decimal form, anything else (absent, null) as "".
func (n Node) Text(endpoint uint16, cluster, attribute uint32) string {
	v, ok := n.Raw(endpoint, cluster, attribute)
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	var num json.Number
	if json.Unmarshal(v, &num) == nil {
		return num.String()
	}
	return ""
}

// Endpoints returns the endpoints that have at least one attribute, sorted.
func (n Node) Endpoints() []uint16 {
	seen := map[uint16]struct{}{}
	for p := range n.Attrs {
		seen[p.Endpoint] = struct{}{}
	}
	eps := make([]uint16, 0, len(seen))
	for ep := range seen {
		eps = append(eps, ep)
	}
	slices.Sort(eps)
	return eps
}

// HasCluster reports whether the endpoint's Descriptor ServerList contains
// the cluster.
func (n Node) HasCluster(endpoint uint16, cluster uint32) bool {
	var servers []uint32
	if !n.Decode(endpoint, ClusterDescriptor, attrServerList, &servers) {
		return false
	}
	return slices.Contains(servers, cluster)
}

// DeviceTypes returns the device type IDs in the endpoint's Descriptor
// DeviceTypeList, sorted and de-duplicated.
func (n Node) DeviceTypes(endpoint uint16) []uint32 {
	// DeviceTypeStruct: tag 0 = DeviceType, tag 1 = Revision.
	var list []struct {
		DeviceType uint32 `json:"0"`
	}
	if !n.Decode(endpoint, ClusterDescriptor, attrDeviceTypeList, &list) {
		return nil
	}
	types := make([]uint32, 0, len(list))
	for _, e := range list {
		types = append(types, e.DeviceType)
	}
	slices.Sort(types)
	return slices.Compact(types)
}

// BasicInfo is the node metadata from the Basic Information cluster on
// endpoint 0. Missing attributes are empty strings.
type BasicInfo struct {
	VendorName  string // attribute 1
	VendorID    string // attribute 2
	ProductName string // attribute 3
	ProductID   string // attribute 4
	NodeLabel   string // attribute 5
	SWVersion   string // attribute 10 (SoftwareVersionString)
	Serial      string // attribute 15
	UniqueID    string // attribute 18
}

// BasicInfo extracts the node metadata.
func (n Node) BasicInfo() BasicInfo {
	get := func(attr uint32) string { return n.Text(0, ClusterBasicInfo, attr) }
	return BasicInfo{
		VendorName:  get(1),
		VendorID:    get(2),
		ProductName: get(3),
		ProductID:   get(4),
		NodeLabel:   get(5),
		SWVersion:   get(10),
		Serial:      get(15),
		UniqueID:    get(18),
	}
}
