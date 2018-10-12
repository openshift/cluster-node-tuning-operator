package manifests

import (
	"bytes"
	"io"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	tunedv1alpha1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1alpha1"
)

const (
	TunedNamespace          = "assets/tuned/01-namespace.yaml"
	TunedServiceAccount     = "assets/tuned/02-service-account.yaml"
	TunedClusterRole        = "assets/tuned/03-cluster-role.yaml"
	TunedClusterRoleBinding = "assets/tuned/04-cluster-role-binding.yaml"
	TunedConfigMapProfiles  = "assets/tuned/05-cm-tuned-profiles.yaml"
	TunedConfigMapRecommend = "assets/tuned/06-cm-tuned-recommend.yaml"
	TunedDaemonSet          = "assets/tuned/07-ds-tuned.yaml"
)

func MustAssetReader(asset string) io.Reader {
	return bytes.NewReader(MustAsset(asset))
}

// Factory knows how to create tuned-related cluster resources from manifest
// files.  It provides a point of control to mutate the static resources with
// provided configuration.
type Factory struct {
}

func NewFactory() *Factory {
	return &Factory{}
}

func (f *Factory) TunedNamespace() (*corev1.Namespace, error) {
	ns, err := NewNamespace(MustAssetReader(TunedNamespace))
	if err != nil {
		return nil, err
	}
	return ns, nil
}

func (f *Factory) TunedServiceAccount() (*corev1.ServiceAccount, error) {
	sa, err := NewServiceAccount(MustAssetReader(TunedServiceAccount))
	if err != nil {
		return nil, err
	}
	return sa, nil
}

func (f *Factory) TunedClusterRole() (*rbacv1.ClusterRole, error) {
	cr, err := NewClusterRole(MustAssetReader(TunedClusterRole))
	if err != nil {
		return nil, err
	}
	return cr, nil
}

func (f *Factory) TunedClusterRoleBinding() (*rbacv1.ClusterRoleBinding, error) {
	crb, err := NewClusterRoleBinding(MustAssetReader(TunedClusterRoleBinding))
	if err != nil {
		return nil, err
	}
	return crb, nil
}

func (f *Factory) TunedConfigMapProfiles() (*corev1.ConfigMap, error) {
	cm, err := NewConfigMap(MustAssetReader(TunedConfigMapProfiles))
	if err != nil {
		return nil, err
	}

	return cm, nil
}

func (f *Factory) TunedConfigMapRecommend() (*corev1.ConfigMap, error) {
	cm, err := NewConfigMap(MustAssetReader(TunedConfigMapRecommend))
	if err != nil {
		return nil, err
	}

	return cm, nil
}

func (f *Factory) TunedDaemonSet() (*appsv1.DaemonSet, error) {
	ds, err := NewDaemonSet(MustAssetReader(TunedDaemonSet))
	if err != nil {
		return nil, err
	}
	return ds, nil
}

func NewServiceAccount(manifest io.Reader) (*corev1.ServiceAccount, error) {
	sa := corev1.ServiceAccount{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&sa); err != nil {
		return nil, err
	}
	return &sa, nil
}

func NewClusterRole(manifest io.Reader) (*rbacv1.ClusterRole, error) {
	cr := rbacv1.ClusterRole{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&cr); err != nil {
		return nil, err
	}
	return &cr, nil
}

func NewClusterRoleBinding(manifest io.Reader) (*rbacv1.ClusterRoleBinding, error) {
	crb := rbacv1.ClusterRoleBinding{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&crb); err != nil {
		return nil, err
	}
	return &crb, nil
}

func NewConfigMap(manifest io.Reader) (*corev1.ConfigMap, error) {
	cm := corev1.ConfigMap{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&cm); err != nil {
		return nil, err
	}
	return &cm, nil
}

func NewDaemonSet(manifest io.Reader) (*appsv1.DaemonSet, error) {
	ds := appsv1.DaemonSet{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&ds); err != nil {
		return nil, err
	}
	return &ds, nil
}

func NewService(manifest io.Reader) (*corev1.Service, error) {
	s := corev1.Service{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

func NewNamespace(manifest io.Reader) (*corev1.Namespace, error) {
	ns := corev1.Namespace{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&ns); err != nil {
		return nil, err
	}
	return &ns, nil
}

func NewDeployment(manifest io.Reader) (*appsv1.Deployment, error) {
	o := appsv1.Deployment{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&o); err != nil {
		return nil, err
	}
	return &o, nil
}

func NewTuned(manifest io.Reader) (*tunedv1alpha1.Tuned, error) {
	o := tunedv1alpha1.Tuned{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&o); err != nil {
		return nil, err
	}
	return &o, nil
}
