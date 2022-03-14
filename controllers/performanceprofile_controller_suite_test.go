package controllers

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	performancev2 "github.com/openshift-kni/performance-addon-operators/api/v2"
	configv1 "github.com/openshift/api/config/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	"k8s.io/client-go/kubernetes/scheme"
)

func TestPerformanceProfile(t *testing.T) {
	RegisterFailHandler(Fail)

	// add resources API to default scheme
	performancev2.AddToScheme(scheme.Scheme)
	configv1.AddToScheme(scheme.Scheme)
	mcov1.AddToScheme(scheme.Scheme)
	tunedv1.AddToScheme(scheme.Scheme)

	RunSpecs(t, "Performance Profile Suite")
}
