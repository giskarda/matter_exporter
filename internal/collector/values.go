package collector

import (
	"encoding/json"
	"math"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/giskarda/matter_exporter/internal/state"
)

// valueMetric maps one cluster attribute to one semantic metric.
type valueMetric struct {
	desc      *prometheus.Desc
	valueType prometheus.ValueType
	cluster   uint32
	attribute uint32
	// convert turns the raw (non-null) attribute into the exported value;
	// false skips the sample.
	convert func(json.RawMessage) (float64, bool)
}

func valueDesc(name, help string) *prometheus.Desc {
	return prometheus.NewDesc(name, help, endpointLabels, nil)
}

// valueMetrics is the curated cluster → metric table (see README).
var valueMetrics = []valueMetric{
	{
		desc:      valueDesc("matter_temperature_celsius", "Temperature Measurement (0x0402) MeasuredValue in degrees Celsius."),
		valueType: prometheus.GaugeValue, cluster: 0x0402, attribute: 0,
		convert: scaled(100), // 0.01 °C
	},
	{
		desc:      valueDesc("matter_relative_humidity_percent", "Relative Humidity Measurement (0x0405) MeasuredValue in percent."),
		valueType: prometheus.GaugeValue, cluster: 0x0405, attribute: 0,
		convert: scaled(100), // 0.01 %
	},
	{
		desc:      valueDesc("matter_pressure_hectopascals", "Pressure Measurement (0x0403) MeasuredValue in hectopascals."),
		valueType: prometheus.GaugeValue, cluster: 0x0403, attribute: 0,
		convert: scaled(1), // 0.1 kPa = 1 hPa
	},
	{
		desc:      valueDesc("matter_illuminance_lux", "Illuminance Measurement (0x0400) MeasuredValue in lux."),
		valueType: prometheus.GaugeValue, cluster: 0x0400, attribute: 0,
		convert: illuminance,
	},
	{
		desc:      valueDesc("matter_occupancy", "Occupancy Sensing (0x0406): 1 if occupied, 0 otherwise."),
		valueType: prometheus.GaugeValue, cluster: 0x0406, attribute: 0,
		convert: occupancy,
	},
	{
		desc:      valueDesc("matter_contact_state", "Boolean State (0x0045) StateValue: 1 if true (contact closed), 0 otherwise."),
		valueType: prometheus.GaugeValue, cluster: 0x0045, attribute: 0,
		convert: boolean,
	},
	{
		desc:      valueDesc("matter_onoff", "On/Off (0x0006): 1 if on, 0 if off."),
		valueType: prometheus.GaugeValue, cluster: 0x0006, attribute: 0,
		convert: boolean,
	},
	{
		desc:      valueDesc("matter_battery_percent", "Power Source (0x002F) BatPercentRemaining in percent."),
		valueType: prometheus.GaugeValue, cluster: 0x002F, attribute: 12,
		convert: scaled(2), // half-percent
	},
	{
		desc:      valueDesc("matter_active_power_watts", "Electrical Power Measurement (0x0090) ActivePower in watts."),
		valueType: prometheus.GaugeValue, cluster: 0x0090, attribute: 8,
		convert: scaled(1000), // mW
	},
	{
		desc:      valueDesc("matter_energy_watt_hours_total", "Electrical Energy Measurement (0x0091) CumulativeEnergyImported in watt-hours."),
		valueType: prometheus.CounterValue, cluster: 0x0091, attribute: 1,
		convert: cumulativeEnergy,
	},
}

// collectValues emits the value metrics of one endpoint. A metric is emitted
// only if the Descriptor ServerList advertises its cluster and the attribute
// has a non-null value.
func collectValues(ch chan<- prometheus.Metric, n state.Node, ep uint16) {
	for _, m := range valueMetrics {
		if !n.HasCluster(ep, m.cluster) {
			continue
		}
		raw, ok := n.Raw(ep, m.cluster, m.attribute)
		if !ok {
			continue
		}
		v, ok := m.convert(raw)
		if !ok {
			continue
		}
		ch <- prometheus.MustNewConstMetric(m.desc, m.valueType, v, nodeID(n.ID), endpointID(ep))
	}
}

func number(raw json.RawMessage) (float64, bool) {
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		return 0, false
	}
	return f, true
}

// scaled converts a number expressed in 1/div units.
func scaled(div float64) func(json.RawMessage) (float64, bool) {
	return func(raw json.RawMessage) (float64, bool) {
		f, ok := number(raw)
		return f / div, ok
	}
}

func boolean(raw json.RawMessage) (float64, bool) {
	var b bool
	if json.Unmarshal(raw, &b) != nil {
		return 0, false
	}
	return boolValue(b), true
}

// illuminance decodes MeasuredValue = 10000*log10(lux)+1; 0 means "too low
// to be measured" and is exported as 0 lux.
func illuminance(raw json.RawMessage) (float64, bool) {
	f, ok := number(raw)
	if !ok {
		return 0, false
	}
	if f == 0 {
		return 0, true
	}
	return math.Pow(10, (f-1)/10000), true
}

// occupancy decodes the Occupancy bitmap: bit 0 is "occupied".
func occupancy(raw json.RawMessage) (float64, bool) {
	var bits uint64
	if json.Unmarshal(raw, &bits) != nil {
		return 0, false
	}
	return float64(bits & 1), true
}

// cumulativeEnergy decodes an EnergyMeasurementStruct; tag 0 is the energy
// in mWh.
func cumulativeEnergy(raw json.RawMessage) (float64, bool) {
	var m struct {
		Energy *float64 `json:"0"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Energy == nil {
		return 0, false
	}
	return *m.Energy / 1000, true
}
