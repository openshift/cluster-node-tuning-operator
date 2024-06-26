package hypershift

import (
	"bytes"
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	PerformanceProfileNameLabel = "hypershift.openshift.io/performanceProfileName"
)

type ControlPlaneClientImpl struct {
	// A client with access to the management cluster
	client.Client

	// managementClusterNamespaceName is the namespace name on the management cluster
	// on which the control-plane objects reside
	managementClusterNamespaceName string
}

func (ci *ControlPlaneClientImpl) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if IsEncapsulatedInConfigMap(obj) {
		return ci.getFromConfigMap(ctx, key, obj, opts...)
	}
	return ci.Client.Get(ctx, key, obj, opts...)
}

func (ci *ControlPlaneClientImpl) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if IsEncapsulatedInConfigMap(obj) {
		return ci.createInConfigMap(ctx, obj, opts...)
	}
	return ci.Client.Create(ctx, obj, opts...)
}

func IsEncapsulatedInConfigMap(obj runtime.Object) bool {
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
	cmList := &corev1.ConfigMapList{}
	err := ci.Client.List(ctx, cmList, &client.ListOptions{
		Namespace: ci.managementClusterNamespaceName,
	})
	if err != nil {
		return err
	}
	for _, cm := range cmList.Items {
		err = validateAndExtractObjectFromConfigMap(&cm, ci.Scheme(), obj)
		if err != nil {
			return err
		}
		if obj.GetName() == key.Name {
			return nil
		}
	}
	// we don't have the actual GroupResource, because it's an internal rendered into the ConfigMap data, so leave it empty.
	return fmt.Errorf("the encapsulated object %s was not found in ConfigMap; %w", key.String(),
		apierrors.NewNotFound(schema.GroupResource{}, key.Name))
}

// validateAndExtractObjectFromConfigMap validates the object if it supposes to be encapsulated in a configmap.
// then it tries to extract the object.
// even if extraction failed, the returned validation value is true.
func validateAndExtractObjectFromConfigMap(cm *corev1.ConfigMap, scheme *runtime.Scheme, obj client.Object) error {
	var manifest string
	switch obj.(type) {
	case *performancev2.PerformanceProfile, *tunedv1.Tuned:
		manifest = cm.Data[TuningKey]
	case *machineconfigv1.KubeletConfig, *machineconfigv1.MachineConfig:
		manifest = cm.Data[ConfigKey]
	default:
		return fmt.Errorf("unsupported config type: %T", obj)
	}
	if err := DecodeManifest([]byte(manifest), scheme, obj); err != nil {
		return fmt.Errorf("error decoding config: %w", err)
	}
	return nil
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
