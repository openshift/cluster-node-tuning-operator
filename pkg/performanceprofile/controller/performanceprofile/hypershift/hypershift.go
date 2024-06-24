package hypershift

import (
	"bytes"
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

	// ConfigMapEncapsulatedKindKey specifies the kind of the object encapsulated in a ConfigMap
	ConfigMapEncapsulatedKindKey = "hypershift.openshift.io/encapsulated-kind"

	// hypershiftControllerGeneratedPerformanceProfile is a label that is set by the hypershift operator
	// when it mirrors the performanceProfile into the hosted control plane namespace
	hypershiftControllerGeneratedPerformanceProfile = "hypershift.openshift.io/performanceprofile-config"
)

type ControlPlaneClientImpl struct {
	// A client with access to the management cluster
	client.Client

	// managementClusterNamespaceName is the namespace name on the management cluster
	// on which the control-plane objects reside
	managementClusterNamespaceName string
}

func (ci *ControlPlaneClientImpl) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	encapsulatedObj, err := ci.getFromConfigMap(ctx, key, obj, opts...)
	if encapsulatedObj {
		return err
	}
	return ci.Client.Get(ctx, key, obj, opts...)
}

func (ci *ControlPlaneClientImpl) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if EncapsulatedInConfigMap(obj) {
		return ci.createInConfigMap(ctx, obj, opts...)
	}
	return ci.Client.Create(ctx, obj, opts...)
}

func EncapsulatedInConfigMap(obj runtime.Object) bool {
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

func (ci *ControlPlaneClientImpl) getFromConfigMap(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) (bool, error) {
	var labelSelector labels.Selector
	switch obj.(type) {
	case *performancev2.PerformanceProfile:
		labelSelector = labels.SelectorFromSet(labels.Set{hypershiftControllerGeneratedPerformanceProfile: "true"})
	case *machineconfigv1.KubeletConfig:
		labelSelector = labels.SelectorFromSet(labels.Set{ConfigMapEncapsulatedKindKey: "kubeletconfig"})
	case *machineconfigv1.MachineConfig:
		labelSelector = labels.SelectorFromSet(labels.Set{ConfigMapEncapsulatedKindKey: "machineconfig"})
	case *tunedv1.Tuned:
		labelSelector = labels.SelectorFromSet(labels.Set{ConfigMapEncapsulatedKindKey: "tuned"})
	default:
		return false, nil
	}
	cmList := &corev1.ConfigMapList{}
	listOpts := &client.ListOptions{}
	if key.Namespace == metav1.NamespaceNone {
		listOpts.Namespace = ci.managementClusterNamespaceName
		listOpts.LabelSelector = labelSelector
	}
	err := ci.List(ctx, cmList, listOpts)
	if err != nil {
		return true, err
	}
	for _, cm := range cmList.Items {
		var objAsYAML string
		var tuningKeyFound, configKeyFound bool
		if s, ok := cm.Data[TuningKey]; ok {
			objAsYAML = s
			tuningKeyFound = true
		}
		if s, ok2 := cm.Data[ConfigKey]; ok2 {
			objAsYAML = s
			configKeyFound = true
		}
		cmKey := client.ObjectKeyFromObject(&cm)
		// can't have both
		if tuningKeyFound && configKeyFound {
			return true, fmt.Errorf("ConfigMap %s has both %s and %s keys", cmKey.String(), TuningKey, ConfigKey)
		}
		if !tuningKeyFound && !configKeyFound {
			return true, fmt.Errorf("could not get %s from ConfigMap %s, keys %s and %s are missing",
				obj.GetObjectKind().GroupVersionKind().Kind, cmKey.String(), TuningKey, ConfigKey)
		}
		err = DecodeManifest([]byte(objAsYAML), scheme.Scheme, obj)
		if err != nil {
			return true, err
		}
		if key.Name == obj.GetName() {
			return true, nil
		}
	}
	return false, nil
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
