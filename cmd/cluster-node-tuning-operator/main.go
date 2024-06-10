package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"net/http"

	apiconfigv1 "github.com/openshift/api/config/v1"
	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	performancev1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1"
	performancev1alpha1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1alpha1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	paocontroller "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/handler"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/status"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	"github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/metrics"
	"github.com/openshift/cluster-node-tuning-operator/pkg/operator"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/cmd/render"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift"
	hcpcomponents "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/components"
	hcpstatus "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/status"
	"github.com/openshift/cluster-node-tuning-operator/pkg/tuned/cmd/operand"
	tunedrender "github.com/openshift/cluster-node-tuning-operator/pkg/tuned/cmd/render"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/openshift/cluster-node-tuning-operator/version"
)

const (
	webhookPort     = 4343
	webhookCertDir  = "/apiserver.local.config/certificates"
	webhookCertName = "apiserver.crt"
	webhookKeyName  = "apiserver.key"
)

var (
	scheme = apiruntime.NewScheme()
)

func init() {
	ctrl.SetLogger(zap.New())

	if !config.InHyperShift() {
		utilruntime.Must(mcov1.AddToScheme(scheme))
	}
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(tunedv1.AddToScheme(scheme))
	utilruntime.Must(apiconfigv1.Install(scheme))
	utilruntime.Must(performancev1alpha1.AddToScheme(scheme))
	utilruntime.Must(performancev1.AddToScheme(scheme))
	utilruntime.Must(performancev2.AddToScheme(scheme))
}

func printVersion() {
	klog.Infof("Go Version: %s", runtime.Version())
	klog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	klog.Infof("%s Version: %s", tunedv1.TunedClusterOperatorResourceName, version.Version)
}

var rootCmd = &cobra.Command{
	Use:   version.OperatorFilename,
	Short: "NTO manages the containerized TuneD instances",
	Run: func(cmd *cobra.Command, args []string) {
		operatorRun()
	},
}

var (
	enableLeaderElection bool
	showVersionAndExit   bool
)

func prepareCommands() {
	rootCmd.Flags().BoolVar(&enableLeaderElection, "enable-leader-election", true,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	rootCmd.Flags().BoolVar(&showVersionAndExit, "version", false,
		"Show program version and exit.")

	// Include the klog command line arguments
	klog.InitFlags(nil)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	if !config.InHyperShift() {
		rootCmd.AddCommand(render.NewRenderCommand())
		rootCmd.AddCommand(tunedrender.NewRenderBootCmdMCCommand())
	}
	rootCmd.AddCommand(operand.NewTunedCommand())
}

func operatorRun() {
	printVersion()

	if showVersionAndExit {
		return
	}

	// We have two namespaces that we need to watch:
	// 1. NTO namespace: for NTO resources.  Note this is not necessarily where the operator itself
	//    runs, for example operator managing HyperShift hosted clusters.
	// 2. None namespace: for cluster-wide resources
	ntoNamespace := config.WatchNamespace()
	namespaces := []string{
		ntoNamespace,
		metav1.NamespaceNone,
	}

	restConfig := ctrl.GetConfigOrDie()
	le := util.GetLeaderElectionConfig(restConfig, enableLeaderElection)
	mgr, err := ctrl.NewManager(rest.AddUserAgent(restConfig, version.OperatorFilename), ctrl.Options{
		Cache:                         cache.Options{Namespaces: namespaces},
		Scheme:                        scheme,
		LeaderElection:                enableLeaderElection,
		LeaderElectionID:              config.OperatorLockName,
		LeaderElectionNamespace:       ntoNamespace,
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &le.LeaseDuration.Duration,
		RetryPeriod:                   &le.RetryPeriod.Duration,
		RenewDeadline:                 &le.RenewDeadline.Duration,
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:     webhookPort,
			CertDir:  webhookCertDir,
			CertName: webhookCertName,
			KeyName:  webhookKeyName,
			TLSOpts:  []func(config *tls.Config){func(c *tls.Config) { c.NextProtos = []string{"http/1.1"} }}, // CVE-2023-44487
		}),
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

	if !config.InHyperShift() {
		fg, err := setupFeatureGates(context.TODO(), restConfig, ntoNamespace)
		if err != nil {
			klog.Exitf("failed to setup feature gates: %v", err)
		}
		if err = (&paocontroller.PerformanceProfileReconciler{
			Client:            mgr.GetClient(),
			ManagementClient:  mgr.GetClient(),
			Recorder:          mgr.GetEventRecorderFor("performance-profile-controller"),
			FeatureGate:       fg,
			ComponentsHandler: handler.NewHandler(mgr.GetClient(), mgr.GetScheme()),
			StatusWriter:      status.NewWriter(mgr.GetClient()),
		}).SetupWithManager(mgr); err != nil {
			klog.Exitf("unable to create PerformanceProfile controller: %v", err)
		}

		if err = (&performancev1.PerformanceProfile{}).SetupWebhookWithManager(mgr); err != nil {
			klog.Exitf("unable to create PerformanceProfile v1 webhook: %v", err)
		}

		if err = (&performancev2.PerformanceProfile{}).SetupWebhookWithManager(mgr); err != nil {
			klog.Exitf("unable to create PerformanceProfile v2 webhook: %v", err)
		}
	} else {
		// HyperShift configuration
		restConfig, err := ntoclient.GetInClusterConfig()
		if err != nil {
			klog.Exitf("unable to create get InClusterConfiguration while creating PerformanceProfile controller: %v", err)
		}

		fOps := func(opts *cluster.Options) {
			operatorNamespace := config.OperatorNamespace()
			opts.Cache.Namespaces = []string{operatorNamespace}
			opts.Scheme = mgr.GetScheme()
			opts.MapperProvider = func(c *rest.Config, httpClient *http.Client) (meta.RESTMapper, error) {
				return mgr.GetRESTMapper(), nil
			}
			opts.NewClient = func(config *rest.Config, options client.Options) (client.Client, error) {
				c, err := client.New(config, options)
				if err != nil {
					return nil, err
				}
				return hypershift.NewControlPlaneClient(c, operatorNamespace), nil
			}
		}
		managementCluster, err := cluster.New(restConfig, fOps)
		if err != nil {
			klog.Exitf("unable to create ManagementCluster while creating PerformanceProfile controller: %v", err)
		}

		if err := mgr.Add(managementCluster); err != nil {
			klog.Exitf("unable to add ManagementCluster to manger while creating PerformanceProfile controller: %v", err)
		}
		if err = (&paocontroller.PerformanceProfileReconciler{
			// dataPlaneClient
			Client:            mgr.GetClient(),
			ManagementClient:  managementCluster.GetClient(),
			Recorder:          managementCluster.GetEventRecorderFor("performance-profile-controller"),
			ComponentsHandler: hcpcomponents.NewHandler(managementCluster.GetClient(), mgr.GetClient(), mgr.GetScheme()),
			StatusWriter:      hcpstatus.NewWriter(managementCluster.GetClient(), mgr.GetClient()),
		}).SetupWithManagerForHypershift(mgr, managementCluster); err != nil {
			klog.Exitf("unable to create PerformanceProfile controller: %v", err)
		}
	}
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		klog.Exitf("manager exited with non-zero code: %v", err)
	}
}

func setupFeatureGates(ctx context.Context, config *rest.Config, operatorNamespace string) (featuregates.FeatureGate, error) {
	missingVersion := "0.0.1-snapshot"
	desiredVersion := missingVersion
	if v, ok := os.LookupEnv(version.ReleaseVersionEnvVarName); ok {
		desiredVersion = v
	}
	configClient, err := configclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	k8sClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	controllerRef, err := events.GetControllerReferenceForCurrentPod(ctx, k8sClient, operatorNamespace, nil)
	if err != nil {
		return nil, err
	}
	eventRecorder := events.NewKubeRecorder(k8sClient.CoreV1().Events(metav1.NamespaceNone), "performance-profile-controller", controllerRef)
	informersFactory := configinformers.NewSharedInformerFactory(configClient, 10*time.Minute)
	// By default, this will exit(0) the process if the featuregates ever change to a different set of values.
	featureGateAccessor := featuregates.NewFeatureGateAccess(
		desiredVersion, missingVersion,
		informersFactory.Config().V1().ClusterVersions(), informersFactory.Config().V1().FeatureGates(),
		eventRecorder,
	)
	go featureGateAccessor.Run(ctx)
	go informersFactory.Start(ctx.Done())

	select {
	case <-featureGateAccessor.InitialFeatureGatesObserved():
		featureGates, _ := featureGateAccessor.CurrentFeatureGates()
		klog.Infof("FeatureGates initialized: knownFeatureGates=%v", featureGates.KnownFeatures())
	case <-time.After(1 * time.Minute):
		klog.Errorf("timed out waiting for FeatureGate detection")
		return nil, fmt.Errorf("timed out waiting for FeatureGate detection")
	}
	return featureGateAccessor.CurrentFeatureGates()
}

func main() {
	prepareCommands()
	if err := rootCmd.Execute(); err != nil {
		klog.Fatal(err)
	}
}
