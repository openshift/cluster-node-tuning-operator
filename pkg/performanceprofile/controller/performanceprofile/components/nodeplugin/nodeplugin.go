package nodeplugin

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	mixedcpusmf "github.com/openshift-kni/mixed-cpu-node-plugin/pkg/manifests"
	assets "github.com/openshift/cluster-node-tuning-operator/assets/performanceprofile"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	defaultIgnitionContentSource = "data:text/plain;charset=utf-8;base64"
	pluginName                   = "mixed-cpu"
)

const (
	nriConfigFileName = "99-nri.conf"
	crioConfigDirPath = "/etc/crio/crio.conf.d"
)

const templateNRIEnable = "Enable"

// Components is a wrapper for mixed-cpu-node-plugin manifests which
// exposes only the resources that needed for OCP deployment.
type Components struct {
	internal *mixedcpusmf.Manifests
}

// ToUnstructured return a list of all manifests converted to objects
func (c *Components) ToUnstructured() ([]*unstructured.Unstructured, error) {
	return toUnstructured(&c.internal.SA, &c.internal.Role, &c.internal.RB, &c.internal.DS)
}

func toUnstructured(objs ...client.Object) ([]*unstructured.Unstructured, error) {
	uns := make([]*unstructured.Unstructured, 0)
	for _, obj := range objs {
		key := fmt.Sprintf("(%s) %s/%s", obj.GetObjectKind().GroupVersionKind(), obj.GetNamespace(), obj.GetName())

		b, err := json.Marshal(obj)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal object: %q", key)
		}

		u := &unstructured.Unstructured{}
		err = json.Unmarshal(b, u)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal object: %q", key)
		}

		uns = append(uns, u)
	}
	return uns, nil
}

func Enabled(profile *performancev2.PerformanceProfile) bool {
	if profile.Spec.CPU == nil || profile.Spec.CPU.Shared == nil || *profile.Spec.CPU.Shared == "" {
		return false
	}
	if profile.Spec.WorkloadHints == nil || profile.Spec.WorkloadHints.MixedCpus == nil {
		return false
	}
	return *profile.Spec.WorkloadHints.MixedCpus
}

func SharedCPUs(profile *performancev2.PerformanceProfile) (*cpuset.CPUSet, error) {
	// TODO reflect error in profile status?
	cpus := *profile.Spec.CPU.Shared
	sharedCPUs, err := cpuset.Parse(string(cpus))
	if err != nil {
		return nil, fmt.Errorf("failed to parse cpuset %q; %w", cpus, err)
	}
	return &sharedCPUs, nil
}

// Activate setups all the components that are needed for node plugin activation
// 1. it enables NRI in CRI-O config
// 2. it updates Kubelet systemReservedCPUs to include the shared cpus
// 3. it updates role to use securitycontextconstraints
// 4. it returns the mixed-cpu-node-plugin manifests, with the right namespace and shared-cpus
func Activate(profile *performancev2.PerformanceProfile, mc *machineconfigv1.MachineConfig, kc *machineconfigv1.KubeletConfig) (*Components, error) {
	if err := configNRI(mc, true); err != nil {
		return nil, err
	}

	cpus, err := SharedCPUs(profile)
	if err != nil {
		return nil, err
	}

	if err = configSharedCPUs(kc, cpus); err != nil {
		return nil, err
	}
	mf, err := mixedcpusmf.Get(cpus.String(), mixedcpusmf.WithNamespace(components.NamespaceNodeTuningOperator), mixedcpusmf.WithName(GetName(profile)))
	if err != nil {
		return nil, err
	}

	// use the existing privileged SCC
	// similar to what tuned is doing
	mf.Role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups:     []string{"security.openshift.io"},
			Resources:     []string{"securitycontextconstraints"},
			Verbs:         []string{"use"},
			ResourceNames: []string{"privileged"},
		},
	}
	podSpec := &mf.DS.Spec.Template.Spec
	podSpec.Containers[0].SecurityContext = &corev1.SecurityContext{Privileged: pointer.Bool(true)}
	// run ds pods only on nodes associate with the profile
	podSpec.NodeSelector = profile.Spec.NodeSelector

	return &Components{internal: mf}, nil
}

func configNRI(mc *machineconfigv1.MachineConfig, enable bool) error {
	ignitionConfig := &igntypes.Config{}
	err := json.Unmarshal(mc.Spec.Config.Raw, ignitionConfig)
	if err != nil {
		return err
	}

	nriConfigTemplate, err := template.ParseFS(assets.Configs, filepath.Join("configs", nriConfigFileName))
	nriConfig := &bytes.Buffer{}
	if err = nriConfigTemplate.Execute(nriConfig, map[string]string{templateNRIEnable: strconv.FormatBool(enable)}); err != nil {
		return err
	}

	contentBase64 := base64.StdEncoding.EncodeToString(nriConfig.Bytes())
	ignitionConfig.Storage.Files = append(ignitionConfig.Storage.Files, igntypes.File{
		Node: igntypes.Node{
			Path: filepath.Join(crioConfigDirPath, nriConfigFileName),
		},
		FileEmbedded1: igntypes.FileEmbedded1{
			Contents: igntypes.Resource{
				Source: pointer.String(fmt.Sprintf("%s,%s", defaultIgnitionContentSource, contentBase64)),
			},
			Mode: pointer.Int(448),
		},
	})

	rawIgnition, err := json.Marshal(ignitionConfig)
	if err != nil {
		return err
	}
	mc.Spec.Config = runtime.RawExtension{Raw: rawIgnition}
	return nil
}

func configSharedCPUs(kc *machineconfigv1.KubeletConfig, SharedCPUs *cpuset.CPUSet) error {
	kcConfig := &kubeletconfigv1beta1.KubeletConfiguration{}
	err := json.Unmarshal(kc.Spec.KubeletConfig.Raw, kcConfig)
	if err != nil {
		return err
	}
	reserved, err := cpuset.Parse(kcConfig.ReservedSystemCPUs)
	if err != nil {
		return err
	}
	// append the shared cpus into the ReservedSystemCPUs
	kcConfig.ReservedSystemCPUs = SharedCPUs.Union(reserved).String()
	raw, err := json.Marshal(kcConfig)
	if err != nil {
		return err
	}
	kc.Spec.KubeletConfig = &runtime.RawExtension{Raw: raw}
	return nil
}
