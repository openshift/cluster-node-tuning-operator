package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiconfigv1 "github.com/openshift/api/config/v1"
	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
)

func mergeMaps(src map[string]string, dst map[string]string) {
	for k, v := range src {
		// NOTE: it will override destination values
		dst[k] = v
	}
}

// TODO: we should merge all create, get and delete methods

func GetMachineConfig(ctx context.Context, cli client.Client, name string) (*mcov1.MachineConfig, error) {
	mc := &mcov1.MachineConfig{}
	key := types.NamespacedName{
		Name:      name,
		Namespace: metav1.NamespaceNone,
	}
	if err := cli.Get(ctx, key, mc); err != nil {
		return nil, err
	}
	return mc, nil
}

func GetMutatedMachineConfig(ctx context.Context, cli client.Client, mc *mcov1.MachineConfig) (*mcov1.MachineConfig, error) {
	existing, err := GetMachineConfig(ctx, cli, mc.Name)
	if errors.IsNotFound(err) {
		return mc, nil
	}

	if err != nil {
		return nil, err
	}

	mutated := existing.DeepCopy()
	mergeMaps(mc.Annotations, mutated.Annotations)
	mergeMaps(mc.Labels, mutated.Labels)
	mutated.Spec = mc.Spec

	// we do not need to update if it no change between mutated and existing object
	if reflect.DeepEqual(existing.Spec, mutated.Spec) &&
		apiequality.Semantic.DeepEqual(existing.Labels, mutated.Labels) &&
		apiequality.Semantic.DeepEqual(existing.Annotations, mutated.Annotations) {
		return nil, nil
	}

	return mutated, nil
}

func GetClusterOperator(ctx context.Context, cli client.Client) (*apiconfigv1.ClusterOperator, error) {
	co := &apiconfigv1.ClusterOperator{}
	key := types.NamespacedName{
		Name:      "node-tuning",
		Namespace: metav1.NamespaceNone,
	}
	if err := cli.Get(ctx, key, co); err != nil {
		return nil, err
	}
	return co, nil
}

func CreateOrUpdateMachineConfig(ctx context.Context, cli client.Client, mc *mcov1.MachineConfig) error {
	_, err := GetMachineConfig(ctx, cli, mc.Name)
	if errors.IsNotFound(err) {
		klog.V(1).Infof("Create machine-config %q", mc.Name)
		if err := cli.Create(ctx, mc); err != nil {
			return err
		}
		return nil
	}

	if err != nil {
		return err
	}

	klog.V(2).Infof("Update machine-config %q", mc.Name)
	return cli.Update(ctx, mc)
}

func DeleteMachineConfig(ctx context.Context, cli client.Client, name string) error {
	mc, err := GetMachineConfig(ctx, cli, name)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return cli.Delete(ctx, mc)
}

func GetKubeletConfig(ctx context.Context, cli client.Client, name string) (*mcov1.KubeletConfig, error) {
	kc := &mcov1.KubeletConfig{}
	key := types.NamespacedName{
		Name:      name,
		Namespace: metav1.NamespaceNone,
	}
	if err := cli.Get(ctx, key, kc); err != nil {
		return nil, err
	}
	return kc, nil
}

func GetMutatedKubeletConfig(ctx context.Context, cli client.Client, kc *mcov1.KubeletConfig) (*mcov1.KubeletConfig, error) {
	existing, err := GetKubeletConfig(ctx, cli, kc.Name)
	if errors.IsNotFound(err) {
		return kc, nil
	}

	if err != nil {
		return nil, err
	}

	mutated := existing.DeepCopy()
	mergeMaps(kc.Annotations, mutated.Annotations)
	mergeMaps(kc.Labels, mutated.Labels)
	mutated.Spec = kc.Spec

	existingKubeletConfig := &kubeletconfigv1beta1.KubeletConfiguration{}
	err = json.Unmarshal(existing.Spec.KubeletConfig.Raw, existingKubeletConfig)
	if err != nil {
		return nil, err
	}

	mutatedKubeletConfig := &kubeletconfigv1beta1.KubeletConfiguration{}
	err = json.Unmarshal(mutated.Spec.KubeletConfig.Raw, mutatedKubeletConfig)
	if err != nil {
		return nil, err
	}

	// we do not need to update if it no change between mutated and existing object
	if apiequality.Semantic.DeepEqual(existingKubeletConfig, mutatedKubeletConfig) &&
		apiequality.Semantic.DeepEqual(existing.Spec.MachineConfigPoolSelector, mutated.Spec.MachineConfigPoolSelector) &&
		apiequality.Semantic.DeepEqual(existing.Labels, mutated.Labels) &&
		apiequality.Semantic.DeepEqual(existing.Annotations, mutated.Annotations) {
		return nil, nil
	}

	return mutated, nil
}

func CreateOrUpdateKubeletConfig(ctx context.Context, cli client.Client, kc *mcov1.KubeletConfig) error {
	_, err := GetKubeletConfig(ctx, cli, kc.Name)
	if errors.IsNotFound(err) {
		klog.V(1).Infof("Create kubelet-config %q", kc.Name)
		if err := cli.Create(ctx, kc); err != nil {
			return err
		}
		return nil
	}

	if err != nil {
		return err
	}

	klog.V(2).Infof("Update kubelet-config %q", kc.Name)
	return cli.Update(ctx, kc)
}

func DeleteKubeletConfig(ctx context.Context, cli client.Client, name string) error {
	kc, err := GetKubeletConfig(ctx, cli, name)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return cli.Delete(ctx, kc)
}

func GetTuned(ctx context.Context, cli client.Client, name string, namespace string) (*tunedv1.Tuned, error) {
	tuned := &tunedv1.Tuned{}
	key := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}
	if err := cli.Get(ctx, key, tuned); err != nil {
		return nil, err
	}
	return tuned, nil
}

func GetMutatedTuned(ctx context.Context, cli client.Client, tuned *tunedv1.Tuned) (*tunedv1.Tuned, error) {
	existing, err := GetTuned(ctx, cli, tuned.Name, tuned.Namespace)
	if errors.IsNotFound(err) {
		return tuned, nil
	}

	if err != nil {
		return nil, err
	}

	mutated := existing.DeepCopy()
	mergeMaps(tuned.Annotations, mutated.Annotations)
	mergeMaps(tuned.Labels, mutated.Labels)
	mutated.Spec = tuned.Spec

	// we do not need to update if it no change between mutated and existing object
	if apiequality.Semantic.DeepEqual(existing.Spec, mutated.Spec) &&
		apiequality.Semantic.DeepEqual(existing.Labels, mutated.Labels) &&
		apiequality.Semantic.DeepEqual(existing.Annotations, mutated.Annotations) {
		return nil, nil
	}

	return mutated, nil
}

func CreateOrUpdateTuned(ctx context.Context, cli client.Client, tuned *tunedv1.Tuned, profileName string) error {
	if err := RemoveOutdatedTuned(ctx, cli, tuned, profileName); err != nil {
		return err
	}

	_, err := GetTuned(ctx, cli, tuned.Name, tuned.Namespace)
	if errors.IsNotFound(err) {
		klog.V(1).Infof("Create tuned %q in the namespace %q", tuned.Name, tuned.Namespace)
		if err := cli.Create(ctx, tuned); err != nil {
			return err
		}
		return nil
	}

	if err != nil {
		return err
	}

	klog.V(2).Infof("Update tuned %q in the namespace %q", tuned.Name, tuned.Namespace)
	return cli.Update(ctx, tuned)
}

func RemoveOutdatedTuned(ctx context.Context, cli client.Client, tuned *tunedv1.Tuned, profileName string) error {
	tunedList := &tunedv1.TunedList{}
	if err := cli.List(ctx, tunedList); err != nil {
		klog.Errorf("Unable to list tuned objects for outdated removal procedure: %v", err)
		return err
	}

	for t := range tunedList.Items {
		tunedItem := tunedList.Items[t]
		ownerReferences := tunedItem.OwnerReferences
		for o := range ownerReferences {
			if ownerReferences[o].Name == profileName && tunedItem.Name != tuned.Name {
				if err := DeleteTuned(ctx, cli, tunedItem.Name, tunedItem.Namespace); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func DeleteTuned(ctx context.Context, cli client.Client, name string, namespace string) error {
	tuned, err := GetTuned(ctx, cli, name, namespace)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return cli.Delete(ctx, tuned)
}

func GetRuntimeClass(ctx context.Context, cli client.Client, name string) (*nodev1.RuntimeClass, error) {
	runtimeClass := &nodev1.RuntimeClass{}
	key := types.NamespacedName{
		Name: name,
	}
	if err := cli.Get(ctx, key, runtimeClass); err != nil {
		return nil, err
	}
	return runtimeClass, nil
}

func GetMutatedRuntimeClass(ctx context.Context, cli client.Client, runtimeClass *nodev1.RuntimeClass) (*nodev1.RuntimeClass, error) {
	existing, err := GetRuntimeClass(ctx, cli, runtimeClass.Name)
	if errors.IsNotFound(err) {
		return runtimeClass, nil
	}

	if err != nil {
		return nil, err
	}

	mutated := existing.DeepCopy()
	mergeMaps(runtimeClass.Annotations, mutated.Annotations)
	mergeMaps(runtimeClass.Labels, mutated.Labels)
	mutated.Handler = runtimeClass.Handler
	mutated.Scheduling = runtimeClass.Scheduling

	// we do not need to update if it no change between mutated and existing object
	if apiequality.Semantic.DeepEqual(existing.Handler, mutated.Handler) &&
		apiequality.Semantic.DeepEqual(existing.Scheduling, mutated.Scheduling) &&
		apiequality.Semantic.DeepEqual(existing.Labels, mutated.Labels) &&
		apiequality.Semantic.DeepEqual(existing.Annotations, mutated.Annotations) {
		return nil, nil
	}

	return mutated, nil
}

func CreateOrUpdateRuntimeClass(ctx context.Context, cli client.Client, runtimeClass *nodev1.RuntimeClass) error {
	_, err := GetRuntimeClass(ctx, cli, runtimeClass.Name)
	if errors.IsNotFound(err) {
		klog.V(1).Infof("Create runtime class %q", runtimeClass.Name)
		if err := cli.Create(ctx, runtimeClass); err != nil {
			return err
		}
		return nil
	}

	if err != nil {
		return err
	}

	klog.V(2).Infof("Update runtime class %q", runtimeClass.Name)
	return cli.Update(ctx, runtimeClass)
}

func DeleteRuntimeClass(ctx context.Context, cli client.Client, name string) error {
	runtimeClass, err := GetRuntimeClass(ctx, cli, name)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return cli.Delete(ctx, runtimeClass)
}

func GetMachineConfigPoolByProfile(ctx context.Context, client client.Client, profile *performancev2.PerformanceProfile) (*mcov1.MachineConfigPool, error) {
	nodeSelector := labels.Set(profile.Spec.NodeSelector)

	mcpList := &mcov1.MachineConfigPoolList{}
	if err := client.List(ctx, mcpList); err != nil {
		return nil, err
	}

	filteredMCPList := filterMCPDuplications(mcpList.Items)

	var profileMCPs []*mcov1.MachineConfigPool
	for i := range filteredMCPList {
		mcp := &mcpList.Items[i]

		if mcp.Spec.NodeSelector == nil {
			continue
		}

		mcpNodeSelector, err := metav1.LabelSelectorAsSelector(mcp.Spec.NodeSelector)
		if err != nil {
			return nil, err
		}

		if mcpNodeSelector.Matches(nodeSelector) {
			profileMCPs = append(profileMCPs, mcp)
		}
	}

	if len(profileMCPs) == 0 {
		return nil, fmt.Errorf("failed to find MCP with the node selector that matches labels %q", nodeSelector.String())
	}

	if len(profileMCPs) > 1 {
		return nil, fmt.Errorf("more than one MCP found that matches performance profile node selector %q", nodeSelector.String())
	}

	return profileMCPs[0], nil
}

func filterMCPDuplications(mcps []mcov1.MachineConfigPool) []mcov1.MachineConfigPool {
	var filtered []mcov1.MachineConfigPool
	items := map[string]mcov1.MachineConfigPool{}
	for _, mcp := range mcps {
		if _, exists := items[mcp.Name]; !exists {
			items[mcp.Name] = mcp
			filtered = append(filtered, mcp)
		}
	}

	return filtered
}

// GetNodesForProfile returns the list of nodes that match the performance profile's node selector.
func GetNodesForProfile(ctx context.Context, cli client.Client, profile *performancev2.PerformanceProfile) ([]corev1.Node, error) {
	nodeList := &corev1.NodeList{}
	if err := cli.List(ctx, nodeList); err != nil {
		return nil, err
	}

	profileNodeSelector := labels.Set(profile.Spec.NodeSelector)
	var matchedNodes []corev1.Node

	for _, node := range nodeList.Items {
		nodeLabels := labels.Set(node.Labels)
		if profileNodeSelector.AsSelector().Matches(nodeLabels) {
			matchedNodes = append(matchedNodes, node)
		}
	}

	return matchedNodes, nil
}

// CheckNodesBootcmdlineReady checks if all nodes have bootcmdline annotations set and if they all agree on the bootcmdline.
// Returns: (bootcmdline string, allNodesReady bool, nodesAgree bool).
func CheckNodesBootcmdlineReady(nodes []corev1.Node) (string, bool, bool) {
	if len(nodes) == 0 {
		return "", true, true
	}

	var bootcmdline string
	allSet := true
	allAgree := true

	for i, node := range nodes {
		if node.Annotations == nil {
			allSet = false
			continue
		}

		bootcmdlineAnnotVal, bootcmdlineAnnotSet := node.Annotations[tunedv1.TunedBootcmdlineAnnotationKey]
		if !bootcmdlineAnnotSet {
			allSet = false
			continue
		}

		if i == 0 {
			bootcmdline = bootcmdlineAnnotVal
		} else if bootcmdline != bootcmdlineAnnotVal {
			allAgree = false
		}
	}

	return bootcmdline, allSet, allAgree
}

// GetKernelArgumentsFromTunedBootcmdline waits for all nodes to have bootcmdline annotations set by tuned,
// then returns the parsed kernel arguments. Returns an error if bootcmdline is not ready or nodes disagree.
func GetKernelArgumentsFromTunedBootcmdline(ctx context.Context, cli client.Client, profile *performancev2.PerformanceProfile) ([]string, error) {
	// Get nodes for this performance profile
	nodes, err := GetNodesForProfile(ctx, cli, profile)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes for performance profile %q: %v", profile.Name, err)
	}

	// Check if all nodes have bootcmdline annotations set by tuned
	bootcmdline, allNodesReady, nodesAgree := CheckNodesBootcmdlineReady(nodes)

	if !allNodesReady {
		klog.V(2).Infof("bootcmdline for profile %s not cached for all nodes, MachineConfig creation deferred", profile.Name)
		return nil, fmt.Errorf("bootcmdline not ready for all nodes of profile %s", profile.Name)
	}

	if !nodesAgree {
		// This is a configuration issue - nodes disagree on bootcmdline.
		klog.Errorf("not all %d nodes for profile %q agree on bootcmdline: %s", len(nodes), profile.Name, bootcmdline)
		return nil, fmt.Errorf("not all nodes for profile %q agree on bootcmdline", profile.Name)
	}

	// Split bootcmdline into kernel arguments
	kernelArguments := util.SplitKernelArguments(bootcmdline)
	klog.V(2).Infof("bootcmdline for profile %s: %s (parsed to %d kernel arguments)", profile.Name, bootcmdline, len(kernelArguments))

	return kernelArguments, nil
}
