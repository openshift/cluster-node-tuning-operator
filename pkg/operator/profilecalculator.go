package operator

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
)

const (
	// default Profile just in case default Tuned CR is inaccessible or incorrectly defined
	defaultProfile = "openshift-node"
)

type tunedState struct {
	nodeLabels map[string]map[string]string
	// Node name:  ^^^^^^
	// Node-specific label:   ^^^^^^
	podLabels map[string]map[string]map[string]string
	// Node name: ^^^^^^
	// Namespace/podname:    ^^^^^^
	// Pod-specific label:              ^^^^^^
	providerIDs map[string]string
	// Node name:   ^^^^^^
	// provider-id         ^^^^^^
	bootcmdline map[string]string
	// Node name:   ^^^^^^
	// bootcmdline         ^^^^^^
	bootcmdlineDeps map[string]string
	// Node name:       ^^^^^^
	// bootcmdline-deps         ^^^^^^
}

type ProfileCalculator struct {
	listers *ntoclient.Listers
	clients *ntoclient.Clients
	state   tunedState
}

func NewProfileCalculator(listers *ntoclient.Listers, clients *ntoclient.Clients) *ProfileCalculator {
	pc := &ProfileCalculator{
		listers: listers,
		clients: clients,
	}
	pc.state.nodeLabels = map[string]map[string]string{}
	pc.state.podLabels = map[string]map[string]map[string]string{}
	pc.state.providerIDs = map[string]string{}
	pc.state.bootcmdline = map[string]string{}
	pc.state.bootcmdlineDeps = map[string]string{}
	return pc
}

// podChangeHandler processes an event for Pod 'podNamespace/podName'.
//
// Returns
//   - the name of the Node the Pod is associated with in the
//     ProfileCalculator internal data structures
//   - an indication whether the event caused a node-wide Pod label change
//   - an error if any
func (pc *ProfileCalculator) podChangeHandler(podNamespace string, podName string) (string, bool, error) {
	var sb strings.Builder

	sb.WriteString(podNamespace)
	sb.WriteString("/")
	sb.WriteString(podName)
	podNamespaceName := sb.String()

	nodeName, podLabelsNew, err := pc.podLabelsGet(podNamespace, podName)
	if err != nil {
		if errors.IsNotFound(err) {
			// This is most likely the cause of a delete event;
			// find any record of a previous run of ns/name Pod, remove it from ProfileCalculator
			// internal data structures and investigate if this causes a node-wide Pod label change
			nodeName, change := pc.podRemove(podNamespaceName)
			return nodeName, change, nil
		}
		return "", false, err
	}

	if nodeName == "" {
		// Pods in Pending phase (being scheduled/unschedulable, downloading images over the network, ...)
		return nodeName, false, fmt.Errorf("Pod %s is not scheduled on any node", podNamespaceName)
	}

	if pc.state.podLabels[nodeName] == nil {
		pc.state.podLabels[nodeName] = map[string]map[string]string{}
	}
	podLabels := pc.state.podLabels[nodeName]

	if !util.MapOfStringsEqual(podLabelsNew, podLabels[podNamespaceName]) {
		// Pod podName labels on nodeName changed
		klog.V(3).Infof("Pod %s labels on Node %s changed: %v", podName, nodeName, true)
		changeNodeWide := podLabelsNodeWideChange(podLabels, podNamespaceName, podLabelsNew)
		podLabels[podNamespaceName] = podLabelsNew

		return nodeName, changeNodeWide, nil
	}

	// Pod labels for podNamespace/podName didn't change
	return nodeName, false, nil
}

// nodeChangeHandler processes an event for Node 'nodeName'.
//
// Returns
// * an indication whether the event caused a Node label/cloud-provider change
// * an error if any
func (pc *ProfileCalculator) nodeChangeHandler(nodeName string) (bool, error) {
	var change bool
	node, err := pc.listers.Nodes.Get(nodeName)
	if err != nil {
		if errors.IsNotFound(err) {
			// This is most likely the cause of a delete event;
			// remove nodeName from ProfileCalculator internal data structures
			pc.nodeRemove(nodeName)
			return true, err
		}

		return false, err
	}

	if node.Spec.ProviderID != pc.state.providerIDs[nodeName] {
		pc.state.providerIDs[nodeName] = node.Spec.ProviderID
		klog.V(3).Infof("Node's %s providerID=%v", nodeName, node.Spec.ProviderID)
		change = true
	}

	if node.Annotations != nil {
		if bootcmdlineAnnotVal, bootcmdlineAnnotSet := node.Annotations[tunedv1.TunedBootcmdlineAnnotationKey]; bootcmdlineAnnotSet {
			change = pc.state.bootcmdline[nodeName] != bootcmdlineAnnotVal
			pc.state.bootcmdline[nodeName] = bootcmdlineAnnotVal
		}
		if bootcmdlineDepsAnnotVal, bootcmdlineDepsAnnotSet := node.Annotations[tunedv1.TunedBootcmdlineDepsAnnotationKey]; bootcmdlineDepsAnnotSet {
			change = change || pc.state.bootcmdlineDeps[nodeName] != bootcmdlineDepsAnnotVal
			pc.state.bootcmdlineDeps[nodeName] = bootcmdlineDepsAnnotVal
		}
	}

	nodeLabelsNew := util.MapOfStringsCopy(node.Labels)

	if !util.MapOfStringsEqual(nodeLabelsNew, pc.state.nodeLabels[nodeName]) {
		// Node labels for nodeName changed
		pc.state.nodeLabels[nodeName] = nodeLabelsNew
		change = true
	}

	return change, nil
}

type ComputedProfile struct {
	TunedProfileName string
	AllProfiles      []tunedv1.TunedProfile
	Deferred         util.DeferMode
	MCLabels         map[string]string
	NodePoolName     string
	Operand          tunedv1.OperandConfig
	// BootcmdlineDeps is a list of all Tuned CR names and generations in the format
	// <name1>:<generation1>,<name2>:<generation2>,...<nameN>:<generationN> out of which
	// the current Tuned Profile was calculated from.  The Tuned CR list is sorted.
	BootcmdlineDeps string
}

type RecommendedProfile struct {
	TunedProfileName string
	Deferred         util.DeferMode
	Labels           map[string]string
	Config           tunedv1.OperandConfig
}

// calculateProfile calculates a tuned profile for Node nodeName.
//
// Returns
// * the tuned daemon profile name
// * the list of all TunedProfiles out of which the tuned profile was calculated
// * MachineConfig labels if the profile was selected by machineConfigLabels
// * operand configuration as defined by tunedv1.OperandConfig
// * bootcmdline dependencies (sorted Tuned CR names and their generations)
// * an error if any
func (pc *ProfileCalculator) calculateProfile(nodeName string) (ComputedProfile, error) {
	klog.V(3).Infof("calculateProfile(%s)", nodeName)
	tunedList, err := pc.listers.TunedResources.List(labels.Everything())

	if err != nil {
		return ComputedProfile{}, fmt.Errorf("failed to list Tuned: %v", err)
	}

	// Compute bootcmdline dependencies from all Tuned CRs.
	bootcmdlineDepsStr := bootcmdlineDeps(tunedList)

	profilesAll, err := tunedProfiles(tunedList)
	if err != nil {
		return ComputedProfile{}, err
	}

	recommendAll := TunedRecommend(tunedList)
	recommendProfile := func(nodeName string, iStart int) (int, RecommendedProfile, error) {
		var i int
		for i = iStart; i < len(recommendAll); i++ {
			var (
				pools []*mcfgv1.MachineConfigPool
				node  *corev1.Node
			)
			recommend := recommendAll[i]

			// Start with node/pod label based matching to MachineConfig matching when
			// both the match section and MachineConfigLabels are specified.
			// Also note the catch-all functionality when "recommend.Match == nil",
			// we do not want to call profileMatches() in that case unless machineConfigLabels
			// is undefined.
			if (recommend.Match != nil || recommend.MachineConfigLabels == nil) && pc.profileMatches(recommend.Match, nodeName) {
				return i, RecommendedProfile{
					TunedProfileName: *recommend.Profile,
					Config:           recommend.Operand,
					Deferred:         recommend.Deferred,
				}, nil
			}

			if recommend.MachineConfigLabels == nil {
				// Speed things up, empty labels (used as selectors) match/select nothing.
				continue
			}

			if node == nil {
				// We did not retrieve the node object from cache yet -- get it and also the pools
				// for this node.  Do not move this code outside the for loop, fetching the node/pools
				// is often unneeded and would likely have a performance impact.
				node, err = pc.listers.Nodes.Get(nodeName)
				if err != nil {
					return i, RecommendedProfile{}, err
				}

				pools, err = pc.getPoolsForNode(node)
				if err != nil {
					return i, RecommendedProfile{}, err
				}
			}

			// MachineConfigLabels based matching
			if pc.machineConfigLabelsMatch(recommend.MachineConfigLabels, pools) {
				return i, RecommendedProfile{
					TunedProfileName: *recommend.Profile,
					Labels:           recommend.MachineConfigLabels,
					Config:           recommend.Operand,
					Deferred:         recommend.Deferred,
				}, nil
			}
		}
		// No profile matches.  This is not necessarily a problem, e.g. when we check for matching profiles with the same priority.
		return i, RecommendedProfile{TunedProfileName: defaultProfile}, nil
	}
	iStop, recommendedProfile, err := recommendProfile(nodeName, 0)

	if iStop == len(recommendAll) {
		// This should never happen; the default Tuned CR should always be accessible and with a catch-all rule
		// in the "recommend" section to select the default profile for the tuned daemon.
		_, err = pc.listers.TunedResources.Get(tunedv1.TunedDefaultResourceName)
		if err != nil {
			return ComputedProfile{
				TunedProfileName: defaultProfile,
				Operand:          recommendedProfile.Config,
				BootcmdlineDeps:  bootcmdlineDepsStr,
			}, fmt.Errorf("failed to get Tuned %s: %v", tunedv1.TunedDefaultResourceName, err)
		}

		return ComputedProfile{
			TunedProfileName: defaultProfile,
			Operand:          recommendedProfile.Config,
			BootcmdlineDeps:  bootcmdlineDepsStr,
		}, fmt.Errorf("the default Tuned CR misses a catch-all profile selection")
	}

	// Make sure we do not have multiple matching profiles with the same priority.  If so, report a warning.
	for i := iStop + 1; i < len(recommendAll); i++ {
		j, recommendedProfileDup, err := recommendProfile(nodeName, i)
		if err != nil {
			// Duplicate matching profile priority detection failed, likely due to a failure to retrieve a k8s object.
			// This is not fatal, do not spam the logs, as we will retry later during a periodic resync.
			continue
		}
		if j == len(recommendAll) {
			// No other profile matched.
			break
		}
		i = j // This will also ensure we do not go through the same recommend rules when calling r() again.

		if recommendAll[iStop].Priority == nil || recommendAll[i].Priority == nil {
			// This should never happen as Priority is a required field, but just in case -- we don't want to crash below.
			klog.Warningf("one or both of profiles %s/%s have undefined priority", *recommendAll[iStop].Profile, *recommendAll[i].Profile)
			continue
		}
		// Warn if two profiles have the same priority, and different names.
		// If they have the same name and different contents a separate warning
		// will be issued by manifests.tunedRenderedProfiles()
		if *recommendAll[iStop].Priority == *recommendAll[i].Priority {
			if recommendedProfile.TunedProfileName != recommendedProfileDup.TunedProfileName {
				klog.Warningf("profiles %s/%s have the same priority %d and match %s; please use a different priority for your custom profiles!",
					recommendedProfile.TunedProfileName, recommendedProfileDup.TunedProfileName, *recommendAll[i].Priority, nodeName)
			}
		} else {
			// We no longer have recommend rules with the same priority -- do not go through the entire (priority-ordered) list.
			break
		}
	}

	return ComputedProfile{
		TunedProfileName: recommendedProfile.TunedProfileName,
		AllProfiles:      profilesAll,
		Deferred:         recommendedProfile.Deferred,
		MCLabels:         recommendedProfile.Labels,
		Operand:          recommendedProfile.Config,
		BootcmdlineDeps:  bootcmdlineDepsStr,
	}, err
}

type HypershiftRecommendedProfile struct {
	TunedProfileName string
	Deferred         util.DeferMode
	NodePoolName     string
	Config           tunedv1.OperandConfig
}

// calculateProfileHyperShift calculates a tuned profile for Node nodeName.
//
// Returns
// * the tuned daemon profile name
// * the list of all TunedProfiles out of which the tuned profile was calculated
// * the NodePool name for this Node
// * operand configuration as defined by tunedv1.OperandConfig
// * bootcmdline dependencies (sorted Tuned CR names and their generations)
// * an error if any
func (pc *ProfileCalculator) calculateProfileHyperShift(nodeName string) (ComputedProfile, error) {
	klog.V(3).Infof("calculateProfileHyperShift(%s)", nodeName)

	node, err := pc.listers.Nodes.Get(nodeName)
	if err != nil {
		return ComputedProfile{}, err
	}

	nodePoolName, err := pc.getNodePoolNameForNode(node)
	if err != nil {
		return ComputedProfile{}, err
	}

	// In HyperShift, we only consider the default profile and
	// the Tuned profiles from Tuneds referenced in this Nodes NodePool spec.
	tunedList, err := pc.listers.TunedResources.List(labels.SelectorFromValidatedSet(
		map[string]string{
			hypershiftNodePoolNameLabel: nodePoolName,
		}))
	if err != nil {
		return ComputedProfile{}, fmt.Errorf("failed to list Tuneds in NodePool %s: %v", nodePoolName, err)
	}
	defaultTuned, err := pc.listers.TunedResources.Get(tunedv1.TunedDefaultResourceName)
	if err != nil {
		return ComputedProfile{
			TunedProfileName: defaultProfile,
		}, fmt.Errorf("failed to get Tuned %s: %v", tunedv1.TunedDefaultResourceName, err)
	}
	tunedList = append(tunedList, defaultTuned)

	// Compute bootcmdline dependencies from all Tuned CRs.
	bootcmdlineDepsStr := bootcmdlineDeps(tunedList)

	profilesAll, err := tunedProfiles(tunedList)
	if err != nil {
		return ComputedProfile{}, err
	}

	recommendAll := TunedRecommend(tunedList)
	recommendProfile := func(nodeName string, iStart int) (int, HypershiftRecommendedProfile, error) {
		var i int
		for i = iStart; i < len(recommendAll); i++ {
			recommend := recommendAll[i]

			// Start with node/pod label based matching
			if recommend.Match != nil && pc.profileMatches(recommend.Match, nodeName) {
				klog.V(3).Infof("calculateProfileHyperShift: node / pod label matching used for node: %s, tunedProfileName: %s, nodePoolName: %s, operand: %v", nodeName, *recommend.Profile, "", recommend.Operand)
				return i, HypershiftRecommendedProfile{
					TunedProfileName: *recommend.Profile,
					Config:           recommend.Operand,
				}, nil
			}

			// If recommend.Match is empty, NodePool based matching is assumed
			if recommend.Match == nil {
				if *recommend.Profile == defaultProfile {
					// Don't set nodepool for default profile, no MachineConfigs should be generated.
					return i, HypershiftRecommendedProfile{
						TunedProfileName: *recommend.Profile,
						Config:           recommend.Operand,
					}, nil
				}
				klog.V(3).Infof("calculateProfileHyperShift: NodePool based matching used for node: %s, tunedProfileName: %s, nodePoolName: %s", nodeName, *recommend.Profile, nodePoolName)
				return i, HypershiftRecommendedProfile{
					TunedProfileName: *recommend.Profile,
					NodePoolName:     nodePoolName,
					Config:           recommend.Operand,
				}, nil
			}
		}
		// No profile matches.  This is not necessarily a problem, e.g. when we check for matching profiles with the same priority.
		return i, HypershiftRecommendedProfile{TunedProfileName: defaultProfile}, nil
	}
	iStop, recommendedProfile, err := recommendProfile(nodeName, 0)

	if iStop == len(recommendAll) {
		return ComputedProfile{
			TunedProfileName: defaultProfile,
			AllProfiles:      profilesAll,
			Operand:          recommendedProfile.Config,
			BootcmdlineDeps:  bootcmdlineDepsStr,
		}, fmt.Errorf("the default Tuned CR misses a catch-all profile selection")
	}

	// Make sure we do not have multiple matching profiles with the same priority.  If so, report a warning.
	for i := iStop + 1; i < len(recommendAll); i++ {
		j, recommendedProfileDup, err := recommendProfile(nodeName, i)
		if err != nil {
			// Duplicate matching profile priority detection failed, likely due to a failure to retrieve a k8s object.
			// This is not fatal, do not spam the logs, as we will retry later during a periodic resync.
			continue
		}
		if j == len(recommendAll) {
			// No other profile matched.
			break
		}
		i = j // This will also ensure we do not go through the same recommend rules when calling r() again.

		if recommendAll[iStop].Priority == nil || recommendAll[i].Priority == nil {
			// This should never happen as Priority is a required field, but just in case -- we don't want to crash below.
			klog.Warningf("one or both of profiles %s/%s have undefined priority", *recommendAll[iStop].Profile, *recommendAll[i].Profile)
			continue
		}
		// Warn if two profiles have the same priority, and different names.
		// If they have the same name and different contents a separate warning
		// will be issued by manifests.tunedRenderedProfiles()
		if *recommendAll[iStop].Priority == *recommendAll[i].Priority {
			if recommendedProfile.TunedProfileName != recommendedProfileDup.TunedProfileName {
				klog.Warningf("profiles %s/%s have the same priority %d and match %s; please use a different priority for your custom profiles!",
					recommendedProfile.TunedProfileName, recommendedProfileDup.TunedProfileName, *recommendAll[i].Priority, nodeName)
			}
		} else {
			// We no longer have recommend rules with the same priority -- do not go through the entire (priority-ordered) list.
			break
		}
	}

	return ComputedProfile{
		TunedProfileName: recommendedProfile.TunedProfileName,
		AllProfiles:      profilesAll,
		Deferred:         recommendedProfile.Deferred,
		NodePoolName:     recommendedProfile.NodePoolName,
		Operand:          recommendedProfile.Config,
		BootcmdlineDeps:  bootcmdlineDepsStr,
	}, err
}

// profileMatches returns true, if Node 'nodeName' fulfills all the necessary
// requirements of TunedMatch's tree-like definition of profile matching
// rules 'match'.
func (pc *ProfileCalculator) profileMatches(match []tunedv1.TunedMatch, nodeName string) bool {
	if len(match) == 0 {
		// Empty catch-all profile with no Node/Pod labels
		return true
	}

	for _, m := range match {
		var labelMatches bool

		if m.Type != nil && *m.Type == "pod" { // note the (lower-)case from the API
			labelMatches = pc.podLabelMatches(m.Label, m.Value, nodeName)
		} else {
			// Assume "node" type match; no types other than "node"/"pod" are allowed.
			// Unspecified m.Type means "node" type match.
			labelMatches = pc.nodeLabelMatches(m.Label, m.Value, nodeName)
		}
		if labelMatches {
			// AND condition, check if subtree matches too
			if pc.profileMatches(m.Match, nodeName) {
				return true
			}
		}
	}

	return false
}

// nodeLabelMatches returns true if Node label's 'mNodeLabel' value 'mNodeLabelValue'
// matches any of the Node labels in the ProfileCalculator internal data structures
// for Node of the name 'mNodeName'.
func (pc *ProfileCalculator) nodeLabelMatches(mNodeLabel *string, mNodeLabelValue *string, mNodeName string) bool {
	if mNodeLabel == nil {
		// Undefined node label matches
		return true
	}

	nodeLabels := pc.state.nodeLabels[mNodeName]
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

// podLabelMatches returns true if Pod label's 'mPodLabel' value 'mPodLabelValue'
// matches any of the Pod labels in the ProfileCalculator internal data structures
// for any Pod associated with Node of the name 'mNodeName'.
func (pc *ProfileCalculator) podLabelMatches(mPodLabel *string, mPodLabelValue *string, mNodeName string) bool {
	if mPodLabel == nil {
		// Undefined Pod label matches
		return true
	}

	podsPerNode := pc.state.podLabels[mNodeName]

	for _, podLabels := range podsPerNode {
		for podLabel, podLabelValue := range podLabels {
			if podLabel == *mPodLabel {
				if mPodLabelValue == nil || (podLabelValue == *mPodLabelValue) {
					// Undefined Pod label value matches
					return true
				}
				// Pod label value did not match, check the remaining pods on mNodeName
			}
		}
	}

	return false
}

// machineConfigLabelsMatch returns true if any of the MachineConfigPools 'pools' select 'machineConfigLabels' labels.
func (pc *ProfileCalculator) machineConfigLabelsMatch(machineConfigLabels map[string]string, pools []*mcfgv1.MachineConfigPool) bool {
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
			klog.Errorf("invalid label selector %s in MachineConfigPool %s: %v", util.ObjectInfo(selector), p.Name, err)
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

// nodeLabelsGet fetches labels for Node 'nodeName' from local cache.
//
// Returns
// * a copy of the Node nodeName labels
// * an error if any
func (pc *ProfileCalculator) nodeLabelsGet(nodeName string) (map[string]string, error) {
	node, err := pc.listers.Nodes.Get(nodeName)
	if err != nil {
		return nil, err
	}

	return util.MapOfStringsCopy(node.Labels), nil
}

// podLabelsGet fetches labels for Pod 'podNamespace/podName' from local cache.
//
// Returns
// * a copy of the Pod podNamespace/podName labels
// * an error if any
func (pc *ProfileCalculator) podLabelsGet(podNamespace, podName string) (string, map[string]string, error) {
	pod, err := pc.listers.Pods.Pods(podNamespace).Get(podName)
	if err != nil {
		return "", nil, err
	}

	return pod.Spec.NodeName, util.MapOfStringsCopy(pod.Labels), nil
}

// nodeRemove removes all data structures related to node "nodeName" in
// the ProfileCalculator internal data structures.
func (pc *ProfileCalculator) nodeRemove(nodeName string) {
	// Delete all structures related to nodeName in nodeLabels
	delete(pc.state.nodeLabels, nodeName)

	// Delete all data structures related to nodeName in podLabels
	delete(pc.state.podLabels, nodeName)
}

// podRemove removes the reference of a Pod identified by namespace/name
// from the ProfileCalculator internal data structures.  If such a reference
// is found, a calculation is made whether the removal causes a Node-wide change
// in terms of Pod label uniqueness.
//
// Returns
//   - the name of the Node the Pod was removed from (empty string if the removal
//     didn't take place)
//   - an indication whether the Pod removal causes a Node-wide change in terms
//     of Pod label uniqueness
func (pc *ProfileCalculator) podRemove(podNamespaceNameRemove string) (string, bool) {
	for nodeName, podsPerNode := range pc.state.podLabels {
		for podNamespaceName, podLabels := range podsPerNode {
			if podNamespaceNameRemove == podNamespaceName {
				delete(podsPerNode, podNamespaceName)
				klog.V(3).Infof("removed Pod %s from Node's %s local structures", podNamespaceName, nodeName)
				uniqueLabels := podLabelsUnique(podsPerNode,
					podNamespaceName,
					podLabels)

				return nodeName, len(uniqueLabels) > 0
			}
		}
	}
	return "", false
}

// podLabelsDelete removes the reference to any old podLabels structure data
func (pc *ProfileCalculator) podLabelsDelete() {
	pc.state.podLabels = map[string]map[string]map[string]string{}
}

// nodeLabelsDelete removes the reference to any old nodeLabels structure data
func (pc *ProfileCalculator) nodeLabelsDelete() {
	pc.state.nodeLabels = map[string]map[string]string{}
}

// tunedUsesNodeLabels returns true if any of the TunedMatch's tree-like definition
// of profile matching rules 'match' uses Node labels.
func (pc *ProfileCalculator) tunedUsesNodeLabels(match []tunedv1.TunedMatch) bool {
	if len(match) == 0 {
		// Empty catch-all profile with no Node/Pod labels
		return false
	}

	for _, m := range match {
		if m.Type == nil || (m.Type != nil && *m.Type == "node") { // note the (lower-)case from the API
			return true
		}
		// AND condition, check if subtree matches
		if pc.tunedUsesNodeLabels(m.Match) {
			return true
		}
	}

	return false
}

// tunedUsesPodLabels returns true if any of the TunedMatch's tree-like definition
// of profile matching rules 'match' uses Pod labels.
func (pc *ProfileCalculator) tunedUsesPodLabels(match []tunedv1.TunedMatch) bool {
	if len(match) == 0 {
		// Empty catch-all profile with no Node/Pod labels
		return false
	}

	for _, m := range match {
		if m.Type != nil && *m.Type == "pod" { // note the (lower-)case from the API
			return true
		}
		// AND condition, check if subtree matches
		if pc.tunedUsesPodLabels(m.Match) {
			return true
		}
	}

	return false
}

// tunedsUseNodeLabels returns true if any of the Tuned CRs uses Node labels.
func (pc *ProfileCalculator) tunedsUseNodeLabels(tunedSlice []*tunedv1.Tuned) bool {
	for _, recommend := range TunedRecommend(tunedSlice) {
		if pc.tunedUsesNodeLabels(recommend.Match) {
			return true
		}
	}
	return false
}

// tunedMatchesPodLabels returns true if Tuned CRs 'tuned' uses Pod labels.
func (pc *ProfileCalculator) tunedMatchesPodLabels(tuned *tunedv1.Tuned) bool {
	for _, recommend := range tuned.Spec.Recommend {
		if pc.tunedUsesPodLabels(recommend.Match) {
			return true
		}
	}
	return false
}

// tunedsUsePodLabels returns true if any of the Tuned CRs uses Pod labels.
func (pc *ProfileCalculator) tunedsUsePodLabels(tunedSlice []*tunedv1.Tuned) bool {
	for _, recommend := range TunedRecommend(tunedSlice) {
		if pc.tunedUsesPodLabels(recommend.Match) {
			return true
		}
	}
	return false
}

// getNodePoolNameForNode returns the NodePool name from a label on the hosted cluster Node
func (pc *ProfileCalculator) getNodePoolNameForNode(node *corev1.Node) (string, error) {
	nodePoolName := node.GetLabels()[hypershiftNodePoolLabel]
	klog.V(3).Infof("calculated nodePoolName: %s for node %s", nodePoolName, node.Name)
	return nodePoolName, nil
}

// tunedProfilesGet returns a TunedProfile map out of Tuned objects.
// Returns an error when TuneD profiles with the same name and different
// contents are detected.
func tunedProfilesGet(tunedSlice []*tunedv1.Tuned) (map[string]tunedv1.TunedProfile, error) {
	var dups bool // Do we have any duplicate TuneD profiles with different content?
	tuned2crName := map[string][]string{}
	m := map[string]tunedv1.TunedProfile{}

	for _, tuned := range tunedSlice {
		if tuned.Spec.Profile == nil {
			continue
		}
		for _, v := range tuned.Spec.Profile {
			if v.Name == nil || v.Data == nil {
				continue
			}
			if existingProfile, found := m[*v.Name]; found {
				if *v.Data == *existingProfile.Data {
					klog.Infof("duplicate profiles %s but they have the same contents", *v.Name)
				} else {
					// Duplicate profiles "*v.Name" with different content detected in Tuned CR "tuned.Name"
					// Do not spam the logs with this error, it will be logged elsewhere.
					dups = true
				}
			}
			m[*v.Name] = v
			tuned2crName[*v.Name] = append(tuned2crName[*v.Name], tuned.Name)
		}
	}

	if dups {
		duplicates := map[string]string{}

		for k, v := range tuned2crName {
			if len(v) < 2 {
				continue
			}
			for _, vv := range v {
				duplicates[vv] = k // Tuned CR name (vv) -> duplicate TuneD profile (k) mapping; ignore multiple duplicates to keep things simple
			}
		}

		return m, &DuplicateProfileError{crName: duplicates}
	}

	return m, nil
}

// tunedProfiles returns a name-sorted TunedProfile slice out of a slice of
// Tuned objects.  Returns an error when TuneD profiles with the same name and
// different contents are detected.
func tunedProfiles(tunedSlice []*tunedv1.Tuned) ([]tunedv1.TunedProfile, error) {
	tunedProfiles := []tunedv1.TunedProfile{}
	m, err := tunedProfilesGet(tunedSlice)

	for _, tunedProfile := range m {
		tunedProfiles = append(tunedProfiles, tunedProfile)
	}

	// The order of Tuned resources is variable and so is the order of profiles
	// within the resource itself.  Sort the rendered profiles by their names for
	// simpler change detection.
	sort.Slice(tunedProfiles, func(i, j int) bool {
		return *tunedProfiles[i].Name < *tunedProfiles[j].Name
	})

	return tunedProfiles, err
}

type TunedRecommendInfo struct {
	tunedv1.TunedRecommend
	Deferred util.DeferMode
}

// TunedRecommend returns a priority-sorted TunedRecommend slice out of
// a slice of Tuned objects for profile-calculation purposes.
func TunedRecommend(tunedSlice []*tunedv1.Tuned) []TunedRecommendInfo {
	var recommendAll []TunedRecommendInfo

	// Tuned profiles should have unique priority across all Tuned CRs and users
	// will be warned about this.  However, go into some effort to make the profile
	// selection deterministic even if users create two or more profiles with the
	// same priority.
	sort.Slice(tunedSlice, func(i, j int) bool {
		return tunedSlice[i].Name < tunedSlice[j].Name
	})

	for _, tuned := range tunedSlice {
		for _, recommend := range tuned.Spec.Recommend {
			recommendAll = append(recommendAll, TunedRecommendInfo{
				TunedRecommend: recommend,
				Deferred:       util.GetDeferredUpdateAnnotation(tuned.Annotations),
			})
		}
	}

	sort.SliceStable(recommendAll, func(i, j int) bool {
		if recommendAll[i].Priority != nil && recommendAll[j].Priority != nil {
			return *recommendAll[i].Priority < *recommendAll[j].Priority
		}
		return recommendAll[i].Priority != nil // undefined priority has the lowest priority
	})

	return recommendAll
}

// podLabelsUnique goes through Pod labels of all the Pods on a Node-wide
// 'podLabelsNodeWide' map and returns a subset of 'podLabels' unique to 'podNsName'
// Pod; i.e. the retuned labels (key & value) will not exist on any other Pod
// that is co-located on the same Node as 'podNsName' Pod.
func podLabelsUnique(podLabelsNodeWide map[string]map[string]string,
	podNsName string,
	podLabels map[string]string) map[string]string {
	unique := map[string]string{}

	if podLabelsNodeWide == nil {
		return podLabels
	}

LoopNeedle:
	for kNeedle, vNeedle := range podLabels {
		for kHaystack, vHaystack := range podLabelsNodeWide {
			if kHaystack == podNsName {
				// Skip the podNsName labels which are part of podLabelsNodeWide
				continue
			}
			if v, ok := vHaystack[kNeedle]; ok && v == vNeedle {
				// We've found a matching key/value pair label in vHaystack, kNeedle/vNeedle is not unique
				continue LoopNeedle
			}
		}

		// We've found label kNeedle with value vNeedle unique to Pod podNsName
		unique[kNeedle] = vNeedle
	}

	return unique
}

// podLabelsNodeWideChange returns true, if the change in current Pod labels
// 'podLabels' affects Pod labels Node-wide.  In other words, the function
// returns true if any of the new or removed labels (key & value) for 'podNsName'
// Pod do *not* exist on any other Pod that is co-located on the same Node as
// 'podNsName' Pod.
func podLabelsNodeWideChange(podLabelsNodeWide map[string]map[string]string,
	podNsName string,
	podLabels map[string]string) bool {
	if podLabelsNodeWide == nil {
		return len(podLabels) > 0
	}

	// Fetch old labels for Pod podNsName, not found on any other Pod that lives on the same Node
	oldPodLabelsUnique := podLabelsUnique(podLabelsNodeWide, podNsName, podLabelsNodeWide[podNsName])
	// Fetch current labels for Pod podNsName, not found on any other Pod that lives on the same Node
	curPodLabelsUnique := podLabelsUnique(podLabelsNodeWide, podNsName, podLabels)
	// If there is a difference between old and current unique Pod labels, a unique Pod label was
	// added/removed or both
	change := !util.MapOfStringsEqual(oldPodLabelsUnique, curPodLabelsUnique)

	return change
}

// bootcmdlineDeps returns a string containing a list of all Tuned CR names and generations
// in the format <name1>:<generation1>,<name2>:<generation2>,...<nameN>:<generationN>.
// The Tuned CR list is sorted.
func bootcmdlineDeps(tunedSlice []*tunedv1.Tuned) string {
	if len(tunedSlice) == 0 {
		return ""
	}

	// Sort the Tuned CRs by name for deterministic output.
	sortedTuneds := make([]*tunedv1.Tuned, len(tunedSlice))
	copy(sortedTuneds, tunedSlice)
	sort.Slice(sortedTuneds, func(i, j int) bool {
		return sortedTuneds[i].Name < sortedTuneds[j].Name
	})

	var sb strings.Builder
	for i, tuned := range sortedTuneds {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf("%s:%d", tuned.Name, tuned.Generation))
	}
	return sb.String()
}
