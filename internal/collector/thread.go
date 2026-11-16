package collector

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/giskarda/matter_exporter/internal/state"
)

// Thread Network Diagnostics cluster, always on the root endpoint.
const (
	clusterThreadDiag = 0x0035

	attrRoutingRole   = 1
	attrNeighborTable = 7
)

var (
	threadRoleDesc = prometheus.NewDesc("matter_thread_routing_role",
		"Current Thread routing role of the node; 1 for the series carrying the current role.",
		[]string{"node_id", "role"}, nil)
	threadNeighborRSSIDesc = prometheus.NewDesc("matter_thread_neighbor_rssi_dbm",
		"Average RSSI of a Thread neighbor as seen by the node, in dBm.",
		[]string{"node_id", "neighbor_ext_addr"}, nil)
	threadNeighborLQIDesc = prometheus.NewDesc("matter_thread_neighbor_lqi",
		"Link quality indicator (0-3) of a Thread neighbor as seen by the node.",
		[]string{"node_id", "neighbor_ext_addr"}, nil)
)

// threadRoles maps RoutingRoleEnum to label values.
var threadRoles = map[uint8]string{
	0: "unspecified",
	1: "unassigned",
	2: "sleepy_end_device",
	3: "end_device",
	4: "reed",
	5: "router",
	6: "leader",
}

// threadNeighbor is a NeighborTableStruct, keyed by field tag.
type threadNeighbor struct {
	ExtAddress  uint64   `json:"0"`
	LQI         *float64 `json:"5"`
	AverageRSSI *float64 `json:"6"` // nullable
	LastRSSI    *float64 `json:"7"` // nullable
}

func collectThread(ch chan<- prometheus.Metric, n state.Node) {
	if !n.HasCluster(0, clusterThreadDiag) {
		return
	}
	id := nodeID(n.ID)

	var role uint8
	if n.Decode(0, clusterThreadDiag, attrRoutingRole, &role) {
		name, ok := threadRoles[role]
		if !ok {
			name = fmt.Sprintf("unknown_%d", role)
		}
		ch <- prometheus.MustNewConstMetric(threadRoleDesc, prometheus.GaugeValue, 1, id, name)
	}

	var neighbors []threadNeighbor
	if !n.Decode(0, clusterThreadDiag, attrNeighborTable, &neighbors) {
		return
	}
	seen := make(map[uint64]struct{}, len(neighbors))
	for _, nb := range neighbors {
		if _, dup := seen[nb.ExtAddress]; dup {
			continue
		}
		seen[nb.ExtAddress] = struct{}{}
		addr := fmt.Sprintf("%016x", nb.ExtAddress)

		rssi := nb.AverageRSSI
		if rssi == nil {
			rssi = nb.LastRSSI
		}
		if rssi != nil {
			ch <- prometheus.MustNewConstMetric(threadNeighborRSSIDesc, prometheus.GaugeValue, *rssi, id, addr)
		}
		if nb.LQI != nil {
			ch <- prometheus.MustNewConstMetric(threadNeighborLQIDesc, prometheus.GaugeValue, *nb.LQI, id, addr)
		}
	}
}
