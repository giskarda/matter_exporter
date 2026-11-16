// Package web serves the human-facing pages: /devices and the health
// endpoint.
package web

import (
	"html/template"
	"net/http"

	"github.com/giskarda/matter_exporter/internal/state"
)

// RoomFunc resolves the room of a node; nil when enrichment is disabled.
type RoomFunc func(state.Node) string

type deviceRow struct {
	NodeID    uint64
	UniqueID  string
	Serial    string
	Vendor    string
	Product   string
	Name      string
	Room      string
	Available bool
}

type devicesPage struct {
	Connected bool
	HasRooms  bool
	Rows      []deviceRow
}

var devicesTmpl = template.Must(template.New("devices").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>matter_exporter devices</title>
<style>
body { font-family: sans-serif; margin: 2em; }
table { border-collapse: collapse; }
th, td { border: 1px solid #ccc; padding: .3em .7em; text-align: left; }
th { background: #eee; }
code { font-size: 1.1em; }
.warn { color: #a40000; }
</style>
</head>
<body>
<h1>Matter devices</h1>
{{if not .Connected}}<p class="warn">Not connected to the Matter server: this is the last known state.</p>{{end}}
<table>
<tr><th>node_id</th><th>unique_id</th><th>serial</th><th>vendor</th><th>product</th><th>name</th>{{if .HasRooms}}<th>room</th>{{end}}<th>available</th></tr>
{{range .Rows}}<tr><td>{{.NodeID}}</td><td><code>{{.UniqueID}}</code></td><td><code>{{.Serial}}</code></td><td>{{.Vendor}}</td><td>{{.Product}}</td><td>{{.Name}}</td>{{if $.HasRooms}}<td>{{.Room}}</td>{{end}}<td>{{if .Available}}yes{{else}}no{{end}}</td></tr>
{{else}}<tr><td colspan="{{if .HasRooms}}8{{else}}7{{end}}">No nodes.</td></tr>
{{end}}</table>
</body>
</html>
`))

// DevicesHandler renders the table of discovered nodes, the place to look up
// the identifiers for rooms.yaml.
func DevicesHandler(src state.Source, room RoomFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		snap := src.Snapshot()
		page := devicesPage{Connected: snap.Connected, HasRooms: room != nil}
		for _, n := range snap.Nodes {
			bi := n.BasicInfo()
			row := deviceRow{
				NodeID:    n.ID,
				UniqueID:  bi.UniqueID,
				Serial:    bi.Serial,
				Vendor:    bi.VendorName,
				Product:   bi.ProductName,
				Name:      bi.NodeLabel,
				Available: n.Available,
			}
			if room != nil {
				row.Room = room(n)
			}
			page.Rows = append(page.Rows, row)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = devicesTmpl.Execute(w, page)
	})
}

// HealthyHandler is the liveness endpoint. It reports the process, not the
// server connection: a disconnected exporter must stay up to keep retrying
// and to export matter_exporter_server_connected 0.
func HealthyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("matter_exporter is healthy.\n"))
	})
}
