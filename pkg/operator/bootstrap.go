package operator

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	ntomf "github.com/openshift/cluster-node-tuning-operator/pkg/manifests"
)

// This function creates the initial configuration for the Node Tuning Operator.
func (c *Controller) Bootstrap() error {
	cr, err := c.listers.TunedResources.Get(tunedv1.TunedDefaultResourceName)
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get %q Tuned resource: %s", tunedv1.TunedDefaultResourceName, err)
	}

	// If the initial Tuned resource already exists,
	// no bootstrapping is required
	if cr != nil {
		return nil
	}

	// If no registry resource exists,
	// let's create one with sane defaults
	klog.Infof("creating default Tuned custom resource")
	cr = ntomf.TunedCustomResource()

	_, err = c.clients.Tuned.TunedV1().Tuneds(ntoconfig.OperatorNamespace()).Create(cr)
	if err != nil {
		return err
	}

	return nil
}
