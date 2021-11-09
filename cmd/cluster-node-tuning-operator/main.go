package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	apiconfigv1 "github.com/openshift/api/config/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/metrics"
	"github.com/openshift/cluster-node-tuning-operator/pkg/operator"
	"github.com/openshift/cluster-node-tuning-operator/pkg/signals"
	"github.com/openshift/cluster-node-tuning-operator/pkg/tuned"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/openshift/cluster-node-tuning-operator/version"
)

const (
	operandFilename  = "openshift-tuned"
	operatorFilename = "cluster-node-tuning-operator"
	metricsHost      = "0.0.0.0"
)

var (
	scheme = apiruntime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(tunedv1.AddToScheme(scheme))
	utilruntime.Must(mcov1.AddToScheme(scheme))
	utilruntime.Must(apiconfigv1.Install(scheme))
}

func printVersion() {
	klog.Infof("Go Version: %s", runtime.Version())
	klog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	klog.Infof("%s Version: %s", tunedv1.TunedClusterOperatorResourceName, version.Version)
}

func main() {
	var boolVersion bool
	var enableLeaderElection bool
	flag.BoolVar(&boolVersion, "version", false, "show program version and exit")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	runAs := filepath.Base(os.Args[0])

	switch runAs {
	case operatorFilename:
		var metricsAddr string
		flag.StringVar(&metricsAddr, "metrics-addr", fmt.Sprintf("%s:%d", metricsHost, metrics.MetricsPort),
			"The address the metric endpoint binds to.")

		klog.InitFlags(nil)
		flag.Parse()

		printVersion()

		if boolVersion {
			os.Exit(0)
		}

		// We have two namespaces that we need to watch:
		// 1. NTO namespace - for NTO resources
		// 2. None namespace - for cluster wide resources
		ntoNamespace := config.OperatorNamespace()
		namespaces := []string{
			ntoNamespace,
			metav1.NamespaceNone,
		}

		restConfig := ctrl.GetConfigOrDie()
		le := util.GetLeaderElectionConfig(restConfig, enableLeaderElection)
		mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
			NewCache:                cache.MultiNamespacedCacheBuilder(namespaces),
			Scheme:                  scheme,
			LeaderElection:          true,
			LeaderElectionID:        config.OperatorLockName,
			LeaderElectionNamespace: ntoNamespace,
			LeaseDuration:           &le.LeaseDuration.Duration,
			RetryPeriod:             &le.RetryPeriod.Duration,
			RenewDeadline:           &le.RenewDeadline.Duration,
			Namespace:               ntoNamespace,
		})

		if err != nil {
			klog.Exit(err)
		}

		mgr.Add(metrics.Server{})
		metrics.RegisterVersion(version.Version)

		if err = (&operator.Reconciler{
			Client:    mgr.GetClient(),
			Scheme:    mgr.GetScheme(),
			Namespace: ntoNamespace,
		}).SetupWithManager(mgr); err != nil {
			klog.Exitf("unable to create node tuning operator controller: %v", err)
		}

		if err = (&operator.TunedReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Recorder: mgr.GetEventRecorderFor("tuned-controller"),
		}).SetupWithManager(mgr); err != nil {
			klog.Exitf("unable to create Tuned controller: %v", err)
		}

		if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
			klog.Exitf("manager exited with non-zero code: %v", err)
		}
	case operandFilename:
		stopCh := signals.SetupSignalHandler()
		tuned.Run(stopCh, &boolVersion, version.Version)
	default:
		klog.Fatalf("application should be run as %q or %q", operatorFilename, operandFilename)
	}
}
