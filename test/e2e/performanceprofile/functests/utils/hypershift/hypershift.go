package hypershift

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	ManagementClusterKubeConfigEnv = "HYPERSHIFT_MANAGEMENT_CLUSTER_KUBECONFIG"
	ManagementClusterNamespaceEnv  = "HYPERSHIFT_MANAGEMENT_CLUSTER_NAMESPACE"
	HostedClusterKubeConfigEnv     = "HYPERSHIFT_HOSTED_CLUSTER_KUBECONFIG"
)

// a set of keys which used to classify the encapsulated objects in the ConfigMap
const (
	TuningKey = "tuning"
	ConfigKey = "config"
)

type ControlPlaneClientImpl struct {
	// A client with access to the management cluster
	client.Client

	// managementClusterNamespaceName is the namespace name on the management cluster
	// on which the control-plane objects reside
	managementClusterNamespaceName string
}

func (ci *ControlPlaneClientImpl) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if ObjIsEncapsulatedInConfigMap(obj) {
		return ci.getFromConfigMap(ctx, key, obj, opts...)
	}
	return ci.Client.Get(ctx, key, obj, opts...)
}

func buildClient(kubeConfigPath string) (client.Client, error) {
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	if err != nil {
		return nil, err
	}
	c, err := client.New(restConfig, client.Options{})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func ObjIsEncapsulatedInConfigMap(obj runtime.Object) bool {
	switch obj.(type) {
	case *performancev2.PerformanceProfile, *performancev2.PerformanceProfileList,
		*machineconfigv1.KubeletConfig, *machineconfigv1.KubeletConfigList,
		*machineconfigv1.MachineConfig, *machineconfigv1.MachineConfigList,
		*tunedv1.Profile, *tunedv1.ProfileList:
		return true
	default:
		return false
	}
}

func BuildControlPlaneClient() (client.Client, error) {
	kcPath, ok := os.LookupEnv(ManagementClusterKubeConfigEnv)
	if !ok {
		return nil, fmt.Errorf("failed to build management-cluster client for hypershift, environment variable %q is not defined", ManagementClusterKubeConfigEnv)
	}
	c, err := buildClient(kcPath)
	if err != nil {
		return nil, err
	}
	ns, ok := os.LookupEnv(ManagementClusterNamespaceEnv)
	if !ok {
		return nil, fmt.Errorf("failed to build management-cluster client for hypershift, environment variable %q is not defined", ManagementClusterNamespaceEnv)
	}
	return &ControlPlaneClientImpl{
		Client:                         c,
		managementClusterNamespaceName: ns,
	}, nil
}

func BuildDataPlaneClient() (client.Client, error) {
	kcPath, ok := os.LookupEnv(HostedClusterKubeConfigEnv)
	if !ok {
		return nil, fmt.Errorf("failed to build hosted-cluster client for hypershift, environment variable %q is not defined", HostedClusterKubeConfigEnv)
	}
	return buildClient(kcPath)
}

func (ci *ControlPlaneClientImpl) getFromConfigMap(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	cm := &corev1.ConfigMap{}
	key.Namespace = ci.managementClusterNamespaceName
	err := ci.Client.Get(ctx, key, cm, opts...)
	if err != nil {
		return err
	}
	var objAsYAML string
	// can't have both
	if s, ok := cm.Data[TuningKey]; ok {
		objAsYAML = s
	}
	if s, ok := cm.Data[ConfigKey]; ok {
		objAsYAML = s
	}
	return yaml.Unmarshal([]byte(objAsYAML), obj)
}
