package metrics

import "github.com/prometheus/client_golang/prometheus"

// When adding metric names, see https://prometheus.io/docs/practices/naming/#metric-names
const (
	podLabelsUsedQuery     = "nto_pod_labels_used_info"
	profileCalculatedQuery = "nto_profile_calculated_total"
	buildInfoQuery         = "nto_build_info"
	degradedInfoQuery      = "nto_degraded_info"

	// Port is the IP port supplied to the HTTP server used for Prometheus,
	// and matches what is specified in the corresponding Service and ServiceMonitor.
	Port = 60000
)

var (
	Registry      = prometheus.NewRegistry()
	podLabelsUsed = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: podLabelsUsedQuery,
			Help: "Is the Pod label functionality turned on (1) or off (0)?",
		},
	)
	profileCalculated = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: profileCalculatedQuery,
			Help: "The number of times a Tuned profile was calculated for a given node.",
		},
		[]string{"node", "profile"},
	)
	buildInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: buildInfoQuery,
			Help: "A metric with a constant '1' value labeled version from which Node Tuning Operator was built.",
		},
		[]string{"version"},
	)
	degradedState = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: degradedInfoQuery,
			Help: "Indicates whether the Node Tuning Operator is degraded.",
		},
	)
)

func init() {
	Registry.MustRegister(
		podLabelsUsed,
		profileCalculated,
		buildInfo,
		degradedState,
	)
}

// PodLabelsUsed indicates whether the deprecated Pod label matching functionality
// is turned on.
func PodLabelsUsed(enable bool) {
	if enable {
		podLabelsUsed.Set(1)
		return
	}
	podLabelsUsed.Set(0)
}

// ProfileCalculated keeps track of the number of times a given Tuned profile
// resource was calculated for node 'nodeName'.
func ProfileCalculated(nodeName, profileName string) {
	profileCalculated.With(map[string]string{"node": nodeName, "profile": profileName}).Inc()
}

// RegisterVersion exposes the Operator build version.
func RegisterVersion(version string) {
	buildInfo.WithLabelValues(version).Set(1)
}

// Degraded sets the metric that indicates whether the operator is in degraded
// mode or not.
func Degraded(deg bool) {
	if deg {
		degradedState.Set(1)
		return
	}
	degradedState.Set(0)
}
