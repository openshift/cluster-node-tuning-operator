/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

*/

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	apiconfigv1 "github.com/openshift/api/config/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"

	v1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/metrics"
	"github.com/openshift/cluster-node-tuning-operator/pkg/operator"
	"github.com/openshift/cluster-node-tuning-operator/pkg/signals"
	"github.com/openshift/cluster-node-tuning-operator/pkg/tuned"
	"github.com/openshift/cluster-node-tuning-operator/version"
)

const (
	operandFilename  = "openshift-tuned"
	operatorFilename = "cluster-node-tuning-operator"
	metricsHost      = "0.0.0.0"
	leaderElectionID = "node-tuning-operator-lock"
)

var (
	// values below taken from:
	// https://github.com/openshift/enhancements/pull/832/files#diff-2e28754e69aa417e5b6d89e99e42f05bfb6330800fa823753383db1d170fbc2fR183
	// see rhbz#1986477 for more detail
	leaseDuration = 137 * time.Second
	renewDeadline = 107 * time.Second
	retryPeriod   = 26 * time.Second
)

var (
	scheme = apiruntime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1.AddToScheme(scheme))
	utilruntime.Must(mcov1.AddToScheme(scheme))
	utilruntime.Must(apiconfigv1.Install(scheme))
}

func printVersion() {
	klog.Infof("Go Version: %s", runtime.Version())
	klog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	klog.Infof("%s Version: %s", v1.TunedClusterOperatorResourceName, version.Version)
}

func main() {
	var boolVersion bool
	flag.BoolVar(&boolVersion, "version", false, "show program version and exit")

	runAs := filepath.Base(os.Args[0])

	switch runAs {
	case operatorFilename:
		var metricsAddr string
		flag.StringVar(&metricsAddr, "metrics-addr", fmt.Sprintf("%s:%d", metricsHost, metrics.Port),
			"The address the metric endpoint binds to.")

		klog.InitFlags(nil)
		flag.Parse()

		printVersion()

		if boolVersion {
			os.Exit(0)
		}

		namespace := config.OperatorNamespace()
		mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
			Scheme:                  scheme,
			MetricsBindAddress:      metricsAddr,
			LeaderElection:          true,
			LeaderElectionID:        leaderElectionID,
			LeaderElectionNamespace: namespace,
			LeaseDuration:           &leaseDuration,
			RetryPeriod:             &retryPeriod,
			RenewDeadline:           &renewDeadline,
			Namespace:               namespace,
		})
		if err != nil {
			klog.Exit(err)
		}

		handler := promhttp.HandlerFor(
			metrics.Registry,
			promhttp.HandlerOpts{
				ErrorHandling: promhttp.HTTPErrorOnError,
			},
		)
		if err := mgr.AddMetricsExtraHandler("/nto-metrics", handler); err != nil {
			klog.Error(err)
		}

		// TODO: start the metrics server before, metrics.RegisterVersion(version.Version)
		// go metrics.RunServer(metrics.MetricsPort, stopCh)
		// metrics.RegisterVersion(version.Version)

		if err = (&operator.Reconciler{
			Client:    mgr.GetClient(),
			Scheme:    mgr.GetScheme(),
			Namespace: namespace,
		}).SetupWithManager(mgr); err != nil {
			klog.Exitf("unable to create node tuning operator controller: %v", err)
		}

		if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
			klog.Exitf("Manager exited with non-zero code: %v", err)
		}
	case operandFilename:
		stopCh := signals.SetupSignalHandler()
		tuned.Run(stopCh, &boolVersion, version.Version)
	default:
		klog.Fatalf("application should be run as %q or %q", operatorFilename, operandFilename)
	}
}
