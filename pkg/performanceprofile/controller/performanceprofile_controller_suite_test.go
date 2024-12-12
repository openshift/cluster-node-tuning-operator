package controller

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

func TestPerformanceProfile(t *testing.T) {
	RegisterFailHandler(Fail)

	// add resources API to default scheme
	utilruntime.Must(performancev2.AddToScheme(scheme.Scheme))
	utilruntime.Must(configv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(mcov1.AddToScheme(scheme.Scheme))
	utilruntime.Must(tunedv1.AddToScheme(scheme.Scheme))

	RunSpecs(t, "Performance Profile Suite")
}
