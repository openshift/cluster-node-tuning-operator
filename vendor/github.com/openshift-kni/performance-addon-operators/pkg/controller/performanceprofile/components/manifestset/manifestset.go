package manifestset

import (
	performancev2 "github.com/openshift-kni/performance-addon-operators/api/v2"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components/kubeletconfig"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components/machineconfig"
	profilecomponent "github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components/profile"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components/runtimeclass"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components/tuned"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	nodev1beta1 "k8s.io/api/node/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ManifestResultSet contains all component's instances that should be created according to performance-profile
type ManifestResultSet struct {
	MachineConfig *mcov1.MachineConfig
	KubeletConfig *mcov1.KubeletConfig
	Tuned         *tunedv1.Tuned
	RuntimeClass  *nodev1beta1.RuntimeClass
}

// ManifestTable is map with Kind name as key and component's instance as value
type ManifestTable map[string]interface{}

// ToObjects return a list of all manifests converted to objects
func (ms *ManifestResultSet) ToObjects() []metav1.Object {
	objs := make([]metav1.Object, 0)

	objs = append(objs,
		ms.MachineConfig.GetObjectMeta(),
		ms.KubeletConfig.GetObjectMeta(),
		ms.Tuned.GetObjectMeta(),
		ms.RuntimeClass.GetObjectMeta(),
	)
	return objs
}

// ToManifestTable return a map with Kind name as key and component's instance as value
func (ms *ManifestResultSet) ToManifestTable() ManifestTable {
	manifests := make(map[string]interface{}, 0)
	manifests[ms.MachineConfig.Kind] = ms.MachineConfig
	manifests[ms.KubeletConfig.Kind] = ms.KubeletConfig
	manifests[ms.Tuned.Kind] = ms.Tuned
	manifests[ms.RuntimeClass.Kind] = ms.RuntimeClass
	return manifests
}

// GetNewComponents return a list of all component's instances that should be created according to profile
func GetNewComponents(profile *performancev2.PerformanceProfile, profileMCP *mcov1.MachineConfigPool) (*ManifestResultSet, error) {
	machineConfigPoolSelector := profilecomponent.GetMachineConfigPoolSelector(profile, profileMCP)

	mc, err := machineconfig.New(profile)
	if err != nil {
		return nil, err
	}

	kc, err := kubeletconfig.New(profile, machineConfigPoolSelector)
	if err != nil {
		return nil, err
	}

	performanceTuned, err := tuned.NewNodePerformance(profile)
	if err != nil {
		return nil, err
	}

	runtimeClass := runtimeclass.New(profile, machineconfig.HighPerformanceRuntime)

	manifestResultSet := ManifestResultSet{
		MachineConfig: mc,
		KubeletConfig: kc,
		Tuned:         performanceTuned,
		RuntimeClass:  runtimeClass,
	}
	return &manifestResultSet, nil
}
