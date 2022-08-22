package operator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
)

// TODO remove unnecessary debugging log lines and describe what this function does.
func (c *Controller) syncHostedClusterTuneds() error {
	cmTuneds, err := c.getObjFromTunedConfigMap()
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
				reflect.DeepEqual(cmTuned.Spec.Recommend, hcTuned.Spec.Recommend) {
				klog.V(2).Infof("Hosted cluster version of Tuned %v matches the ConfigMap %s config", tunedName, cmTuned.ObjectMeta.Name)
			} else {
				// This Tuned exists in the hosted cluster but is out-of-sync with the management configuration
				newTuned := hcTuned.DeepCopy() // never update the objects from cache
				newTuned.Spec.Profile = cmTuned.Spec.Profile
				newTuned.Spec.Recommend = cmTuned.Spec.Recommend

				klog.V(2).Infof("Updating Tuned %v from ConfigMap %s", tunedName, cmTuned.ObjectMeta.Name)
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
			klog.V(1).Infof("Need to create Tuned %v based on ConfigMap %s", cmTuned.ObjectMeta.Name, tunedName)
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
		if tunedName != "default" && tunedName != "rendered" {
			klog.V(1).Infof("found Tuned in HostedCluster named %s. Deleting.", tunedName)
			err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.WatchNamespace()).Delete(context.TODO(), tunedName, metav1.DeleteOptions{})
			if err != nil {
				return fmt.Errorf("failed to delete Tuned %s: %v", tunedName, err)
			}
			klog.Infof("deleted Tuned %s", tunedName)
		}
	}
	return nil
}

// TODO: describe what this function does.
func (c *Controller) getObjFromTunedConfigMap() ([]tunedv1.Tuned, error) {
	var cmTuneds []tunedv1.Tuned

	cmListOptions := metav1.ListOptions{
		LabelSelector: tunedConfigMapAnnotation + "=true",
	}

	cmList, err := c.clients.ManagementKube.CoreV1().ConfigMaps(ntoconfig.OperatorNamespace()).List(context.TODO(), cmListOptions)
	if err != nil {
		return cmTuneds, fmt.Errorf("error listing ConfigMaps in namespace %s: %v", ntoconfig.OperatorNamespace(), err)
	}

	// TODO test cluster upgrades.
	seenTunedObject := map[string]bool{}
	for _, cm := range cmList.Items {
		tunedConfig, ok := cm.Data[tunedConfigMapConfigKey]
		if !ok {
			klog.Warning("Tuned config in ConfigMap %s has no data for field %s", cm.ObjectMeta.Name, tunedConfigMapConfigKey)
			continue
		}

		tunedsFromConfigMap, err := parseTunedManifests([]byte(tunedConfig))
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

		// TODO remove these log lines
		for _, t := range cmTuneds {
			klog.Infof("got Tuned %v from ConfigMap: %v", t.Name, cm.Name)
		}
		//klog.V(1).Infof("TunedConfig from ConfigMap: %v", string(tunedConfig[:]))
	}

	return cmTuneds, nil
}

// parseManifests parses a YAML or JSON document that may contain one or more
// kubernetes resources.
func parseTunedManifests(data []byte) ([]tunedv1.Tuned, error) {
	r := bytes.NewReader(data)
	d := yamlutil.NewYAMLOrJSONDecoder(r, 1024)
	var tuneds []tunedv1.Tuned
	for {
		t := tunedv1.Tuned{}
		if err := d.Decode(&t); err != nil {
			if err == io.EOF {
				klog.Infof("parseTunedManifests: EOF reached, num tuneds: %d", len(tuneds)) // TODO: do we want this log/verbosity here?
				return tuneds, nil
			}
			return tuneds, fmt.Errorf("error parsing Tuned manifests: %v", err)
		}
		klog.Infof("parseTunedManifests: name: %s", t.GetName()) // TODO: do we want this log/verbosity here?

		tuneds = append(tuneds, t)
	}
}
