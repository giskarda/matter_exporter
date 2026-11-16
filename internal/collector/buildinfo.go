package collector

import "github.com/prometheus/client_golang/prometheus"

// NewBuildInfo returns a collector for matter_exporter_build_info, a constant
// 1 labelled with the version, VCS revision and Go version of the binary.
func NewBuildInfo(version, revision, goVersion string) prometheus.Collector {
	return prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "matter_exporter_build_info",
		Help: "Build information of matter_exporter; always 1.",
		ConstLabels: prometheus.Labels{
			"version":   version,
			"revision":  revision,
			"goversion": goVersion,
		},
	}, func() float64 { return 1 })
}
