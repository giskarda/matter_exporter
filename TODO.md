# TODO

## Bridged devices (Bridged Device Basic Information, 0x0039)

Not implemented: there is no bridge on the network the exporter was developed
against, so there is no capture to verify message shapes with. A bridge (Hue,
Aqara, a Zigbee hub exposed over Matter, ...) is a single node with one
endpoint per physical device. With the current code:

- Value metrics work: they are emitted per `endpoint`.
- **Stale values (most serious).** The per-device `Reachable` attribute
  (0x0039, attribute 17) is not read, so a bridged device that is offline
  keeps exporting its last value. This breaks the rule that stale readings
  are never presented as current.
- **No per-device metadata.** `matter_node_info` describes the bridge only;
  the devices behind it have no name, vendor, product or serial, and
  `/devices` shows a single row for the whole bridge.
- **Rooms are per node.** Every device behind a bridge ends up in the
  bridge's room.

To do, once a real bridge can be captured into `testdata/`:

1. Capture and anonymise a dump with a bridge; document the observed shapes.
2. Extend the README contract first (CLAUDE.md: README is the contract):
   an info metric for bridged devices (e.g. `matter_bridged_device_info`
   with `node_id`, `endpoint` and the 0x0039 metadata) and their
   reachability (e.g. `matter_bridged_device_reachable`).
3. Suppress value metrics for unreachable bridged endpoints, like for
   unavailable nodes.
4. Let `rooms.yaml` assign rooms per bridged device (0x0039 UniqueID 18 /
   SerialNumber 15) and decide how `matter_node_room` carries the endpoint.
5. List bridged devices on `/devices`.
