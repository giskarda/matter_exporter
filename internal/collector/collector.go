// Package collector exposes a state snapshot as Prometheus metrics.
//
// It is an unchecked collector (empty Describe): every scrape emits const
// metrics for exactly what is in the snapshot, so removed nodes and endpoints
// vanish from the output on their own.
package collector

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/giskarda/matter_exporter/internal/state"
)

// Collector is the core Matter collector.
type Collector struct {
	src state.Source
}

// New returns a collector reading from src.
func New(src state.Source) *Collector {
	return &Collector{src: src}
}

var (
	nodeLabels     = []string{"node_id"}
	endpointLabels = []string{"node_id", "endpoint"}

	nodeAvailableDesc = prometheus.NewDesc("matter_node_available",
		"Whether the Matter server can currently reach the node (1) or not (0).", nodeLabels, nil)
	nodeInfoDesc = prometheus.NewDesc("matter_node_info",
		"Node metadata from the Basic Information cluster; always 1.",
		[]string{"node_id", "unique_id", "vendor", "vendor_id", "product", "product_id", "name", "sw_version", "serial"}, nil)
	endpointInfoDesc = prometheus.NewDesc("matter_endpoint_info",
		"Device types of an endpoint from the Descriptor cluster, one series per device type; always 1.",
		[]string{"node_id", "endpoint", "device_type"}, nil)

	switchEventsDesc = prometheus.NewDesc("matter_switch_events_total",
		"Switch cluster events received.", []string{"node_id", "endpoint", "event"}, nil)
	contactChangesDesc = prometheus.NewDesc("matter_contact_changes_total",
		"Boolean State (contact) changes observed.", endpointLabels, nil)
	availabilityChangesDesc = prometheus.NewDesc("matter_node_availability_changes_total",
		"Node availability transitions observed, by new state.", []string{"node_id", "to"}, nil)

	connectedDesc = prometheus.NewDesc("matter_exporter_server_connected",
		"Whether the WebSocket connection to the Matter server is up (1) or not (0).", nil, nil)
	reconnectsDesc = prometheus.NewDesc("matter_exporter_reconnects_total",
		"WebSocket reconnections to the Matter server.", nil, nil)
	eventsDesc = prometheus.NewDesc("matter_exporter_events_total",
		"Events received from the Matter server.", []string{"type"}, nil)
	lastEventDesc = prometheus.NewDesc("matter_exporter_last_event_timestamp_seconds",
		"Unix time of the last event received from the Matter server.", nil, nil)
)

// Describe is intentionally empty: this is an unchecked collector.
func (c *Collector) Describe(chan<- *prometheus.Desc) {}

// Collect emits the current snapshot.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	snap := c.src.Snapshot()

	gauge(ch, connectedDesc, boolValue(snap.Connected))
	counter(ch, reconnectsDesc, snap.Reconnects)
	for typ, n := range snap.Events {
		counter(ch, eventsDesc, n, typ)
	}
	if !snap.LastEvent.IsZero() {
		gauge(ch, lastEventDesc, float64(snap.LastEvent.UnixNano())/1e9)
	}

	for k, n := range snap.SwitchEvents {
		counter(ch, switchEventsDesc, n, nodeID(k.NodeID), endpointID(k.Endpoint), k.Event)
	}
	for k, n := range snap.ContactChanges {
		counter(ch, contactChangesDesc, n, nodeID(k.NodeID), endpointID(k.Endpoint))
	}
	for k, n := range snap.AvailabilityChanges {
		counter(ch, availabilityChangesDesc, n, nodeID(k.NodeID), k.To)
	}

	// Without a server connection the node picture is stale: exporting it
	// would present old readings and old availability as current.
	if !snap.Connected {
		return
	}

	for _, n := range snap.Nodes {
		id := nodeID(n.ID)
		gauge(ch, nodeAvailableDesc, boolValue(n.Available), id)

		bi := n.BasicInfo()
		gauge(ch, nodeInfoDesc, 1,
			id, bi.UniqueID, bi.VendorName, bi.VendorID, bi.ProductName, bi.ProductID, bi.NodeLabel, bi.SWVersion, bi.Serial)

		endpoints := n.Endpoints()
		for _, ep := range endpoints {
			for _, dt := range n.DeviceTypes(ep) {
				gauge(ch, endpointInfoDesc, 1, id, endpointID(ep), strconv.FormatUint(uint64(dt), 10))
			}
		}

		// Known but unreachable: availability and info only, no values.
		if !n.Available {
			continue
		}
		for _, ep := range endpoints {
			collectValues(ch, n, ep)
		}
		collectThread(ch, n)
	}
}

func gauge(ch chan<- prometheus.Metric, desc *prometheus.Desc, v float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, v, labels...)
}

func counter(ch chan<- prometheus.Metric, desc *prometheus.Desc, n uint64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, float64(n), labels...)
}

func nodeID(id uint64) string     { return strconv.FormatUint(id, 10) }
func endpointID(ep uint16) string { return strconv.FormatUint(uint64(ep), 10) }

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
