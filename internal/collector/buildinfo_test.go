package collector

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestBuildInfo(t *testing.T) {
	tests := []struct {
		name                         string
		version, revision, goVersion string
		want                         string
	}{
		{
			name:    "stamped build",
			version: "v0.1.0", revision: "1cc11eeee00c", goVersion: "go1.27.4",
			want: `matter_exporter_build_info{goversion="go1.27.4",revision="1cc11eeee00c",version="v0.1.0"} 1`,
		},
		{
			name:    "build without VCS information",
			version: "(devel)", revision: "unknown", goVersion: "go1.27.4",
			want: `matter_exporter_build_info{goversion="go1.27.4",revision="unknown",version="(devel)"} 1`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := `
# HELP matter_exporter_build_info Build information of matter_exporter; always 1.
# TYPE matter_exporter_build_info gauge
` + tt.want + "\n"
			c := NewBuildInfo(tt.version, tt.revision, tt.goVersion)
			if err := testutil.CollectAndCompare(c, strings.NewReader(want)); err != nil {
				t.Error(err)
			}
		})
	}
}
