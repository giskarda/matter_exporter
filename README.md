# matter_exporter

Prometheus exporter for [Matter](https://csa-iot.org/all-solutions/matter/) smart-home networks.

`matter_exporter` connects to a [matterjs-server](https://github.com/matter-js/matterjs-server)
(or any server speaking the python-matter-server WebSocket API) and exposes the state of every
commissioned node — availability, device metadata, sensor values, and Thread network
diagnostics — as Prometheus metrics.

> **Status:** early development. Metric names and labels may change before v1.0.

## How it works

```
 Matter devices ──(Thread / Wi-Fi)──> matterjs-server ──(WebSocket /ws)──> matter_exporter ──(/metrics)──> Prometheus
```

- The exporter **never talks Matter directly**. Sessions, subscriptions and reconnections are
  owned by matterjs-server; the exporter is a passive WebSocket client (`start_listening`) and
  adds no traffic to the Thread mesh.
- Nodes and endpoints are **discovered**, not configured. The exporter keeps an in-memory
  snapshot of all nodes, updated from server events (`node_added`, `node_updated`,
  `node_removed`, `attribute_updated`, `node_event`).
- On every scrape it emits the **current snapshot**. Nodes that are removed from the fabric simply
  disappear from the output (Prometheus marks their series stale); nodes that are known but
  unreachable keep reporting `matter_node_available 0` and stop reporting values, so stale
  readings are never presented as current.
- While the exporter itself is **disconnected from the server**, its picture of the nodes is
  stale, so it exports no node metrics at all — only its self-metrics
  (`matter_exporter_server_connected 0`) and the event counters. Alert on
  `matter_exporter_server_connected == 0` rather than on missing node series. After reconnecting,
  the state is rebuilt from a fresh node dump.

## Metrics

### Node and endpoint metadata

| Metric | Labels | Source |
|---|---|---|
| `matter_node_available` | `node_id` | server node state |
| `matter_node_info` | `node_id`, `unique_id`, `vendor`, `vendor_id`, `product`, `product_id`, `name`, `sw_version`, `serial` | Basic Information cluster (0x0028), endpoint 0 |
| `matter_endpoint_info` | `node_id`, `endpoint`, `device_type` | Descriptor cluster (0x001D) |
| `matter_node_room` | `node_id`, `room` | optional enrichment (see below) |

`name` is the device's own `NodeLabel` (0/40/5). Set it once from your controller and every
consumer — including this exporter — will see it.

### Sensor values (curated, emitted only when the cluster is present and the node is available)

| Metric | Unit | Cluster |
|---|---|---|
| `matter_temperature_celsius` | °C | Temperature Measurement (0x0402) |
| `matter_relative_humidity_percent` | % | Relative Humidity Measurement (0x0405) |
| `matter_pressure_hectopascals` | hPa | Pressure Measurement (0x0403) |
| `matter_illuminance_lux` | lx | Illuminance Measurement (0x0400) |
| `matter_occupancy` | 0/1 | Occupancy Sensing (0x0406) |
| `matter_contact_state` | 0/1 | Boolean State (0x0045) |
| `matter_onoff` | 0/1 | On/Off (0x0006) |
| `matter_battery_percent` | % | Power Source (0x002F) |
| `matter_active_power_watts` | W | Electrical Power Measurement (0x0090) |
| `matter_energy_watt_hours_total` | Wh | Electrical Energy Measurement (0x0091) |

All value metrics carry `node_id` and `endpoint` labels.

### Events (counters)

Prometheus samples state at scrape time, so short-lived changes (a button press, a door opened and
closed within seconds) would be lost as gauges. They are exported as counters instead:

| Metric | Labels |
|---|---|
| `matter_switch_events_total` | `node_id`, `endpoint`, `event` |
| `matter_contact_changes_total` | `node_id`, `endpoint` |
| `matter_node_availability_changes_total` | `node_id`, `to` |

### Thread network diagnostics

From the Thread Network Diagnostics cluster (0x0035), when exposed by the device:

| Metric | Labels |
|---|---|
| `matter_thread_routing_role` | `node_id`, `role` |
| `matter_thread_neighbor_rssi_dbm` | `node_id`, `neighbor_ext_addr` |
| `matter_thread_neighbor_lqi` | `node_id`, `neighbor_ext_addr` |

### Exporter self-metrics

| Metric | Description |
|---|---|
| `matter_exporter_server_connected` | 1 while the WebSocket connection is up |
| `matter_exporter_reconnects_total` | WebSocket reconnections |
| `matter_exporter_events_total{type}` | events received from the server |
| `matter_exporter_last_event_timestamp_seconds` | time of the last event |
| `matter_exporter_build_info{version,revision,goversion}` | build information of the running binary; always 1 |

## Room enrichment (optional)

Matter has no notion of rooms. If you want a `room` dimension, provide a mapping file keyed by the
device's `UniqueID` (stable across re-commissioning, unlike `node_id`):

```yaml
# rooms.yaml
rooms:
  living_room:
    - "4F2A9C1E7B3D5A60"
  bathroom:
    - "9B1D3E5F7A2C4E68"
  bedroom:
    - "1C3E5A7B9D2F4A06"
```

Nodes not in the file are exported normally, with `room="unassigned"`. The file is reloaded on
`SIGHUP`. To find the IDs, open `http://<exporter>/devices`.

The room is exposed as a separate info metric and joined at query time:

```promql
# temperature by room
matter_temperature_celsius
  * on(node_id) group_left(room) matter_node_room

# human-readable names
matter_temperature_celsius
  * on(node_id) group_left(name, product) matter_node_info
```

## Usage

```sh
matter_exporter \
  --matter.server-url=ws://localhost:5580/ws \
  --enrich.rooms-file=/etc/matter_exporter/rooms.yaml \
  --web.listen-address=:10063
```

| Endpoint | Description |
|---|---|
| `/metrics` | Prometheus metrics |
| `/devices` | table of discovered nodes (`node_id`, `unique_id`, product, name, room) |
| `/-/healthy` | liveness |

## Building

```sh
go build ./cmd/matter_exporter
go test ./...
```

## License

Apache-2.0
