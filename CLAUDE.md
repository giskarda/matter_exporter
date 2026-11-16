# CLAUDE.md — matter_exporter

Prometheus exporter for a home Matter-over-Thread network, written in Go.
Read README.md first: it is the user-facing contract (metric names, labels, flags).
If an implementation decision contradicts README.md, stop and ask before changing either.

## Environment

- Matter controller: **matterjs-server**, running in podman (quadlet) on the home server, Debian sid.
  WebSocket API at `ws://<home-server>:5580/ws` (default port; confirm on the live host).
- Thread border router: OTBR on the same host (nRF52840 RCP dongle).
- matterjs-server is API-compatible with the (archived) python-matter-server WebSocket API.
  Use that API as the reference, but **verify every message shape against the live server**
  before coding against it.
- Deployment target: podman quadlet next to matterjs-server.

## Design decisions (already agreed — do not revisit without asking)

1. **Passive client only.** The exporter never opens Matter sessions itself. It connects to the
   server WebSocket, sends `start_listening`, receives the full node dump, then applies events
   (`node_added`, `node_updated`, `node_removed`, `attribute_updated`, `node_event`,
   `server_shutdown`). Reconnect with backoff; on reconnect, rebuild state from the fresh dump.
2. **Snapshot collector.** Implement a custom `prometheus.Collector` with an **empty `Describe`**
   (unchecked collector). `Collect` takes a snapshot of the state (copy under lock) and emits
   `prometheus.MustNewConstMetric(...)`. Do NOT use global `GaugeVec`s updated from events:
   removed nodes/endpoints must disappear from the output automatically.
3. **Offline vs removed.** Known-but-unavailable nodes: emit `matter_node_available 0` and the info
   metrics, but no value metrics. Removed nodes: emit nothing.
4. **Labels.** Only stable, bounded identifiers in value-metric labels: `node_id`, `endpoint`.
   Human metadata goes in info metrics (`matter_node_info`, `matter_endpoint_info`) joined at
   query time. Never put IPs, timestamps or free text in value-metric labels.
5. **Metadata comes from discovery, not config.**
   - Node: Basic Information cluster 0x0028 (40) on endpoint 0 — VendorName 1, VendorID 2,
     ProductName 3, ProductID 4, NodeLabel 5, SoftwareVersionString 10, SerialNumber 15,
     UniqueID 18.
   - Bridged endpoints: Bridged Device Basic Information 0x0039 (57).
   - Endpoint: Descriptor 0x001D (29) — DeviceTypeList 0, ServerList 1 (use ServerList to decide
     which metrics an endpoint can produce).
   - Attribute paths in the server dump are `"<endpoint>/<cluster>/<attribute>"`, decimal.
6. **Curated metrics, not a generic dump.** Map known clusters to semantic metrics with correct
   units (see README table). Watch the encodings: Temperature and Humidity MeasuredValue are in
   0.01 units; Pressure in 0.1 kPa; Illuminance is `10000*log10(lux)+1`; BatPercentRemaining is
   in half-percent; ActivePower is in mW; null means "unknown" → skip the sample.
   A generic `matter_attribute_value` metric may be added later behind a flag, off by default.
7. **Events as counters.** Switch events, contact changes and availability transitions are
   counters incremented on event arrival — gauges would miss changes between scrapes.
8. **Room enrichment is a separate, optional package** (`internal/enrich`). It reads `rooms.yaml`
   keyed by UniqueID (fallback: SerialNumber, then node_id with a warning), translates to the
   current `node_id`, and emits `matter_node_room{node_id,room}`. Unmapped nodes get
   `room="unassigned"`. Reload on SIGHUP. The core collector must work with enrichment disabled.
9. **No room/label hacks inside the core.** The core collector stays generic and publishable.

## Layout

```
cmd/matter_exporter/     main, flags, wiring
internal/matterws/       WebSocket client, protocol types, reconnect/backoff
internal/state/          node store, event application, Snapshot()
internal/collector/      prometheus.Collector, cluster → metric mappings
internal/enrich/         rooms.yaml loading and matter_node_room
internal/web/            /devices page, health endpoints
testdata/                captured server dumps and event streams (anonymised)
deploy/                  podman quadlet, example rooms.yaml, example Prometheus rules
```

## Conventions

- Go stable, modules, `log/slog` for logging.
- Dependencies: `github.com/prometheus/client_golang`, `github.com/prometheus/exporter-toolkit`
  (for `--web.*` flags), a maintained WebSocket client (`github.com/coder/websocket`),
  `gopkg.in/yaml.v3`. Ask before adding anything else.
- Flags follow Prometheus style: `--matter.server-url`, `--enrich.rooms-file`,
  `--web.listen-address`, `--web.telemetry-path`.
- Metric naming per https://prometheus.io/docs/practices/naming/ — base units, `_total` for
  counters, `_info` for info metrics.
- Tests: table-driven; collector tests with `prometheus/testutil.CollectAndCompare`; protocol
  tests against a fake WebSocket server replaying files from `testdata/`.
- Commit messages in English, imperative mood.

## AI attribution (mandatory)

Every commit authored or co-authored by an AI assistant MUST declare it in the commit message
trailers. No exceptions, including small fixes, formatting and generated files.

Required trailers, at the end of the commit message after a blank line:

```
Co-Authored-By: <Assistant name> <Model name and version> <noreply address of the vendor>
AI-Assistant: <tool or interface used, with version if known>
AI-Model: <exact model identifier>
```

Example:

```
Add snapshot collector for node availability

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
AI-Assistant: Claude Code
AI-Model: claude-opus-5-5
```

Rules:
- Use the real model identifier of the session that produced the change; never guess or
  copy it from a previous commit.
- If several models contributed to one commit, add one `Co-Authored-By` and one `AI-Model`
  trailer per model.
- Commits written entirely by a human carry no AI trailers.
- Pull request descriptions follow the same rule: state which AI tool and model were used.

## First steps

1. **Capture reality.** Connect to the live server (e.g. `websocat ws://<home-server>:5580/ws`),
   send `{"message_id":"1","command":"start_listening","args":{}}`, and save the server-info
   message, the full node dump and a few minutes of events to `testdata/`. Anonymise serials and
   UniqueIDs before committing. Document the observed message shapes in `internal/matterws/`.
2. `internal/matterws` + `internal/state` with tests replaying the capture.
3. Collector with availability + info metrics only; verify output with `curl /metrics`.
4. Sensor value metrics for the clusters actually present in the capture.
5. Event counters, Thread diagnostics, self-metrics.
6. `internal/enrich` + `/devices`.
7. `deploy/` quadlet and example alerting rules (node unavailable > 15m, exporter disconnected).
