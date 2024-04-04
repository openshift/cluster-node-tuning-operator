package hypershift

import (
	"bytes"
	"context"
	"fmt"
	"os"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var isHypershiftCluster bool

func init() {
	if v, ok := os.LookupEnv("CLUSTER_TYPE"); ok && v == "hypershift" {
		isHypershiftCluster = true
	}
}

const (
	ManagementClusterKubeConfigEnv = "HYPERSHIFT_MANAGEMENT_CLUSTER_KUBECONFIG"
	ManagementClusterNamespaceEnv  = "HYPERSHIFT_MANAGEMENT_CLUSTER_NAMESPACE"
	HostedClusterKubeConfigEnv     = "HYPERSHIFT_HOSTED_CLUSTER_KUBECONFIG"
	HostedClusterNameEnv           = "CLUSTER_NAME"
	NodePoolNamespace              = "clusters"
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

func (ci *ControlPlaneClientImpl) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if ObjIsEncapsulatedInConfigMap(obj) {
		return ci.createInConfigMap(ctx, obj, opts...)
	}
	return ci.Client.Create(ctx, obj, opts...)
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
	ns, err := GetManagementClusterNamespace()
	if err != nil {
		return nil, fmt.Errorf("failed to build management-cluster client for hypershift; err %v", err)
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
	return decodeManifest([]byte(objAsYAML), scheme.Scheme, obj)
}

func (ci *ControlPlaneClientImpl) createInConfigMap(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	ns := ci.managementClusterNamespaceName
	// the performance profile should be created in the node pool namespace, and
	// the hypershift operator copies it to the hostedcluster namespace
	if _, ok := obj.(*performancev2.PerformanceProfile); ok {
		ns = NodePoolNamespace
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: obj.GetName(), Namespace: ns}}
	b, err := encodeManifest(obj, scheme.Scheme)
	if err != nil {
		return err
	}
	cm.Data = map[string]string{TuningKey: string(b)}
	return ci.Client.Create(ctx, cm, opts...)
}

func GetHostedClusterName() (string, error) {
	v, ok := os.LookupEnv(HostedClusterNameEnv)
	if !ok {
		return "", fmt.Errorf("failed to retrieve hosted cluster name; %q environment var is not set", HostedClusterNameEnv)
	}
	return v, nil
}

func GetManagementClusterNamespace() (string, error) {
	ns, ok := os.LookupEnv(ManagementClusterNamespaceEnv)
	if !ok {
		return "", fmt.Errorf("failed to retrieve management cluster namespace; %q environment var is not set", ManagementClusterNamespaceEnv)
	}
	return ns, nil
}

func IsHypershiftCluster() bool {
	return isHypershiftCluster
}

func encodeManifest(obj runtime.Object, scheme *runtime.Scheme) ([]byte, error) {
	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
	buff := bytes.Buffer{}
	err := yamlSerializer.Encode(obj, &buff)
	return buff.Bytes(), err
}

func decodeManifest(b []byte, scheme *runtime.Scheme, obj runtime.Object) error {
	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
	// Get the GroupVersionKind of the object
	gvk := obj.GetObjectKind().GroupVersionKind()
	_, _, err := yamlSerializer.Decode(b, &gvk, obj)
	return err
}
