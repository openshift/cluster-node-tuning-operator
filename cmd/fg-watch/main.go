package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/openshift/cluster-node-tuning-operator/version"
)

func init() {
	ctrl.SetLogger(zap.New())
}

var (
	ntoVersion   string
	ntoNamespace string
)

func printInfo() {
	klog.Infof("Version: %s", ntoVersion)
	klog.Infof("Namespace: %s", ntoNamespace)
}

func setupFeatureGates(ctx context.Context, config *rest.Config, operatorNamespace string) (featuregates.FeatureGate, error) {
	// TODO is this version correct?
	desiredVersion := ntoVersion
	missingVersion := "0.0.1-snapshot"

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
	klog.InitFlags(nil)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.StringVar(&ntoVersion, "version", version.Version, "set version")
	pflag.StringVar(&ntoNamespace, "namespace", "default", "set namespace")
	pflag.Parse()

	printInfo()

	restConfig := ctrl.GetConfigOrDie()

	fg, err := setupFeatureGates(context.TODO(), restConfig, ntoNamespace)
	if err != nil {
		klog.Exitf("failed to setup feature gates: %v", err)
	}

	klog.Infof("featuregates=%+v", fg)
}
