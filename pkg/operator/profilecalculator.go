package operator

import (
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
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
	return pc
}

// podChangeHandler processes an event for Pod 'podNamespace/podName'.
//
// Returns
// * the name of the Node the Pod is associated with in the
//   ProfileCalculator internal data structures
// * an indication whether the event caused a node-wide Pod label change
// * an error if any
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

	if !mapOfStringsEqual(podLabelsNew, podLabels[podNamespaceName]) {
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
// * an indication whether the event caused a Node label change
// * an error if any
func (pc *ProfileCalculator) nodeChangeHandler(nodeName string) (bool, error) {
	nodeLabelsNew, err := pc.nodeLabelsGet(nodeName)
	if err != nil {
		if errors.IsNotFound(err) {
			// This is most likely the cause of a delete event;
			// remove nodeName from ProfileCalculator internal data structures
			pc.nodeRemove(nodeName)
			return true, err
		}

		return false, err
	}

	if !mapOfStringsEqual(nodeLabelsNew, pc.state.nodeLabels[nodeName]) {
		// Node labels for nodeName changed
		pc.state.nodeLabels[nodeName] = nodeLabelsNew

		return true, nil
	}

	// Node labels for nodeName didn't change
	return false, nil
}

// calculateProfile calculates a tuned profile for Node nodeName.
//
// Returns
// * the tuned daemon profile name
// * an error if any
func (pc *ProfileCalculator) calculateProfile(nodeName string) (string, error) {
	klog.V(3).Infof("calculateProfile(%s)", nodeName)
	tunedList, err := pc.listers.TunedResources.List(labels.Everything())
	if err != nil {
		return "", fmt.Errorf("failed to list Tuned: %v", err)
	}

	for _, recommend := range tunedRecommend(tunedList) {
		if pc.profileMatches(recommend.Match, nodeName) {
			return *recommend.Profile, nil
		}
	}

	// This should never happen; the default Tuned CR should always be accessible and with a catch-all rule
	// in the "recommend" section to select the default profile for the tuned daemon.
	_, err = pc.listers.TunedResources.Get(tunedv1.TunedDefaultResourceName)
	if err != nil {
		return defaultProfile, fmt.Errorf("failed to get Tuned %s: %v", tunedv1.TunedDefaultResourceName, err)
	}

	return defaultProfile, fmt.Errorf("the default Tuned CR misses a catch-all profile selection")
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

	return mapOfStringsCopy(node.Labels), nil
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

	return pod.Spec.NodeName, mapOfStringsCopy(pod.Labels), nil
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
// * the name of the Node the Pod was removed from (empty string if the removal
//   didn't take place)
// * an indication whether the Pod removal causes a Node-wide change in terms
//   of Pod label uniqueness
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
		// AND condition, check if subtree matches too
		if pc.tunedUsesPodLabels(m.Match) {
			return true
		}
	}

	return false
}

// tunedsUsePodLabels returns true if any of the Tuned CRs uses Pod labels.
func (pc *ProfileCalculator) tunedsUsePodLabels(tunedSlice []*tunedv1.Tuned) bool {
	for _, recommend := range tunedRecommend(tunedSlice) {
		if pc.tunedUsesPodLabels(recommend.Match) {
			return true
		}
	}
	return false
}

// tunedRecommend returns a priority-sorted TunedRecommend slice out of
// a slice of Tuned objects for profile-calculation purposes.
func tunedRecommend(tunedSlice []*tunedv1.Tuned) []tunedv1.TunedRecommend {
	var recommendAll []tunedv1.TunedRecommend

	for _, tuned := range tunedSlice {
		if tuned.Spec.Recommend != nil {
			recommendAll = append(recommendAll, tuned.Spec.Recommend...)
		}
	}

	sort.Slice(recommendAll, func(i, j int) bool {
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
		return podLabels != nil && len(podLabels) > 0
	}

	// Fetch old labels for Pod podNsName, not found on any other Pod that lives on the same Node
	oldPodLabelsUnique := podLabelsUnique(podLabelsNodeWide, podNsName, podLabelsNodeWide[podNsName])
	// Fetch current labels for Pod podNsName, not found on any other Pod that lives on the same Node
	curPodLabelsUnique := podLabelsUnique(podLabelsNodeWide, podNsName, podLabels)
	// If there is a difference between old and current unique Pod labels, a unique Pod label was
	// added/removed or both
	change := !mapOfStringsEqual(oldPodLabelsUnique, curPodLabelsUnique)

	return change
}

// mapOfStringsCopy returns a copy of a map of strings 'a'
func mapOfStringsCopy(a map[string]string) map[string]string {
	b := map[string]string{}

	for k, v := range a {
		b[k] = v
	}

	return b
}

// mapOfStringsEqual returns true if maps of strings 'a' and 'b' are equal.
// reflect.DeepEqual is roughly 10x slower than this
func mapOfStringsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if w, ok := b[k]; !ok || v != w {
			return false
		}
	}

	return true
}
