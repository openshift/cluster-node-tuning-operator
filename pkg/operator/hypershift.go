package operator

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
)

const (
	hypershiftNodeOwnerNameLabel          = "cluster.x-k8s.io/owner-name"
	hypershiftNodeOwnerKindLabel          = "cluster.x-k8s.io/owner-kind"
	hypershiftNodePoolNamespacedNameLabel = "hypershift.openshift.io/nodePool"
	hypershiftNodePoolNameLabel           = "hypershift.openshift.io/nodePoolName"
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
				newTuned, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.WatchNamespace()).Update(context.TODO(), newTuned, metav1.UpdateOptions{})
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
			newTuned, err := c.clients.Tuned.TunedV1().Tuneds(ntoconfig.WatchNamespace()).Create(context.TODO(), newTuned, metav1.CreateOptions{})
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
		if tunedName != tunedv1.TunedDefaultResourceName && tunedName != tunedv1.TunedRenderedResourceName {
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
		tunedConfig, ok := cm.Data[tunedConfigMapConfigKey]
		if !ok {
			klog.Warning("Tuned in ConfigMap %s has no data for field %s", cm.ObjectMeta.Name, tunedConfigMapConfigKey)
			continue
		}

		cmNodePoolNamespacedName, ok := cm.Annotations[hypershiftNodePoolNamespacedNameLabel]
		if !ok {
			klog.Warningf("failed to parseTunedManifests in ConfigMap %s, no label %s", cm.ObjectMeta.Name, hypershiftNodePoolNamespacedNameLabel)
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

func hashStruct(o interface{}) string {
	hash := fnv.New32a()
	hash.Write([]byte(fmt.Sprintf("%v", o)))
	intHash := hash.Sum32()
	return fmt.Sprintf("%08x", intHash)
}

// parseNamespacedName expects a string with the format "namespace/name"
// and returns the name only.
// If given a string in the format "name" returns "name".
func parseNamespacedName(nodePoolFullName string) string {
	parts := strings.SplitN(nodePoolFullName, "/", 2)
	if len(parts) > 1 {
		return parts[1]
	}
	return parts[0]
}
