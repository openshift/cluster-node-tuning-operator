package operator

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"reflect"
	"strings"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/version"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"
)

const (
	hypershiftNodeOwnerNameLabel = "cluster.x-k8s.io/owner-name"
	hypershiftNodeOwnerKindLabel = "cluster.x-k8s.io/owner-kind"
	HypershiftNodePoolLabel      = "hypershift.openshift.io/nodePool"
	hypershiftNodePoolNameLabel  = "hypershift.openshift.io/nodePoolName"

	tunedConfigMapLabel      = "hypershift.openshift.io/tuned-config"
	tuningConfigMapConfigKey = "tuning"
	// TODO remove once HyperShift has switched to using new key.
	tunedConfigMapConfigKeyDeprecated = "tuned"

	operatorGeneratedMachineConfig            = "hypershift.openshift.io/nto-generated-machine-config"
	NtoGeneratedPerformanceProfileStatusLabel = "hypershift.openshift.io/nto-generated-performance-profile-status"
	mcConfigMapDataKey                        = "config"
	PPStatusConfigMapConfigKey                = "status"
	generatedConfigMapPrefix                  = "nto-mc-"
)

// syncHostedClusterTuneds synchronizes Tuned objects embedded in ConfigMaps
// in management's cluster hosted namespace with Tuned objects in the hosted
// cluster.  Returns non-nil error only when retry/resync is needed.
func (c *Controller) syncHostedClusterTuneds() error {
	cmTuneds, err := c.getObjFromTunedConfigMap()
	if err != nil {
		return err
	}

	hcTunedList, err := c.clients.Tuned.TunedV1().Tuneds(ntoconfig.WatchNamespace()).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Tuneds: %v", err)
	}
	hcTuneds := hcTunedList.Items
	hcTunedMap := tunedMapFromList(hcTuneds)
	cmTunedMap := tunedMapFromList(cmTuneds)

	for tunedName, cmTuned := range cmTunedMap {
		if hcTuned, ok := hcTunedMap[tunedName]; ok {
			klog.V(1).Infof("hosted cluster already contains Tuned %v from ConfigMap", tunedName)
			if reflect.DeepEqual(cmTuned.Spec.Profile, hcTuned.Spec.Profile) &&
				reflect.DeepEqual(cmTuned.Spec.Recommend, hcTuned.Spec.Recommend) &&
				reflect.DeepEqual(cmTuned.ObjectMeta.Labels, hcTuned.ObjectMeta.Labels) {
				klog.V(2).Infof("hosted cluster version of Tuned %v matches the ConfigMap %s config", tunedName, cmTuned.ObjectMeta.Name)
			} else {
				// This Tuned exists in the hosted cluster but is out-of-sync with the management configuration
				newTuned := hcTuned.DeepCopy() // never update the objects from cache
				newTuned.Spec.Profile = cmTuned.Spec.Profile
				newTuned.Spec.Recommend = cmTuned.Spec.Recommend

				klog.V(2).Infof("updating Tuned %v from ConfigMap %s", tunedName, cmTuned.ObjectMeta.Name)
				_, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.WatchNamespace()).Update(context.TODO(), newTuned, metav1.UpdateOptions{})
				if err != nil {
					if errors.IsInvalid(err) {
						// The provided resource is not valid.  Log an error and do not try to resync.
						klog.Errorf("failed to update Tuned due to invalid resource: %v", err)
					} else {
						// Failure to update Tuned, queue another sync().
						return fmt.Errorf("failed to update Tuned %s: %v", tunedName, err)
					}
				}
			}
			delete(hcTunedMap, tunedName)
			delete(cmTunedMap, tunedName)
		} else {
			klog.V(1).Infof("need to create Tuned %v based on ConfigMap %s", cmTuned.ObjectMeta.Name, tunedName)
			// Create the Tuned in the hosted cluster from the config in ConfigMap
			newTuned := cmTuned.DeepCopy()
			_, err := c.clients.Tuned.TunedV1().Tuneds(ntoconfig.WatchNamespace()).Create(context.TODO(), newTuned, metav1.CreateOptions{})
			if err != nil {
				if errors.IsInvalid(err) {
					// The provided resource is not valid.  Log an error and do not try to resync.
					klog.Errorf("failed to create Tuned due to invalid resource: %v", err)
				} else {
					// Failure to create Tuned, queue another sync().
					return fmt.Errorf("failed to create Tuned %s: %v", tunedName, err)
				}
			}
			delete(cmTunedMap, tunedName)
		}
	}
	// Anything left in hcMap should be deleted
	for tunedName := range hcTunedMap {
		if tunedName != tunedv1.TunedDefaultResourceName {
			klog.V(1).Infof("deleting stale Tuned %s in hosted cluster", tunedName)
			err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.WatchNamespace()).Delete(context.TODO(), tunedName, metav1.DeleteOptions{})
			if err != nil {
				return fmt.Errorf("failed to delete Tuned %s: %v", tunedName, err)
			}
			klog.Infof("deleted Tuned %s", tunedName)
		}
	}
	return nil
}

// getObjFromTunedConfigMap retrieves all ConfigMaps with embedded Tuned objects
// from management's cluster hosted namespace and returns a slice of the
// retrieved Tuned objects.  Duplicate Tuned objects are ignored.  Returns non-nil
// error only when retry is needed.
func (c *Controller) getObjFromTunedConfigMap() ([]tunedv1.Tuned, error) {
	var cmTuneds []tunedv1.Tuned

	cmListOptions := metav1.ListOptions{
		LabelSelector: tunedConfigMapLabel + "=true",
	}

	cmList, err := c.clients.ManagementKube.CoreV1().ConfigMaps(ntoconfig.OperatorNamespace()).List(context.TODO(), cmListOptions)
	if err != nil {
		return cmTuneds, fmt.Errorf("error listing ConfigMaps in namespace %s: %v", ntoconfig.OperatorNamespace(), err)
	}

	seenTunedObject := map[string]bool{}
	for _, cm := range cmList.Items {
		tunedConfig, ok := cm.Data[tuningConfigMapConfigKey]
		if !ok {
			tunedConfig, ok = cm.Data[tunedConfigMapConfigKeyDeprecated]
			if !ok {
				klog.Warningf("ConfigMap %s has no data in field %s or %s (deprecated). Expected Tuned manifests.", cm.ObjectMeta.Name, tuningConfigMapConfigKey, tunedConfigMapConfigKeyDeprecated)
				continue
			} else {
				klog.Infof("Deprecated key %s used in ConfigMap %s", tunedConfigMapConfigKeyDeprecated, cm.ObjectMeta.Name)
			}
		}

		cmNodePoolNamespacedName, ok := cm.Annotations[HypershiftNodePoolLabel]
		if !ok {
			klog.Warningf("failed to parseTunedManifests in ConfigMap %s, no label %s", cm.ObjectMeta.Name, HypershiftNodePoolLabel)
			continue
		}
		nodePoolName := parseNamespacedName(cmNodePoolNamespacedName)

		tunedsFromConfigMap, err := parseTunedManifests([]byte(tunedConfig), nodePoolName)
		if err != nil {
			klog.Warningf("failed to parseTunedManifests in ConfigMap %s: %v", cm.ObjectMeta.Name, err)
			continue
		}

		tunedsFromConfigMapUnique := []tunedv1.Tuned{}
		for j, t := range tunedsFromConfigMap {
			tunedObjectName := tunedsFromConfigMap[j].ObjectMeta.Name
			if seenTunedObject[tunedObjectName] {
				klog.Warningf("ignoring duplicate Tuned Profile %s in ConfigMap %s", tunedObjectName, cm.ObjectMeta.Name)
				continue
			}
			seenTunedObject[tunedObjectName] = true
			tunedsFromConfigMapUnique = append(tunedsFromConfigMapUnique, t)
		}

		cmTuneds = append(cmTuneds, tunedsFromConfigMapUnique...)
	}

	return cmTuneds, nil
}

// getNodesForNodePool uses 'hypershiftNodePoolLabel' to return all Nodes which are in
// the NodePool 'nodePoolName'.
func (c *Controller) getNodesForNodePool(nodePoolName string) ([]*corev1.Node, error) {
	selector := labels.SelectorFromValidatedSet(
		map[string]string{
			HypershiftNodePoolLabel: nodePoolName,
		})
	nodes, err := c.pc.listers.Nodes.List(selector)
	if err != nil {
		return nodes, err
	}

	return nodes, err
}

// parseManifests parses a YAML or JSON document that may contain one or more
// kubernetes resources.
func parseTunedManifests(data []byte, nodePoolName string) ([]tunedv1.Tuned, error) {
	r := bytes.NewReader(data)
	d := yamlutil.NewYAMLOrJSONDecoder(r, 1024)
	var tuneds []tunedv1.Tuned
	for {
		t := tunedv1.Tuned{}
		if err := d.Decode(&t); err != nil {
			if err == io.EOF {
				return tuneds, nil
			}
			return tuneds, fmt.Errorf("error parsing Tuned manifests: %v", err)
		}
		// Make Tuned names unique if a Tuned is duplicated across NodePools
		// for example, if one ConfigMap is referenced in multiple NodePools
		t.SetName(t.ObjectMeta.Name + "-" + hashStruct(nodePoolName))
		klog.V(2).Infof("parseTunedManifests: name: %s", t.GetName())

		// Propagate NodePool name from ConfigMap down to Tuned object
		if t.Labels == nil {
			t.Labels = make(map[string]string)
		}
		t.Labels[hypershiftNodePoolNameLabel] = nodePoolName
		tuneds = append(tuneds, t)
	}
}

func mcConfigMapName(name string) string {
	return generatedConfigMapPrefix + name
}

func hashStruct(o interface{}) string {
	hash := fnv.New32a()
	hash.Write([]byte(fmt.Sprintf("%v", o)))
	intHash := hash.Sum32()
	return fmt.Sprintf("%08x", intHash)
}

// parseNamespacedName expects a string with the format "namespace/name"
// and returns the name only.
// If given a string in the format "name" returns "name".
func parseNamespacedName(namespacedName string) string {
	parts := strings.SplitN(namespacedName, "/", 2)
	if len(parts) > 1 {
		return parts[1]
	}
	return parts[0]
}

func (c *Controller) getMachineConfigFromConfigMap(config *corev1.ConfigMap) (*mcfgv1.MachineConfig, error) {
	YamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, c.scheme, c.scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)

	manifest := []byte(config.Data[mcConfigMapDataKey])
	cr, _, err := YamlSerializer.Decode(manifest, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("error decoding MachineConfig from ConfigMap: %s, %v", config.Name, err)
	}

	mcObj, ok := cr.(*mcfgv1.MachineConfig)
	if !ok {
		return nil, fmt.Errorf("unexpected type in ConfigMap: %T, must be MachineConfig", cr)
	}
	return mcObj, nil
}

func (c *Controller) newConfigMapForMachineConfig(configMapName string, nodePoolName string, mc *mcfgv1.MachineConfig) (*corev1.ConfigMap, error) {
	mcManifest, err := c.serializeMachineConfig(mc)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize ConfigMap for MachineConfig %s: %v", mc.Name, err)
	}

	ret := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: ntoconfig.OperatorNamespace(),
			Labels:    generatedConfigMapLabels(nodePoolName),
			Annotations: map[string]string{
				GeneratedByControllerVersionAnnotationKey: version.Version,
			},
		},
		Data: map[string]string{
			mcConfigMapDataKey: string(mcManifest),
		},
	}

	return ret, nil
}

func (c *Controller) serializeMachineConfig(mc *mcfgv1.MachineConfig) ([]byte, error) {
	YamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, c.scheme, c.scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
	buff := bytes.Buffer{}
	if err := YamlSerializer.Encode(mc, &buff); err != nil {
		return nil, fmt.Errorf("failed to encode ConfigMap for MachineConfig %s: %v", mc.Name, err)
	}
	return buff.Bytes(), nil
}

func generatedConfigMapLabels(nodePoolName string) map[string]string {
	return map[string]string{
		operatorGeneratedMachineConfig: "true",
		HypershiftNodePoolLabel:        nodePoolName,
	}
}

func generatedConfigMapAnnotations(nodePoolName string) map[string]string {
	return map[string]string{
		HypershiftNodePoolLabel: nodePoolName,
	}
}
