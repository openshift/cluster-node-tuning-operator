package manifests

import (
	"bytes"
	"io"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
)

const (
	// Assets
	TunedDaemonSetAsset      = "assets/tuned/manifests/ds-tuned.yaml"
	TunedProfileAsset        = "assets/tuned/manifests/tuned-profile.yaml"
	TunedCustomResourceAsset = "assets/tuned/manifests/default-cr-tuned.yaml"
)

func MustAssetReader(asset string) io.Reader {
	return bytes.NewReader(MustAsset(asset))
}

func TunedDaemonSet() *appsv1.DaemonSet {
	ds, err := NewDaemonSet(MustAssetReader(TunedDaemonSetAsset))
	if err != nil {
		panic(err)
	}
	imageTuned := ntoconfig.NodeTunedImage()
	ds.Spec.Template.Spec.Containers[0].Image = imageTuned

	for i := range ds.Spec.Template.Spec.Containers[0].Env {
		switch ds.Spec.Template.Spec.Containers[0].Env[i].Name {
		case "RELEASE_VERSION":
			ds.Spec.Template.Spec.Containers[0].Env[i].Value = os.Getenv("RELEASE_VERSION")
		case "CLUSTER_NODE_TUNED_IMAGE":
			ds.Spec.Template.Spec.Containers[0].Env[i].Value = os.Getenv("CLUSTER_NODE_TUNED_IMAGE")
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
