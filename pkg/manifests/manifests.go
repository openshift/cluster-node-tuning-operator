package manifests

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"sort"

	yamlv2 "gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	tunedv1alpha1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1alpha1"
)

const (
	// Node labels file is a file with node labels in a pod with the tuned daemon
	nodeLabelsFile = "/var/lib/tuned/ocp-node-labels.cfg"
	// Assets
	TunedServiceAccount     = "assets/tuned/01-service-account.yaml"
	TunedClusterRole        = "assets/tuned/02-cluster-role.yaml"
	TunedClusterRoleBinding = "assets/tuned/03-cluster-role-binding.yaml"
	TunedConfigMapProfiles  = "assets/tuned/04-cm-tuned-profiles.yaml"
	TunedConfigMapRecommend = "assets/tuned/05-cm-tuned-recommend.yaml"
	TunedDaemonSet          = "assets/tuned/06-ds-tuned.yaml"
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

func (f *Factory) TunedConfigMapProfiles(tuned *tunedv1alpha1.Tuned) (*corev1.ConfigMap, error) {
	cm, err := NewConfigMap(MustAssetReader(TunedConfigMapProfiles))
	if err != nil {
		return nil, err
	}

	if tuned.Spec.Profiles != nil {
		m := make(map[string]string)
		for _, v := range tuned.Spec.Profiles {
			if v.Name != nil && v.Data != nil {
				m[*v.Name] = *v.Data
			}
		}
		tunedOcpProfiles, err := yamlv2.Marshal(&m)
		if err != nil {
			log.Fatalf("error: %v", err)
		}

		cm.Data["tuned-profiles-data"] = string(tunedOcpProfiles)
	}

	return cm, nil
}

func (f *Factory) TunedConfigMapRecommend(tuned *tunedv1alpha1.Tuned) (*corev1.ConfigMap, error) {
	cm, err := NewConfigMap(MustAssetReader(TunedConfigMapRecommend))
	if err != nil {
		return nil, err
	}

	if tuned.Spec.Recommend != nil {
		recommendConf := ""
		sort.Slice(tuned.Spec.Recommend, func(i, j int) bool {
			if tuned.Spec.Recommend[i].Priority != nil && tuned.Spec.Recommend[j].Priority != nil {
				return *tuned.Spec.Recommend[i].Priority < *tuned.Spec.Recommend[j].Priority
			}
			return false
		})
		i := 0
		for _, r := range tuned.Spec.Recommend {
			if r.Profile != nil {
				recommendConf += fmt.Sprintf("[%s,%d]\n", *r.Profile, i)
				recommendConf += nodeLabelsFile + "=.*"
				if r.Label != nil {
					if r.Label.Name != nil {
						recommendConf += *r.Label.Name + "="
						if r.Label.Value != nil {
							recommendConf += *r.Label.Value
						}
					} else {
						// label name wasn't specified, ignore it (profile catch-all)
					}
				}
				recommendConf += "\n\n"
			} else {
				// no profile was specified, ignore this TunedRecommend struct
			}
			i++
		}

		cm.Data["tuned-ocp-recommend"] = recommendConf
	}

	return cm, nil
}

func (f *Factory) TunedDaemonSet() (*appsv1.DaemonSet, error) {
	ds, err := NewDaemonSet(MustAssetReader(TunedDaemonSet))
	imageTuned := os.Getenv("CLUSTER_NODE_TUNED_IMAGE")
	ds.Spec.Template.Spec.Containers[0].Image = imageTuned

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
