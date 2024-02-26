package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	apiconfigv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	performancev1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1"
	performancev1alpha1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1alpha1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	paocontroller "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/machineconfig"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/metrics"
	"github.com/openshift/cluster-node-tuning-operator/pkg/operator"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/cmd/render"
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

	performanceOperatorDeploymentName = "performance-operator"
)

var (
	scheme = apiruntime.NewScheme()
)

func init() {
	ctrl.SetLogger(zap.New())

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(tunedv1.AddToScheme(scheme))
	utilruntime.Must(mcov1.AddToScheme(scheme))
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

	if !config.InHyperShift() {
		if err := removePerformanceOLMOperator(restConfig); err != nil {
			klog.Fatalf("unable to remove Performance addons OLM operator: %v", err)
		}

		if err := migratePinnedSingleNodeInfraStatus(restConfig, scheme); err != nil {
			klog.Fatalf("unable to migrate pinned single node infra status: %v", err)
		}
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
			Client:      mgr.GetClient(),
			Scheme:      mgr.GetScheme(),
			Recorder:    mgr.GetEventRecorderFor("performance-profile-controller"),
			FeatureGate: fg,
		}).SetupWithManager(mgr); err != nil {
			klog.Exitf("unable to create PerformanceProfile controller: %v", err)
		}

		if err = (&performancev1.PerformanceProfile{}).SetupWebhookWithManager(mgr); err != nil {
			klog.Exitf("unable to create PerformanceProfile v1 webhook: %v", err)
		}

		if err = (&performancev2.PerformanceProfile{}).SetupWebhookWithManager(mgr); err != nil {
			klog.Exitf("unable to create PerformanceProfile v2 webhook: %v", err)
		}
	}
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		klog.Exitf("manager exited with non-zero code: %v", err)
	}
}

// Uninstall PAO OLM operator since PAO is shipped
// as a core operator from 4.11.
// This is relevant for any upgrade path of an OpenShift cluster
// below 4.11 containing PAO to this current version.
func removePerformanceOLMOperator(cfg *rest.Config) error {
	k8sclient, err := client.New(cfg, client.Options{})
	if err != nil {
		return err
	}

	csvSelector := labels.NewSelector()
	req, err := labels.NewRequirement(olmv1alpha1.CopiedLabelKey, selection.DoesNotExist, []string{})
	if err != nil {
		return err
	}
	csvSelector.Add(*req)

	//REVIEW - should this be an input parameter? or something configurable?
	paginationLimit := uint64(1000)
	options := []client.ListOption{
		client.MatchingLabelsSelector{Selector: csvSelector},
	}

	performanceOperatorCSVs, err := paocontroller.ListPerformanceOperatorCSVs(k8sclient, options, paginationLimit, performanceOperatorDeploymentName)
	if err != nil && !util.IsNoMatchError(err) {
		return err
	}

	subscriptions := &olmv1alpha1.SubscriptionList{}
	if err := k8sclient.List(context.TODO(), subscriptions); err != nil {
		if !errors.IsNotFound(err) && !util.IsNoMatchError(err) {
			return err
		}
	}
	for i := range subscriptions.Items {
		subscription := &subscriptions.Items[i]
		subscriptionExists := true
		for _, csv := range performanceOperatorCSVs {
			if subscription.Namespace == csv.Namespace && subscription.Status.InstalledCSV == csv.Name {
				if subscriptionExists {
					klog.Infof("Removing performance-addon-operator subscription %s", subscription.Name)
					if err := k8sclient.Delete(context.TODO(), subscription); err != nil {
						return err
					}
					subscriptionExists = false
				}
				klog.Infof("Removing performance-addon-operator related CSV %s/%s", csv.Namespace, csv.Name)
				if err := k8sclient.Delete(context.TODO(), &csv); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// During upgrade from 4.13 -> 4.14, this logic will update the authoritative flag in the new
// Infrastructures.Status.CPUPartitioning to migrate Single Node clusters to the new method.
// Note:
// This method will also execute during fresh installs on SNO using the legacy method. We are
// generating the bootstrap files during that process, when this method then executes, it will only
// update the API flag as intended, and since the bootstrap configs will already exist, nothing will happen.
//
// TODO: Revisit after 4.14 to remove logic when no longer needed.
func migratePinnedSingleNodeInfraStatus(cfg *rest.Config, scheme *apiruntime.Scheme) error {
	k8sclient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	key := types.NamespacedName{
		Name: "cluster",
	}
	infra := &apiconfigv1.Infrastructure{}
	if err := k8sclient.Get(context.Background(), key, infra); err != nil {
		return err
	}

	// If partitioning is on, we don't need to do anymore checks
	if infra.Status.CPUPartitioning != apiconfigv1.CPUPartitioningNone {
		return nil
	}

	// If we are in single node we need to do another check for upgrades from 4.12
	// this should only be triggered during the first upgrade
	if infra.Status.ControlPlaneTopology == apiconfigv1.SingleReplicaTopologyMode {
		nodes := &corev1.NodeList{}
		if err := k8sclient.List(context.Background(), nodes); err != nil {
			return err
		}
		foundPinningCapacity := false
		for _, node := range nodes.Items {
			// Since we're in single node mode, we break early.
			if _, ok := node.Status.Allocatable[corev1.ResourceName("management.workload.openshift.io/cores")]; ok {
				foundPinningCapacity = true
				break
			}
		}
		if foundPinningCapacity {
			ctx := context.Background()
			pinning := apiconfigv1.CPUPartitioningAllNodes
			infraCopy := infra.DeepCopy()
			infraCopy.Status.CPUPartitioning = pinning
			mc, err := machineconfig.BootstrapWorkloadPinningMC("master", &pinning)
			if err != nil {
				return err
			}
			// If the bootstrap MC already exists or the error is nil,
			// we continue with updating the infra status
			err = k8sclient.Create(ctx, mc)
			if errors.IsAlreadyExists(err) || err == nil {
				return k8sclient.Status().Update(ctx, infraCopy)
			}
		}
	}

	return nil
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
