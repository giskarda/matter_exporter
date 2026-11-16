# testdata

`capture.jsonl` is a capture of a live matterjs-server session
(`matter-server/1.5.5 (matter.js/0.18.0)`, schema version 14), one WebSocket
message per line, in the order received:

1. the server-info message;
2. the `start_listening` result (full node dump: two Eve Energy plugs, three
   IKEA TIMMERFLOTTE temperature/humidity sensors, all on Thread);
3. five minutes of events (`attribute_updated`, one `node_event`).

It was anonymised before being committed:

- SerialNumber (`0/40/15`) and UniqueID (`0/40/18`) are replaced by fakes;
- Thread network name, PAN ID, extended PAN ID, mesh-local prefix and
  partition ID are replaced; extended addresses in the neighbor and route
  tables are replaced by fake 64-bit values (still above 2^53, on purpose);
- `compressed_fabric_id` is replaced (also still above 2^53);
- all attributes of Network Commissioning (49), General Diagnostics (51),
  Operational Credentials (62), Group Key Management (63) and of
  manufacturer-specific clusters are removed. The Descriptor ServerList still
  lists those clusters.

Everything else is as sent by the server.
