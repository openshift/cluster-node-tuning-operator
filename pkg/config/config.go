package config

import (
	"os"
	"strconv"

	"github.com/golang/glog"
)

const (
	nodeTunedImageDefault    string = "registry.svc.ci.openshift.org/openshift/origin-v4.0:cluster-node-tuned"
	operatorNameDefault      string = "node-tuning"
	operatorNamespaceDefault string = "openshift-cluster-node-tuning-operator"
	resyncPeriodDefault      int64  = 600
)

// NodeTunedImage returns the operator's operand/tuned image path.
func NodeTunedImage() string {
	nodeTunedImage := os.Getenv("CLUSTER_NODE_TUNED_IMAGE")

	if len(nodeTunedImage) > 0 {
		return nodeTunedImage
	}

	return nodeTunedImageDefault
}

// OperatorName returns the operator name.
func OperatorName() string {
	operatorName := os.Getenv("OPERATOR_NAME")

	if len(operatorName) > 0 {
		return operatorName
	}

	return operatorNameDefault
}

// OperatorName returns the operator namespace.
func OperatorNamespace() string {
	operatorNamespace := os.Getenv("WATCH_NAMESPACE")

	if len(operatorNamespace) > 0 {
		return operatorNamespace
	}

	return operatorNamespaceDefault
}

// ResyncPeriod returns the configured or default Reconcile period.
func ResyncPeriod() int64 {
	resyncPeriodDuration := resyncPeriodDefault
	resyncPeriodEnv := os.Getenv("RESYNC_PERIOD")

	if len(resyncPeriodEnv) > 0 {
		var err error
		resyncPeriodDuration, err = strconv.ParseInt(resyncPeriodEnv, 10, 64)
		if err != nil {
			glog.Errorf("Cannot parse RESYNC_PERIOD (%s), using %d", resyncPeriodEnv, resyncPeriodDefault)
			resyncPeriodDuration = resyncPeriodDefault
		}
	}
	return resyncPeriodDuration
}
