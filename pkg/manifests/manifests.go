package manifests

import (
	"bytes"
	"io"
	"os"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"k8s.io/klog"
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

func TunedRenderedResource(tunedSlice []*tunedv1.Tuned) *tunedv1.Tuned {
	cr := &tunedv1.Tuned{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tunedv1.TunedRenderedResourceName,
			Namespace: ntoconfig.OperatorNamespace(),
		},
		Spec: tunedv1.TunedSpec{
			Recommend: []tunedv1.TunedRecommend{},
		},
	}

	tunedProfiles := []tunedv1.TunedProfile{}
	m := map[string]tunedv1.TunedProfile{}

	for _, tuned := range tunedSlice {
		if tuned.Name == tunedv1.TunedRenderedResourceName {
			// Skip the "rendered" Tuned resource itself
			continue
		}
		tunedRenderedProfiles(tuned, m)
	}
	for _, tunedProfile := range m {
		if tunedProfile.Name == nil {
			// This should never happen (openAPIV3Schema validation); ignore invalid profiles
			continue
		}
		tunedProfiles = append(tunedProfiles, tunedProfile)
	}

	// The order of Tuned resources is variable and so is the order of profiles
	// within the resource itself.  Sort the rendered profiles by their names for
	// simpler change detection.
	sort.Slice(tunedProfiles, func(i, j int) bool {
		return *tunedProfiles[i].Name < *tunedProfiles[j].Name
	})

	cr.Spec.Profile = tunedProfiles

	return cr
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

func tunedRenderedProfiles(tuned *tunedv1.Tuned, m map[string]tunedv1.TunedProfile) {
	if tuned.Spec.Profile != nil {
		for _, v := range tuned.Spec.Profile {
			if v.Name != nil && v.Data != nil {
				if _, found := m[*v.Name]; found {
					klog.Warningf("WARNING: Duplicate profile %s", *v.Name)
				}
				m[*v.Name] = v
			}
		}
	}
}
