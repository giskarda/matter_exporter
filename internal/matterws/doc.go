// Package matterws is a passive client for the matterjs-server WebSocket API
// (API-compatible with python-matter-server).
//
// # Observed protocol
//
// Shapes marked "observed" were captured from a live matterjs-server
// (sdk_version "matter-server/1.5.5 (matter.js/0.18.0)", schema_version 14);
// see testdata/capture.jsonl. Shapes marked "reference" come from the
// python-matter-server API and have not yet been seen on the wire.
//
// All messages are JSON text frames.
//
// 1. Server info (observed). Sent by the server right after the connection is
// established, without a message_id:
//
//	{"fabric_id":1,"compressed_fabric_id":16617035897271205996,"fabric_index":1,
//	 "schema_version":14,"min_supported_schema_version":11,
//	 "sdk_version":"matter-server/1.5.5 (matter.js/0.18.0)",
//	 "wifi_credentials_set":false,"thread_credentials_set":true,
//	 "bluetooth_enabled":false,"ble_proxy_enabled":false,"controller_node_id":112233}
//
// 2. Command (observed). The only command this client ever sends:
//
//	{"message_id":"1","command":"start_listening","args":{}}
//
// 3. Command result (observed). The start_listening result is the full node
// dump, an array of node objects:
//
//	{"message_id":"1","result":[{"node_id":4,"date_commissioned":"...",
//	 "last_interview":"...","interview_version":6,"available":true,
//	 "is_bridge":false,"attributes":{"0/40/1":"Eve Systems", ...},
//	 "attribute_subscriptions":[],"matter_version":"1.3.0",
//	 "endpoint_parents":{"1":0,"2":0}}, ...]}
//
// A failed command (reference) is answered with
//
//	{"message_id":"1","error_code":<int>,"details":"..."}
//
// 4. Events. Sent at any time after start_listening, without a message_id:
//
//	{"event":"attribute_updated","data":[4,"2/144/4",211100]}   (observed)
//	{"event":"node_added","data":{<node object>}}               (reference)
//	{"event":"node_updated","data":{<node object>}}             (reference)
//	{"event":"node_removed","data":<node_id>}                   (reference)
//	{"event":"node_event","data":{"node_id":5,"endpoint_id":2,"cluster_id":145,
//	   "event_id":0,"event_number":5898319,"priority":1,"timestamp":242644342,
//	   "timestamp_type":0,"data":{"energyImported":{"energy":109285000},
//	   "energyExported":{"energy":0}}}}                         (observed)
//	{"event":"endpoint_added","data":{"node_id":..,"endpoint_id":..}}   (reference)
//	{"event":"endpoint_removed","data":{"node_id":..,"endpoint_id":..}} (reference)
//	{"event":"server_shutdown","data":null}                     (reference)
//
// # Encoding notes (observed)
//
//   - Attribute paths are "<endpoint>/<cluster>/<attribute>", all decimal.
//   - Struct values in attributes are JSON objects keyed by the decimal field
//     tag, not by field name: a DeviceTypeStruct is
//     {"0":<device type>,"1":<revision>}. Event payloads (node_event "data")
//     are different: they use camelCase field names.
//   - 64-bit integers are sent as plain JSON numbers and routinely exceed
//     2^53 (compressed_fabric_id, Thread extended addresses). Never decode
//     them through float64: use uint64 fields or json.Number.
//   - Null attribute values are JSON null and mean "unknown".
//   - Octet strings are base64 text ("0/53/60":"AB//4A==").
package matterws
