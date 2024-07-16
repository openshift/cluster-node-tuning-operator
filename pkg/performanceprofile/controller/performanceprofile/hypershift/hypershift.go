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
	"sigs.k8s.io/yaml"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	hypershiftconsts "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/consts"
)

const (
	HostedClustersNamespaceName = "clusters"
)

type ControlPlaneClientImpl struct {
	// A client with access to the management cluster
	client.Client

	// hostedControlPlaneNamespaceName is the namespace name on the management cluster
	// on which the control-plane objects reside
	hostedControlPlaneNamespaceName string

	yamlSerializer *serializer.Serializer
}

func NewControlPlaneClient(c client.Client, ns string) *ControlPlaneClientImpl {
	return &ControlPlaneClientImpl{
		Client:                          c,
		hostedControlPlaneNamespaceName: ns,
		yamlSerializer: serializer.NewSerializerWithOptions(
			serializer.DefaultMetaFactory, c.Scheme(), c.Scheme(),
			serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true}),
	}
}

func (ci *ControlPlaneClientImpl) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	dataKey := GetObjectConfigMapDataKey(obj)
	if dataKey != "" {
		return ci.getFromConfigMap(ctx, key, obj, dataKey, opts...)
	}
	return ci.Client.Get(ctx, key, obj, opts...)
}

func (ci *ControlPlaneClientImpl) List(ctx context.Context, objList client.ObjectList, opts ...client.ListOption) error {
	dataKey := GetObjectConfigMapDataKey(objList)
	if dataKey != "" {
		return ci.listFromConfigMaps(ctx, objList, dataKey, opts...)
	}
	return ci.Client.List(ctx, objList, opts...)
}

func (ci *ControlPlaneClientImpl) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	dataKey := GetObjectConfigMapDataKey(obj)
	if dataKey != "" {
		return ci.createInConfigMap(ctx, obj, dataKey, opts...)
	}
	return ci.Client.Create(ctx, obj, opts...)
}

func (ci *ControlPlaneClientImpl) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	dataKey := GetObjectConfigMapDataKey(obj)
	if dataKey != "" {
		return ci.updateConfigMap(ctx, obj, dataKey, opts...)
	}
	return ci.Client.Update(ctx, obj, opts...)
}

func (ci *ControlPlaneClientImpl) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	dataKey := GetObjectConfigMapDataKey(obj)
	if dataKey != "" {
		return ci.deleteFromConfigMap(ctx, obj, dataKey, opts...)
	}
	return ci.Client.Delete(ctx, obj, opts...)
}

func IsWriteableToControlPlane(obj runtime.Object) bool {
	return GetObjectConfigMapDataKey(obj) == ""
}

func IsReadableFromControlPlane(obj runtime.Object) bool {
	switch obj.(type) {
	case *tunedv1.Tuned, *tunedv1.TunedList:
		return true
	}
	return GetObjectConfigMapDataKey(obj) == ""
}

func GetObjectConfigMapDataKey(obj runtime.Object) string {
	switch obj.(type) {
	case *performancev2.PerformanceProfile, *performancev2.PerformanceProfileList, *tunedv1.Tuned, *tunedv1.TunedList:
		return hypershiftconsts.TuningKey
	case *machineconfigv1.KubeletConfig, *machineconfigv1.KubeletConfigList,
		*machineconfigv1.MachineConfig, *machineconfigv1.MachineConfigList,
		*machineconfigv1.ContainerRuntimeConfig, *machineconfigv1.ContainerRuntimeConfigList:
		return hypershiftconsts.ConfigKey
	default:
		return ""
	}
}

// We do not have a direct way to detect the ConfigMaps with the wanted objects,
// so the function does the following:
// 1. List() all ConfigMap within the HCP namespace
// 2. Decodes all internal objects that are found with the provided dataKey
// 3. Checks the listObj type and inserts the decoded objects which are match the list type
func (ci *ControlPlaneClientImpl) listFromConfigMaps(ctx context.Context, listObj client.ObjectList, dataKey string, opts ...client.ListOption) error {
	cmList := &corev1.ConfigMapList{}
	if err := ci.Client.List(ctx, cmList, &client.ListOptions{
		Namespace: ci.hostedControlPlaneNamespaceName}); err != nil {
		return err
	}
	var decodedObjects []runtime.Object
	for _, cm := range cmList.Items {
		b, ok := cm.Data[dataKey]
		if !ok {
			// if it does not found under the key,
			// this object it not of the wanted type
			continue
		}
		obj, _, err := ci.yamlSerializer.Decode([]byte(b), nil, nil)
		if err != nil {
			return err
		}
		if pp, ok := obj.(*performancev2.PerformanceProfile); ok {
			if err := ci.getStatusForPerformanceProfile(ctx, cm.Name, pp); err != nil {
				return fmt.Errorf("failed to get status for PerformanceProfile ConfigMap %q; %w", fmt.Sprintf("%s/%s", cm.Namespace, cm.Name), err)
			}
		}
		decodedObjects = append(decodedObjects, obj)
	}
	insertDecodedObjects(listObj, decodedObjects)
	return nil
}

func (ci *ControlPlaneClientImpl) getFromConfigMap(ctx context.Context, key client.ObjectKey, obj client.Object, dataKey string, opts ...client.GetOption) error {
	cmList := &corev1.ConfigMapList{}
	err := ci.Client.List(ctx, cmList, &client.ListOptions{
		Namespace: ci.hostedControlPlaneNamespaceName,
	})
	if err != nil {
		return err
	}
	for _, cm := range cmList.Items {
		err = validateAndExtractObjectFromConfigMap(&cm, ci.Scheme(), obj, dataKey)
		if err != nil {
			return err
		}
		if obj.GetName() == key.Name {
			if pp, ok := obj.(*performancev2.PerformanceProfile); ok {
				if err = ci.getStatusForPerformanceProfile(ctx, cm.Name, pp); err != nil {
					return fmt.Errorf("failed to get status for PerformanceProfile ConfigMap %q; %w", key.String(), err)
				}
			}
			return nil
		}
	}
	// we don't have the actual GroupResource, because it's an internal rendered into the ConfigMap data, so leave it empty.
	return fmt.Errorf("the encapsulated object %s was not found in ConfigMap; %w", key.String(),
		apierrors.NewNotFound(schema.GroupResource{}, key.Name))
}

func (ci *ControlPlaneClientImpl) getStatusForPerformanceProfile(ctx context.Context, instanceName string, profile *performancev2.PerformanceProfile) error {
	statusConfigMap := &corev1.ConfigMap{}
	key := client.ObjectKey{
		Namespace: ci.hostedControlPlaneNamespaceName,
		Name:      GetStatusConfigMapName(instanceName),
	}

	if err := ci.Get(ctx, key, statusConfigMap); err != nil {
		return err
	}
	b := statusConfigMap.Data[hypershiftconsts.PerformanceProfileStatusKey]
	ppStatus := &performancev2.PerformanceProfileStatus{}
	if err := yaml.Unmarshal([]byte(b), ppStatus); err != nil {
		return err
	}
	profile.Status = *ppStatus
	return nil
}

// validateAndExtractObjectFromConfigMap validates if there's object data in a configmap
// and tries to extract the object.
func validateAndExtractObjectFromConfigMap(cm *corev1.ConfigMap, scheme *runtime.Scheme, obj client.Object, dataKey string) error {
	manifest, ok := cm.Data[dataKey]
	if !ok {
		// the given configmap does not contain data under the provided key
		// we return here to save the unnecessary decoding
		return nil
	}
	if _, err := DecodeManifest([]byte(manifest), scheme, obj); err != nil {
		return fmt.Errorf("error decoding config: %w", err)
	}
	return nil
}

func insertDecodedObjects(objectList client.ObjectList, objs []runtime.Object) {
	for _, obj := range objs {
		switch list := objectList.(type) {
		case *performancev2.PerformanceProfileList:
			if pp, ok := obj.(*performancev2.PerformanceProfile); ok {
				list.Items = append(list.Items, *pp)
			}
		case *machineconfigv1.KubeletConfigList:
			if kc, ok := obj.(*machineconfigv1.KubeletConfig); ok {
				list.Items = append(list.Items, *kc)
			}
		case *machineconfigv1.MachineConfigList:
			if mc, ok := obj.(*machineconfigv1.MachineConfig); ok {
				list.Items = append(list.Items, *mc)
			}
		case *tunedv1.TunedList:
			if t, ok := obj.(*tunedv1.Tuned); ok {
				list.Items = append(list.Items, *t)
			}
		}
	}
}

func (ci *ControlPlaneClientImpl) createInConfigMap(ctx context.Context, obj client.Object, dataKey string, opts ...client.CreateOption) error {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: obj.GetName(), Namespace: HostedClustersNamespaceName}}
	b, err := EncodeManifest(obj, scheme.Scheme)
	if err != nil {
		return err
	}
	cm.Data = map[string]string{dataKey: string(b)}
	return ci.Client.Create(ctx, cm, opts...)
}

func (ci *ControlPlaneClientImpl) updateConfigMap(ctx context.Context, obj client.Object, dataKey string, opts ...client.UpdateOption) error {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: obj.GetName(), Namespace: HostedClustersNamespaceName}}
	b, err := EncodeManifest(obj, scheme.Scheme)
	if err != nil {
		return err
	}
	cm.Data = map[string]string{dataKey: string(b)}
	return ci.Client.Update(ctx, cm, opts...)
}

func (ci *ControlPlaneClientImpl) deleteFromConfigMap(ctx context.Context, obj client.Object, dataKey string, opts ...client.DeleteOption) error {
	cmList := &corev1.ConfigMapList{}
	err := ci.Client.List(ctx, cmList, &client.ListOptions{
		Namespace: HostedClustersNamespaceName,
	})
	if err != nil {
		return err
	}

	for _, cm := range cmList.Items {
		manifest, ok := cm.Data[dataKey]
		if !ok {
			continue
		}
		decodedObj, _, err := ci.yamlSerializer.Decode([]byte(manifest), nil, nil)
		if err != nil {
			return err
		}
		if obj.GetName() == decodedObj.(client.Object).GetName() {
			return ci.Client.Delete(ctx, &cm)
		}
	}
	// we don't have the actual GroupResource,
	//because it's an internal rendered into the ConfigMap data, so leave it empty.
	return fmt.Errorf("cannot delete the encapsulated object %s, because it was not found in ConfigMap in the %s namespace; %w", obj.GetName(), HostedClustersNamespaceName,
		apierrors.NewNotFound(schema.GroupResource{}, obj.GetName()))
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

// DecodeManifest tries to decode b into obj.
// if fails to decode b, it returns an error
// if decoded data returned from b, has different GVK than what stored in obj
// it returns false
func DecodeManifest(b []byte, scheme *runtime.Scheme, obj runtime.Object) (bool, error) {
	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		return false, fmt.Errorf("cannot determine GVK of resource of type %T: %w", obj, err)
	}
	newObj, _, err := yamlSerializer.Decode(b, nil, obj)
	if err != nil {
		return false, err
	}
	for _, gvk := range gvks {
		if newObj.GetObjectKind().GroupVersionKind() == gvk {
			return true, err
		}
	}
	return false, err
}

func GetStatusConfigMapName(instanceName string) string {
	return fmt.Sprintf("%s-%s", hypershiftconsts.PerformanceProfileStatusKey, instanceName)
}
