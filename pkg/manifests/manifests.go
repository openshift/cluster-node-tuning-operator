package manifests

import (
	"bytes"
	"io"
	"os"

	yamlv2 "gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"k8s.io/klog"
)

const (
	// Assets
	TunedConfigMapProfilesAsset = "assets/tuned/cm-tuned-profiles.yaml"
	TunedDaemonSetAsset         = "assets/tuned/ds-tuned.yaml"
	TunedProfileAsset           = "assets/tuned/tuned-profile.yaml"
	TunedCustomResourceAsset    = "assets/tuned/default-cr-tuned.yaml"
)

func MustAssetReader(asset string) io.Reader {
	return bytes.NewReader(MustAsset(asset))
}

func TunedConfigMapProfiles(tunedArray []*tunedv1.Tuned) *corev1.ConfigMap {
	cm, err := NewConfigMap(MustAssetReader(TunedConfigMapProfilesAsset))
	if err != nil {
		panic(err)
	}

	m := map[string]string{}
	for _, tuned := range tunedArray {
		tunedConfigMapProfiles(tuned, m)
	}
	tunedOcpProfiles, err := yamlv2.Marshal(&m)
	if err != nil {
		panic(err)
	}

	cm.Data["tuned-profiles-data"] = string(tunedOcpProfiles)

	return cm
}

func TunedDaemonSet() *appsv1.DaemonSet {
	ds, err := NewDaemonSet(MustAssetReader(TunedDaemonSetAsset))
	if err != nil {
		panic(err)
	}
	imageTuned := ntoconfig.NodeTunedImage()
	ds.Spec.Template.Spec.Containers[0].Image = imageTuned

	for i := range ds.Spec.Template.Spec.Containers[0].Env {
		if ds.Spec.Template.Spec.Containers[0].Env[i].Name == "RELEASE_VERSION" {
			ds.Spec.Template.Spec.Containers[0].Env[i].Value = os.Getenv("RELEASE_VERSION")
			break
		}
	}

	return ds
}

func TunedProfile() *tunedv1.Profile {
	p, err := NewTunedProfile(MustAssetReader(TunedProfileAsset))
	if err != nil {
		panic(err)
	}
	return p
}

func TunedCustomResource() *tunedv1.Tuned {
	cr, err := NewTuned(MustAssetReader(TunedCustomResourceAsset))
	if err != nil {
		panic(err)
	}
	return cr
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

func NewTunedProfile(manifest io.Reader) (*tunedv1.Profile, error) {
	p := tunedv1.Profile{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

func NewTuned(manifest io.Reader) (*tunedv1.Tuned, error) {
	o := tunedv1.Tuned{}
	if err := yaml.NewYAMLOrJSONDecoder(manifest, 100).Decode(&o); err != nil {
		return nil, err
	}
	return &o, nil
}

func tunedConfigMapProfiles(tuned *tunedv1.Tuned, m map[string]string) {
	if tuned.Spec.Profile != nil {
		for _, v := range tuned.Spec.Profile {
			if v.Name != nil && v.Data != nil {
				if _, found := m[*v.Name]; found {
					klog.Warningf("WARNING: Duplicate profile %s", *v.Name)
				}
				m[*v.Name] = *v.Data
			}
		}
	}
}
