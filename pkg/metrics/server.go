package metrics

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"k8s.io/klog/v2"
)

const (
	tlsCRT = "/etc/secrets/tls.crt"
	tlsKey = "/etc/secrets/tls.key"
)

// RunServer starts the metrics server.
func RunServer(port int, stopCh <-chan struct{}) {
	if port <= 0 {
		klog.Error("invalid port for metric server")
		return
	}

	handler := promhttp.HandlerFor(
		registry,
		promhttp.HandlerOpts{
			ErrorHandling: promhttp.HTTPErrorOnError,
		},
	)

	bindAddr := fmt.Sprintf(":%d", port)
	router := http.NewServeMux()
	router.Handle("/metrics", handler)
	srv := &http.Server{
		Addr:    bindAddr,
		Handler: router,
	}

	go func() {
		if err := srv.ListenAndServeTLS(tlsCRT, tlsKey); err != nil {
			klog.Errorf("error starting metrics server: %v", err)
		}
	}()
	<-stopCh
	if err := srv.Shutdown(context.Background()); err != http.ErrServerClosed {
		klog.Errorf("error stopping metrics listener: %v", err)
	}
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

// ProfileSet keeps track of the number of times a given Tuned profile
// resource was set for node 'nodeName'.
func ProfileSet(nodeName, profileName string) {
	profileSet.With(map[string]string{"node": nodeName, "profile": profileName}).Inc()
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
