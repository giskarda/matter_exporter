package collector

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/giskarda/matter_exporter/internal/state"
	"github.com/giskarda/matter_exporter/internal/state/statetest"
)

// sensorNode exercises every curated metric.
func sensorNode(t *testing.T, id uint64, available bool) state.Node {
	return statetest.Node(t, id, available, map[string]string{
		// Root endpoint: metadata, battery, Thread diagnostics.
		"0/29/0":  `[{"0":22,"1":2},{"0":17,"1":1}]`,
		"0/29/1":  `[29,40,47,53]`,
		"0/40/1":  `"ACME"`,
		"0/40/2":  `4874`,
		"0/40/3":  `"Multi Sensor"`,
		"0/40/4":  `80`,
		"0/40/5":  `"kitchen plug"`,
		"0/40/10": `"3.5.0"`,
		"0/40/15": `"SN0001"`,
		"0/40/18": `"30BFA39F70D20029"`,
		"0/47/12": `173`,
		"0/53/1":  `5`,
		"0/53/7": `[{"0":13140920649285976541,"5":3,"6":-69,"7":-70},
		            {"0":1,"5":2,"6":null,"7":-80},
		            {"0":1,"5":1,"6":-10,"7":-10}]`,
		// Environmental sensors.
		"1/29/0":   `[{"0":770,"1":3}]`,
		"1/29/1":   `[3,29,1024,1026,1027,1029,1030]`,
		"1/1024/0": `10001`,
		"1/1026/0": `1993`,
		"1/1027/0": `1013`,
		"1/1029/0": `5824`,
		"1/1030/0": `1`,
		// Contact, switch and electrical measurements.
		"2/29/0":  `[{"0":266,"1":3}]`,
		"2/29/1":  `[6,29,69,144,145]`,
		"2/6/0":   `false`,
		"2/69/0":  `true`,
		"2/144/8": `76400`,
		"2/145/1": `{"0":109275000}`,
		// Null value, and a value whose cluster is not in the ServerList.
		"3/29/0":   `[{"0":770,"1":3}]`,
		"3/29/1":   `[29,1026]`,
		"3/1026/0": `null`,
		"3/1029/0": `5000`,
	})
}

const infoMetrics = `
# HELP matter_endpoint_info Device types of an endpoint from the Descriptor cluster, one series per device type; always 1.
# TYPE matter_endpoint_info gauge
matter_endpoint_info{device_type="17",endpoint="0",node_id="1"} 1
matter_endpoint_info{device_type="22",endpoint="0",node_id="1"} 1
matter_endpoint_info{device_type="770",endpoint="1",node_id="1"} 1
matter_endpoint_info{device_type="266",endpoint="2",node_id="1"} 1
matter_endpoint_info{device_type="770",endpoint="3",node_id="1"} 1
# HELP matter_node_info Node metadata from the Basic Information cluster; always 1.
# TYPE matter_node_info gauge
matter_node_info{name="kitchen plug",node_id="1",product="Multi Sensor",product_id="80",serial="SN0001",sw_version="3.5.0",unique_id="30BFA39F70D20029",vendor="ACME",vendor_id="4874"} 1
`

const selfConnected = `
# HELP matter_exporter_reconnects_total WebSocket reconnections to the Matter server.
# TYPE matter_exporter_reconnects_total counter
matter_exporter_reconnects_total 0
# HELP matter_exporter_server_connected Whether the WebSocket connection to the Matter server is up (1) or not (0).
# TYPE matter_exporter_server_connected gauge
matter_exporter_server_connected 1
`

func TestCollect(t *testing.T) {
	tests := []struct {
		name string
		snap func(t *testing.T) state.Snapshot
		want string
	}{
		{
			name: "available node emits info, values and Thread diagnostics",
			snap: func(t *testing.T) state.Snapshot {
				return state.Snapshot{Connected: true, Nodes: []state.Node{sensorNode(t, 1, true)}}
			},
			want: selfConnected + infoMetrics + `
# HELP matter_node_available Whether the Matter server can currently reach the node (1) or not (0).
# TYPE matter_node_available gauge
matter_node_available{node_id="1"} 1
# HELP matter_temperature_celsius Temperature Measurement (0x0402) MeasuredValue in degrees Celsius.
# TYPE matter_temperature_celsius gauge
matter_temperature_celsius{endpoint="1",node_id="1"} 19.93
# HELP matter_relative_humidity_percent Relative Humidity Measurement (0x0405) MeasuredValue in percent.
# TYPE matter_relative_humidity_percent gauge
matter_relative_humidity_percent{endpoint="1",node_id="1"} 58.24
# HELP matter_pressure_hectopascals Pressure Measurement (0x0403) MeasuredValue in hectopascals.
# TYPE matter_pressure_hectopascals gauge
matter_pressure_hectopascals{endpoint="1",node_id="1"} 1013
# HELP matter_illuminance_lux Illuminance Measurement (0x0400) MeasuredValue in lux.
# TYPE matter_illuminance_lux gauge
matter_illuminance_lux{endpoint="1",node_id="1"} 10
# HELP matter_occupancy Occupancy Sensing (0x0406): 1 if occupied, 0 otherwise.
# TYPE matter_occupancy gauge
matter_occupancy{endpoint="1",node_id="1"} 1
# HELP matter_contact_state Boolean State (0x0045) StateValue: 1 if true (contact closed), 0 otherwise.
# TYPE matter_contact_state gauge
matter_contact_state{endpoint="2",node_id="1"} 1
# HELP matter_onoff On/Off (0x0006): 1 if on, 0 if off.
# TYPE matter_onoff gauge
matter_onoff{endpoint="2",node_id="1"} 0
# HELP matter_battery_percent Power Source (0x002F) BatPercentRemaining in percent.
# TYPE matter_battery_percent gauge
matter_battery_percent{endpoint="0",node_id="1"} 86.5
# HELP matter_active_power_watts Electrical Power Measurement (0x0090) ActivePower in watts.
# TYPE matter_active_power_watts gauge
matter_active_power_watts{endpoint="2",node_id="1"} 76.4
# HELP matter_energy_watt_hours_total Electrical Energy Measurement (0x0091) CumulativeEnergyImported in watt-hours.
# TYPE matter_energy_watt_hours_total counter
matter_energy_watt_hours_total{endpoint="2",node_id="1"} 109275
# HELP matter_thread_routing_role Current Thread routing role of the node; 1 for the series carrying the current role.
# TYPE matter_thread_routing_role gauge
matter_thread_routing_role{node_id="1",role="router"} 1
# HELP matter_thread_neighbor_rssi_dbm Average RSSI of a Thread neighbor as seen by the node, in dBm.
# TYPE matter_thread_neighbor_rssi_dbm gauge
matter_thread_neighbor_rssi_dbm{neighbor_ext_addr="0000000000000001",node_id="1"} -80
matter_thread_neighbor_rssi_dbm{neighbor_ext_addr="b65dedb4fa4941dd",node_id="1"} -69
# HELP matter_thread_neighbor_lqi Link quality indicator (0-3) of a Thread neighbor as seen by the node.
# TYPE matter_thread_neighbor_lqi gauge
matter_thread_neighbor_lqi{neighbor_ext_addr="0000000000000001",node_id="1"} 2
matter_thread_neighbor_lqi{neighbor_ext_addr="b65dedb4fa4941dd",node_id="1"} 3
`,
		},
		{
			name: "unavailable node emits availability and info only",
			snap: func(t *testing.T) state.Snapshot {
				return state.Snapshot{Connected: true, Nodes: []state.Node{sensorNode(t, 1, false)}}
			},
			want: selfConnected + infoMetrics + `
# HELP matter_node_available Whether the Matter server can currently reach the node (1) or not (0).
# TYPE matter_node_available gauge
matter_node_available{node_id="1"} 0
`,
		},
		{
			name: "disconnected exporter emits no node metrics but keeps counters",
			snap: func(t *testing.T) state.Snapshot {
				return state.Snapshot{
					Connected:  false,
					Nodes:      []state.Node{sensorNode(t, 1, true)},
					Reconnects: 3,
					Events:     map[string]uint64{"attribute_updated": 40, "node_event": 2},
					LastEvent:  time.Unix(1_700_000_000, 500_000_000),
					SwitchEvents: map[state.SwitchEventKey]uint64{
						{NodeID: 1, Endpoint: 3, Event: "initial_press"}: 7,
					},
					ContactChanges: map[state.EndpointKey]uint64{{NodeID: 1, Endpoint: 2}: 4},
					AvailabilityChanges: map[state.AvailabilityKey]uint64{
						{NodeID: 1, To: "unavailable"}: 2,
						{NodeID: 1, To: "available"}:   1,
					},
				}
			},
			want: `
# HELP matter_exporter_reconnects_total WebSocket reconnections to the Matter server.
# TYPE matter_exporter_reconnects_total counter
matter_exporter_reconnects_total 3
# HELP matter_exporter_server_connected Whether the WebSocket connection to the Matter server is up (1) or not (0).
# TYPE matter_exporter_server_connected gauge
matter_exporter_server_connected 0
# HELP matter_exporter_events_total Events received from the Matter server.
# TYPE matter_exporter_events_total counter
matter_exporter_events_total{type="attribute_updated"} 40
matter_exporter_events_total{type="node_event"} 2
# HELP matter_exporter_last_event_timestamp_seconds Unix time of the last event received from the Matter server.
# TYPE matter_exporter_last_event_timestamp_seconds gauge
matter_exporter_last_event_timestamp_seconds 1.7000000005e+09
# HELP matter_switch_events_total Switch cluster events received.
# TYPE matter_switch_events_total counter
matter_switch_events_total{endpoint="3",event="initial_press",node_id="1"} 7
# HELP matter_contact_changes_total Boolean State (contact) changes observed.
# TYPE matter_contact_changes_total counter
matter_contact_changes_total{endpoint="2",node_id="1"} 4
# HELP matter_node_availability_changes_total Node availability transitions observed, by new state.
# TYPE matter_node_availability_changes_total counter
matter_node_availability_changes_total{node_id="1",to="available"} 1
matter_node_availability_changes_total{node_id="1",to="unavailable"} 2
`,
		},
		{
			name: "no nodes",
			snap: func(*testing.T) state.Snapshot { return state.Snapshot{Connected: true} },
			want: selfConnected,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(&statetest.Source{Snap: tt.snap(t)})
			if err := testutil.CollectAndCompare(c, strings.NewReader(tt.want)); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestConverters(t *testing.T) {
	tests := []struct {
		name    string
		convert func(json.RawMessage) (float64, bool)
		raw     string
		want    float64
		wantOK  bool
	}{
		{"temperature", scaled(100), `2150`, 21.5, true},
		{"negative temperature", scaled(100), `-990`, -9.9, true},
		{"battery half-percent", scaled(2), `200`, 100, true},
		{"power mW", scaled(1000), `1500`, 1.5, true},
		{"number of wrong type", scaled(100), `"21"`, 0, false},
		{"boolean true", boolean, `true`, 1, true},
		{"boolean false", boolean, `false`, 0, true},
		{"boolean of wrong type", boolean, `1`, 0, false},
		{"illuminance too low", illuminance, `0`, 0, true},
		{"illuminance 1 lux", illuminance, `1`, 1, true},
		{"illuminance 100 lux", illuminance, `20001`, 100, true},
		{"occupied", occupancy, `1`, 1, true},
		{"occupied with other bits", occupancy, `3`, 1, true},
		{"unoccupied", occupancy, `0`, 0, true},
		{"energy mWh", cumulativeEnergy, `{"0":1500}`, 1.5, true},
		{"energy with extra fields", cumulativeEnergy, `{"0":2000,"3":16634}`, 2, true},
		{"energy without value", cumulativeEnergy, `{"3":16634}`, 0, false},
		{"energy of wrong type", cumulativeEnergy, `5`, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.convert(json.RawMessage(tt.raw))
			if ok != tt.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tt.wantOK)
			}
			if ok && math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
