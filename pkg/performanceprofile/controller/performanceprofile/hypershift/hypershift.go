package hypershift

import (
	"bytes"
	"context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
)

// a set of keys which used to classify the encapsulated objects in the ConfigMap
const (
	TuningKey                   = "tuning"
	ConfigKey                   = "config"
	HostedClustersNamespaceName = "clusters"
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

func ObjIsEncapsulatedInConfigMap(obj runtime.Object) bool {
	switch obj.(type) {
	case *performancev2.PerformanceProfile, *performancev2.PerformanceProfileList,
		*machineconfigv1.KubeletConfig, *machineconfigv1.KubeletConfigList,
		*machineconfigv1.MachineConfig, *machineconfigv1.MachineConfigList,
		*tunedv1.Tuned, *tunedv1.TunedList:
		return true
	default:
		return false
	}
}

func NewControlPlaneClient(c client.Client, ns string) *ControlPlaneClientImpl {
	return &ControlPlaneClientImpl{
		Client:                         c,
		managementClusterNamespaceName: ns,
	}
}

func (ci *ControlPlaneClientImpl) getFromConfigMap(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	cm := &corev1.ConfigMap{}
	if key.Namespace == metav1.NamespaceNone {
		key.Namespace = ci.managementClusterNamespaceName
	}
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
	return DecodeManifest([]byte(objAsYAML), scheme.Scheme, obj)
}

func (ci *ControlPlaneClientImpl) createInConfigMap(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: obj.GetName(), Namespace: HostedClustersNamespaceName}}
	b, err := EncodeManifest(obj, scheme.Scheme)
	if err != nil {
		return err
	}
	cm.Data = map[string]string{TuningKey: string(b)}
	return ci.Client.Create(ctx, cm, opts...)
}

func EncodeManifest(obj runtime.Object, scheme *runtime.Scheme) ([]byte, error) {
	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
	buff := bytes.Buffer{}
	err := yamlSerializer.Encode(obj, &buff)
	return buff.Bytes(), err
}

func DecodeManifest(b []byte, scheme *runtime.Scheme, obj runtime.Object) error {
	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
	// Get the GroupVersionKind of the object
	gvk := obj.GetObjectKind().GroupVersionKind()
	_, _, err := yamlSerializer.Decode(b, &gvk, obj)
	return err
}
