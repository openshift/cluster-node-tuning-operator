package main

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"

	apiconfigv1 "github.com/openshift/api/config/v1"
	performancev1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1"
	performancev1alpha1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1alpha1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	paocontroller "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	olmv1 "github.com/operator-framework/api/pkg/operators/v1"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/metrics"
	"github.com/openshift/cluster-node-tuning-operator/pkg/operator"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/cmd/render"
	"github.com/openshift/cluster-node-tuning-operator/pkg/signals"
	"github.com/openshift/cluster-node-tuning-operator/pkg/tuned"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/openshift/cluster-node-tuning-operator/version"
)

const (
	operandFilename  = "openshift-tuned"
	operatorFilename = "cluster-node-tuning-operator"
	webhookPort      = 4343
	webhookCertDir   = "/apiserver.local.config/certificates"
	webhookCertName  = "apiserver.crt"
	webhookKeyName   = "apiserver.key"
)

var (
	scheme = apiruntime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(tunedv1.AddToScheme(scheme))
	utilruntime.Must(mcov1.AddToScheme(scheme))
	utilruntime.Must(apiconfigv1.Install(scheme))
	utilruntime.Must(performancev1alpha1.AddToScheme(scheme))
	utilruntime.Must(performancev1.AddToScheme(scheme))
	utilruntime.Must(performancev2.AddToScheme(scheme))
	utilruntime.Must(olmv1alpha1.AddToScheme(scheme))
	utilruntime.Must(olmv1.AddToScheme(scheme))
}

func printVersion() {
	klog.Infof("Go Version: %s", runtime.Version())
	klog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	klog.Infof("%s Version: %s", tunedv1.TunedClusterOperatorResourceName, version.Version)
}

var rootCmd = &cobra.Command{
	Use:   operatorFilename,
	Short: "NTO manages the containerized TuneD instances",
	Run: func(cmd *cobra.Command, args []string) {
		operatorRun()
	},
}

var enableLeaderElection bool
var showVersionAndExit bool

func prepareCommands() {
	rootCmd.PersistentFlags().BoolVar(&enableLeaderElection, "enable-leader-election", true,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	rootCmd.PersistentFlags().BoolVar(&showVersionAndExit, "version", false,
		"Show program version and exit.")

	// Include the klog command line arguments
	klog.InitFlags(nil)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	rootCmd.AddCommand(render.NewRenderCommand())
}

func operatorRun() {
	printVersion()

	if showVersionAndExit {
		return
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

	controller, err := operator.NewController()
	if err != nil {
		klog.Fatalf("failed to create new controller: %v", err)
	}

	if err := mgr.Add(controller); err != nil {
		klog.Fatalf("failed to add new controller to the manager: %v", err)
	}

	if err := mgr.Add(metrics.Server{}); err != nil {
		klog.Fatalf("unable to add metrics server as runnable under the manager: %v", err)
	}
	metrics.RegisterVersion(version.Version)

	if err = (&paocontroller.PerformanceProfileReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("performance-profile-controller"),
	}).SetupWithManager(mgr); err != nil {
		klog.Exitf("unable to create PerformanceProfile controller: %v", err)
	}

	// Configure webhook server.
	webHookServer := mgr.GetWebhookServer()
	webHookServer.Port = webhookPort
	webHookServer.CertDir = webhookCertDir
	webHookServer.CertName = webhookCertName
	webHookServer.KeyName = webhookKeyName

	if err = (&performancev1.PerformanceProfile{}).SetupWebhookWithManager(mgr); err != nil {
		klog.Exitf("unable to create PerformanceProfile v1 webhook: %v", err)
	}

	if err = (&performancev2.PerformanceProfile{}).SetupWebhookWithManager(mgr); err != nil {
		klog.Exitf("unable to create PerformanceProfile v2 webhook: %v", err)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		klog.Exitf("manager exited with non-zero code: %v", err)
	}
}

func tunedOperandRun() {
	var boolVersion bool
	flag.BoolVar(&boolVersion, "version", false, "show program version and exit")

	// flag.Parse is called from within tuned.Run -> parseCmdOpts
	// but the version flag variable is inherited from here..

	stopCh := signals.SetupSignalHandler()
	tuned.Run(stopCh, &boolVersion, version.Version)
}

func main() {
	runAs := filepath.Base(os.Args[0])

	switch runAs {
	case operatorFilename:
		prepareCommands()
		_ = rootCmd.Execute()
	case operandFilename:
		tunedOperandRun()
	default:
		klog.Fatalf("application should be run as \"%s\" or \"%s\"", operatorFilename, operandFilename)
	}
}
