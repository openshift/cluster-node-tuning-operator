package operator

import (
	"context"
	"fmt"
	"sort"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	// default Profile just in case default Tuned CR is inaccessible or incorrectly defined
	defaultProfile = "openshift-node"
)

type ProfileCalculator struct {
	client *client.Client
	state  tunedState
}

// calculateProfile calculates a tuned profile for Node nodeName.
//
// Returns
// * the tuned daemon profile name
// * MachineConfig labels if the profile was selected by machineConfigLabels
// * MachineConfigPools for 'nodeName' if the profile was selected by machineConfigLabels
// * whether to run the Tuned daemon in debug mode on node nodeName
// * an error if any
func (r *TunedReconciler) calculateProfile(ctx context.Context, node corev1.Node) (string, map[string]string, []*mcfgv1.MachineConfigPool, bool, error) {
	klog.V(3).Infof("calculateProfile(%s)", node.GetName())
	//TODO - we are listing tuned several times in the pipeline . consider listing once and using in all the reconcile procedure once.
	tunedList := &tunedv1.TunedList{}
	err := r.Client.List(ctx, tunedList, client.InNamespace(ntoconfig.OperatorNamespace()))

	if err != nil {
		return "", nil, nil, false, fmt.Errorf("failed to list Tuned: %v", err)
	}
	for _, recommend := range tunedRecommend(tunedList.Items) {
		var (
			pools []*mcfgv1.MachineConfigPool
		)

		// Start with node/pod label based matching to MachineConfig matching when
		// both the match section and MachineConfigLabels are specified.
		// Also note the catch-all functionality when "recommend.Match == nil",
		// we do not want to call profileMatches() in that case unless machineConfigLabels
		// is undefined.
		if (recommend.Match != nil || recommend.MachineConfigLabels == nil) && r.profileMatches(recommend.Match, node) {
			return *recommend.Profile, nil, nil, recommend.Operand.Debug, nil
		}

		if recommend.MachineConfigLabels == nil {
			// Speed things up, empty labels (used as selectors) match/select nothing.
			continue
		}

		pools, err = r.getPoolsForNode(node)
		if err != nil {
			return "", nil, nil, false, err
		}

		// MachineConfigLabels based matching
		if r.machineConfigLabelsMatch(recommend.MachineConfigLabels, pools) {
			return *recommend.Profile, recommend.MachineConfigLabels, pools, recommend.Operand.Debug, nil
		}
	}

	// This should never happen; the default Tuned CR should always be accessible and with a catch-all rule
	// in the "recommend" section to select the default profile for the tuned daemon.
	tunedDefault := &tunedv1.Tuned{}
	key := types.NamespacedName{
		Namespace: ntoconfig.OperatorNamespace(),
		Name:      tunedv1.TunedDefaultResourceName,
	}

	err = r.Get(context.TODO(), key, tunedDefault)

	if err != nil {
		return defaultProfile, nil, nil, false, fmt.Errorf("failed to get Tuned %s: %v", tunedv1.TunedDefaultResourceName, err)
	}

	return defaultProfile, nil, nil, false, fmt.Errorf("the default Tuned CR misses a catch-all profile selection")
}

// profileMatches returns true, if Node 'nodeName' fulfills all the necessary
// requirements of TunedMatch's tree-like definition of profile matching
// rules 'match'.
func (r *TunedReconciler) profileMatches(match []tunedv1.TunedMatch, node corev1.Node) bool {
	if len(match) == 0 {
		// Empty catch-all profile with no Node/Pod labels
		return true
	}

	for _, m := range match {
		var labelMatches bool

		if m.Type != nil && *m.Type == "pod" { // note the (lower-)case from the API
			labelMatches = r.podLabelMatches(m.Label, m.Value, node.Name)
		} else {
			// Assume "node" type match; no types other than "node"/"pod" are allowed.
			// Unspecified m.Type means "node" type match.
			labelMatches = r.nodeLabelMatches(m.Label, m.Value, node)
		}
		if labelMatches {
			// AND condition, check if subtree matches too
			if r.profileMatches(m.Match, node) {
				return true
			}
		}
	}

	return false
}

// nodeLabelMatches returns true if Node label's 'mNodeLabel' value 'mNodeLabelValue'
// matches any of the Node labels in the ProfileCalculator internal data structures
// for Node of the name 'mNodeName'.
func (r *TunedReconciler) nodeLabelMatches(mNodeLabel *string, mNodeLabelValue *string, node corev1.Node) bool {
	if mNodeLabel == nil {
		// Undefined node label matches
		return true
	}

	nodeLabels := node.GetLabels()
	for nodeLabel, nodeLabelValue := range nodeLabels {
		if nodeLabel == *mNodeLabel {
			if mNodeLabelValue != nil {
				return nodeLabelValue == *mNodeLabelValue
			}
			// Undefined Node label value matches
			return true
		}
	}

	return false
}

func (r *TunedReconciler) podLabelMatches(mPodLabel *string, mPodLabelValue *string, mNodeName string) bool {
	podList := &corev1.PodList{}
	labelsMap := map[string]string{}
	if mPodLabel != nil {
		if mPodLabelValue != nil {
			labelsMap[*mPodLabel] = *mPodLabelValue
		} else {
			labelsMap[*mPodLabel] = ""
		}
	}
	err := r.List(context.TODO(), podList, client.MatchingFields{"spec.nodeName": mNodeName}, client.MatchingLabels(labelsMap))
	if err != nil {
		klog.Errorf("could not list pods for label matching %v", err)
		return false
	}
	return len(podList.Items) > 0
}

// machineConfigLabelsMatch returns true if any of the MachineConfigPools 'pools' select 'machineConfigLabels' labels.
func (r *TunedReconciler) machineConfigLabelsMatch(machineConfigLabels map[string]string, pools []*mcfgv1.MachineConfigPool) bool {
	if machineConfigLabels == nil || pools == nil {
		// Undefined MachineConfig labels or no pools provided are not a valid match
		return false
	}

	labelSelector := &metav1.LabelSelector{
		MatchLabels: machineConfigLabels,
	}

	selector, err := metav1.LabelSelectorAsSelector(labelSelector)
	if err != nil {
		// Invalid label selector, do not propagate this user error to the event loop, only log this
		klog.Errorf("invalid label selector %s: %v", util.ObjectInfo(selector), err)
		return false
	}

	for _, p := range pools {
		selector, err := metav1.LabelSelectorAsSelector(p.Spec.MachineConfigSelector)
		if err != nil {
			klog.Errorf("invalid label selector %s in MachineConfigPool %s: %v", util.ObjectInfo(selector), p.ObjectMeta.Name, err)
			continue
		}

		// A resource with a nil or empty selector matches nothing.
		if selector.Empty() || !selector.Matches(labels.Set(machineConfigLabels)) {
			continue
		}

		return true
	}

	return false
}

// tunedUsesNodeLabels returns true if any of the TunedMatch's tree-like definition
// of profile matching rules 'match' uses Node labels.
func (r *TunedReconciler) tunedUsesNodeLabels(match []tunedv1.TunedMatch) bool {
	if len(match) == 0 {
		// Empty catch-all profile with no Node/Pod labels
		return false
	}

	for _, m := range match {
		if m.Type == nil || (m.Type != nil && *m.Type == "node") { // note the (lower-)case from the API
			return true
		}
		// AND condition, check if subtree matches
		if r.tunedUsesNodeLabels(m.Match) {
			return true
		}
	}

	return false
}

// tunedUsesPodLabels returns true if any of the TunedMatch's tree-like definition
// of profile matching rules 'match' uses Pod labels.
func (r *TunedReconciler) tunedUsesPodLabels(match []tunedv1.TunedMatch) bool {
	if len(match) == 0 {
		// Empty catch-all profile with no Node/Pod labels
		return false
	}

	for _, m := range match {
		if m.Type != nil && *m.Type == "pod" { // note the (lower-)case from the API
			return true
		}
		// AND condition, check if subtree matches
		if r.tunedUsesPodLabels(m.Match) {
			return true
		}
	}

	return false
}

// tunedsUseNodeLabels returns true if any of the Tuned CRs uses Node labels.
func (r *TunedReconciler) tunedsUseNodeLabels(tunedSlice []tunedv1.Tuned) bool {
	for _, recommend := range tunedRecommend(tunedSlice) {
		if r.tunedUsesNodeLabels(recommend.Match) {
			return true
		}
	}
	return false
}

// tunedsUsePodLabels returns true if any of the Tuned CRs uses Pod labels.
func (r *TunedReconciler) tunedsUsePodLabels(tunedList *tunedv1.TunedList) bool {
	for _, recommend := range tunedRecommend(tunedList.Items) {
		if r.tunedUsesPodLabels(recommend.Match) {
			return true
		}
	}
	return false
}

// tunedRecommend returns a priority-sorted TunedRecommend slice out of
// a slice of Tuned objects for profile-calculation purposes.
func tunedRecommend(tunedSlice []tunedv1.Tuned) []tunedv1.TunedRecommend {
	recommendAll := []tunedv1.TunedRecommend{}

	// Tuned profiles should have unique priority across all Tuned CRs and users
	// will be warned about this.  However, go into some effort to make the profile
	// selection deterministic even if users create two or more profiles with the
	// same priority.
	sort.Slice(tunedSlice, func(i, j int) bool {
		return tunedSlice[i].Name < tunedSlice[j].Name
	})

	for _, tuned := range tunedSlice {
		if tuned.Spec.Recommend != nil {
			recommendAll = append(recommendAll, tuned.Spec.Recommend...)
		}
	}

	sort.SliceStable(recommendAll, func(i, j int) bool {
		if recommendAll[i].Priority != nil && recommendAll[j].Priority != nil {
			return *recommendAll[i].Priority < *recommendAll[j].Priority
		}
		return recommendAll[i].Priority != nil // undefined priority has the lowest priority
	})

	for i := 0; i < len(recommendAll)-1; i++ {
		if recommendAll[i].Priority == nil || recommendAll[i+1].Priority == nil {
			continue
		}
		if *recommendAll[i].Priority == *recommendAll[i+1].Priority {
			klog.Warningf("profiles %s/%s have the same priority %d, please use a different priority for your custom profiles!",
				*recommendAll[i].Profile, *recommendAll[i+1].Profile, *recommendAll[i].Priority)
		}
	}

	return recommendAll
}
