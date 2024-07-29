package hypershift

import (
	"fmt"
	"os"

	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift"
)

var isHypershiftCluster bool

func init() {
	if v, ok := os.LookupEnv("CLUSTER_TYPE"); ok && v == "hypershift" {
		isHypershiftCluster = true
	}
}

const (
	ManagementClusterKubeConfigEnv = "HYPERSHIFT_MANAGEMENT_CLUSTER_KUBECONFIG"
	HostedControlPlaneNamespaceEnv = "HYPERSHIFT_HOSTED_CONTROL_PLANE_NAMESPACE"
	HostedClusterKubeConfigEnv     = "KUBECONFIG"
	HostedClusterNameEnv           = "CLUSTER_NAME"
)

func BuildControlPlaneClient() (client.Client, error) {
	kcPath, ok := os.LookupEnv(ManagementClusterKubeConfigEnv)
	if !ok {
		return nil, fmt.Errorf("failed to build management-cluster client for hypershift, environment variable %q is not defined", ManagementClusterKubeConfigEnv)
	}
	c, err := buildClient(kcPath)
	if err != nil {
		return nil, err
	}
	ns, err := GetManagementClusterNamespace()
	if err != nil {
		return nil, fmt.Errorf("failed to build management-cluster client for hypershift; err %v", err)
	}
	return hypershift.NewControlPlaneClient(c, ns), nil
}

func BuildDataPlaneClient() (client.Client, error) {
	kcPath, ok := os.LookupEnv(HostedClusterKubeConfigEnv)
	if !ok {
		return nil, fmt.Errorf("failed to build hosted-cluster client for hypershift, environment variable %q is not defined", HostedClusterKubeConfigEnv)
	}
	return buildClient(kcPath)
}

func GetHostedClusterName() (string, error) {
	v, ok := os.LookupEnv(HostedClusterNameEnv)
	if !ok {
		return "", fmt.Errorf("failed to retrieve hosted cluster name; %q environment var is not set", HostedClusterNameEnv)
	}
	return v, nil
}

func GetManagementClusterNamespace() (string, error) {
	ns, ok := os.LookupEnv(HostedControlPlaneNamespaceEnv)
	if !ok {
		return "", fmt.Errorf("failed to retrieve management cluster namespace; %q environment var is not set", HostedControlPlaneNamespaceEnv)
	}
	return ns, nil
}

// IsHypershiftCluster should be used only on CI environment
func IsHypershiftCluster() bool {
	return isHypershiftCluster
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
