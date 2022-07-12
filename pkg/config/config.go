package config

import (
	"os"
	"strconv"
	"time"

	"k8s.io/klog/v2"
)

const (
	nodeTunedImageDefault    string = "registry.svc.ci.openshift.org/openshift/origin-v4.0:cluster-node-tuned"
	operatorNamespaceDefault string = "openshift-cluster-node-tuning-operator"
	resyncPeriodDefault      int64  = 600

	OperatorLockName string = "node-tuning-operator-lock"
)

// NodeTunedImage returns the operator's operand/tuned image path.
func NodeTunedImage() string {
	nodeTunedImage := os.Getenv("CLUSTER_NODE_TUNED_IMAGE")

	if len(nodeTunedImage) > 0 {
		return nodeTunedImage
	}

	return nodeTunedImageDefault
}

// WatchNamespace returns the namespace that the Tuned and Profile CRs are in.
func WatchNamespace() string {
	operatorNamespace := os.Getenv("WATCH_NAMESPACE")

	if len(operatorNamespace) > 0 {
		return operatorNamespace
	}

	return operatorNamespaceDefault
}

// OperatorNamespace returns the namespace that the operator is running in
func OperatorNamespace() string {
	operatorNamespace := os.Getenv("MY_NAMESPACE")

	if len(operatorNamespace) > 0 {
		return operatorNamespace
	}

	return operatorNamespaceDefault
}

// InHyperShift returns a boolean which is True when NTO is used to manage tuning
// of hosted cluster nodes.
func InHyperShift() bool {
	hypershiftEnv := os.Getenv("HYPERSHIFT")
	return hypershiftEnv == "true"
}

// ResyncPeriod returns the configured or default Reconcile period.
func ResyncPeriod() time.Duration {
	resyncPeriodDuration := resyncPeriodDefault
	resyncPeriodEnv := os.Getenv("RESYNC_PERIOD")

	if len(resyncPeriodEnv) > 0 {
		var err error
		resyncPeriodDuration, err = strconv.ParseInt(resyncPeriodEnv, 10, 64)
		if err != nil {
			klog.Errorf("cannot parse RESYNC_PERIOD (%s), using %d", resyncPeriodEnv, resyncPeriodDefault)
			resyncPeriodDuration = resyncPeriodDefault
		}
	}
	return time.Second * time.Duration(resyncPeriodDuration)
}
