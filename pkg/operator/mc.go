package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-node-tuning-operator/pkg/util"

	ign3error "github.com/coreos/ignition/v2/config/shared/errors"
	ign3 "github.com/coreos/ignition/v2/config/v3_2"
	ign3types "github.com/coreos/ignition/v2/config/v3_2/types"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	GeneratedByControllerVersionAnnotationKey string = "tuned.openshift.io/generated-by-controller-version"
	MachineConfigPrefix                       string = "50-nto"
)

func newMachineConfig(name string, annotations map[string]string, labels map[string]string, kernelArguments []string,
	ignFiles []ign3types.File, ignUnits []ign3types.Unit) *mcfgv1.MachineConfig {
	if labels == nil {
		labels = map[string]string{}
	}
	if annotations == nil {
		annotations = map[string]string{}
	}

	ignTypesCfg := ign3types.Config{
		Ignition: ign3types.Ignition{
			Version: ign3types.MaxVersion.String(),
		},
	}
	if ignFiles != nil {
		ignTypesCfg.Storage = ign3types.Storage{Files: ignFiles}
	}
	if ignUnits != nil {
		ignTypesCfg.Systemd = ign3types.Systemd{Units: ignUnits}
	}

	rawNewIgnCfg, err := json.Marshal(ignTypesCfg)
	if err != nil {
		// This should never happen
		panic(err)
	}

	return &mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: mcfgv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: mcfgv1.MachineConfigSpec{
			Config: runtime.RawExtension{
				Raw: rawNewIgnCfg,
			},
			KernelArguments: kernelArguments,
		},
	}
}

// IgnParseWrapper parses rawIgn for V3.2 ignition config and returns
// a V3.2 Config or an error.
func ignParseWrapper(rawIgn []byte) (interface{}, error) {
	ignCfgV3_2, rptV3_2, errV3_2 := ign3.Parse(rawIgn)
	if errV3_2 == nil && !rptV3_2.IsFatal() {
		return ignCfgV3_2, nil
	}
	if errV3_2.Error() == ign3error.ErrUnknownVersion.Error() {
		// NTO handles NTO-created MachineConfigs only.  The first Ignition version
		// used was 2.2.0 and only Ignition version was provided by the Ignition
		// config.  Later a switch to 3.1.0 was made as we started support for
		// Storage/Systemd types.  As of 3.2.0 it is safe to ignore this error and
		// provide Ignition config with only Ignition version without pulling old
		// ignition dependencies for unneeded parsing.  Existing Ignition configs
		// will automatically be converted to the latest NTO-used Ignition version.
		ignTypesCfg := ign3types.Config{
			Ignition: ign3types.Ignition{
				Version: ign3types.MaxVersion.String(),
			},
		}

		return ignTypesCfg, nil
	}
	return ign3types.Config{}, fmt.Errorf("parsing Ignition config spec v3.2 failed with error: %v\nReport: %v", errV3_2, rptV3_2)
}

func parseAndConvertConfig(rawIgn []byte) (ign3types.Config, error) {
	ignconfigi, err := ignParseWrapper(rawIgn)
	if err != nil {
		return ign3types.Config{}, fmt.Errorf("failed to parse Ignition config: %v", err)
	}

	switch typedConfig := ignconfigi.(type) {
	case ign3types.Config:
		return ignconfigi.(ign3types.Config), nil
	default:
		return ign3types.Config{}, fmt.Errorf("unexpected type for ignition config: %v", typedConfig)
	}
}

func ignEqual(mcOld, mcNew *mcfgv1.MachineConfig) (bool, error) {
	ignOld, err := parseAndConvertConfig(mcOld.Spec.Config.Raw)
	if err != nil {
		return false, fmt.Errorf("parsing old Ignition config failed with error: %v", err)
	}
	ignNew, err := parseAndConvertConfig(mcNew.Spec.Config.Raw)
	if err != nil {
		return false, fmt.Errorf("parsing new Ignition config failed with error: %v", err)
	}

	return reflect.DeepEqual(ignOld.Storage.Files, ignNew.Storage.Files) && reflect.DeepEqual(ignOld.Systemd.Units, ignNew.Systemd.Units), nil
}

func getMachineConfigNameForPools(pools []*mcfgv1.MachineConfigPool) string {
	var (
		sb        strings.Builder
		sbPrimary strings.Builder
	)

	sb.WriteString(MachineConfigPrefix)
	for _, pool := range pools {
		if pool == nil {
			continue
		}

		sb.WriteString("-")
		if pool.Name == "master" || pool.Name == "worker" {
			sbPrimary.WriteString(pool.ObjectMeta.Name)
		} else {
			// This is a custom pool; a node can be a member of only one custom pool => return its name.
			sb.WriteString(pool.ObjectMeta.Name)
			return sb.String()
		}
	}
	sb.WriteString(sbPrimary.String())

	return sb.String()
}

// getPoolsForMachineConfigLabels chooses the MachineConfigPools that use MachineConfigs with labels 'mcLabels'.
// Errors are only returned in cases that warrant event reques (e.g. a failure to list k8s objects).
func (r *TunedReconciler) getPoolsForMachineConfigLabels(mcLabels map[string]string) ([]*mcfgv1.MachineConfigPool, error) {
	if len(mcLabels) == 0 {
		return nil, nil
	}

	mcpList := &mcfgv1.MachineConfigPoolList{}
	if err := r.Client.List(context.TODO(), mcpList); err != nil {
		return nil, err
	}

	var pools []*mcfgv1.MachineConfigPool
	for i := range mcpList.Items {
		p := &mcpList.Items[i]
		selector, err := metav1.LabelSelectorAsSelector(p.Spec.MachineConfigSelector)
		if err != nil {
			klog.Errorf("invalid label selector %s: %v", util.ObjectInfo(selector), err)
			return nil, nil
		}

		// A pool with a nil or empty selector matches nothing.
		if selector.Empty() || !selector.Matches(labels.Set(mcLabels)) {
			continue
		}
		pools = append(pools, p)
	}

	return pools, nil
}

// getPoolsForNode chooses the MachineConfigPools that should be used for a given node.
// It disambiguates in the case where e.g. a node has both master/worker roles applied,
// and where a custom role may be used. It returns a slice of all the pools the node belongs to.
// Errors are only returned in cases that warrant event reques (e.g. a failure to list k8s objects).
func (r *TunedReconciler) getPoolsForNode(node corev1.Node) ([]*mcfgv1.MachineConfigPool, error) {

	pl := &mcfgv1.MachineConfigPoolList{}
	if err := r.Client.List(context.TODO(), pl); err != nil {
		return nil, err
	}

	var pools []*mcfgv1.MachineConfigPool
	for i := range pl.Items {
		p := &pl.Items[i]
		selector, err := metav1.LabelSelectorAsSelector(p.Spec.NodeSelector)
		if err != nil {
			klog.Errorf("invalid label selector %s in MachineConfigPool %s: %v", util.ObjectInfo(selector), p.ObjectMeta.Name, err)
			continue
		}

		// A pool with a nil or empty selector matches nothing.
		if selector.Empty() || !selector.Matches(labels.Set(node.GetLabels())) {
			continue
		}

		pools = append(pools, p)
	}

	if len(pools) == 0 {
		// This is not an error, as there might be nodes in cluster that are not managed by machineconfigpool.
		return nil, nil
	}

	var master, worker *mcfgv1.MachineConfigPool
	var custom []*mcfgv1.MachineConfigPool
	for _, pool := range pools {
		if pool.Name == "master" {
			master = pool
		} else if pool.Name == "worker" {
			worker = pool
		} else {
			custom = append(custom, pool)
		}
	}

	if len(custom) > 1 {
		klog.Errorf("node %s belongs to %d custom roles, cannot proceed with this Node", node.Name, len(custom))
		return nil, nil
	} else if len(custom) == 1 {
		// We don't support making custom pools for masters
		if master != nil {
			klog.Errorf("node %s has both master role and custom role %s", node.GetName(), custom[0].Name)
			return nil, nil
		}
		// One custom role, let's use its pool
		pls := []*mcfgv1.MachineConfigPool{custom[0]}
		if worker != nil {
			pls = append(pls, worker)
		}
		return pls, nil
	} else if master != nil {
		// In the case where a node is both master/worker, have it live under
		// the master pool. This occurs in CodeReadyContainers and general
		// "single node" deployments, which one may want to do for testing bare
		// metal, etc.
		return []*mcfgv1.MachineConfigPool{master}, nil
	}
	// Otherwise, it's a worker with no custom roles.
	return []*mcfgv1.MachineConfigPool{worker}, nil
}
