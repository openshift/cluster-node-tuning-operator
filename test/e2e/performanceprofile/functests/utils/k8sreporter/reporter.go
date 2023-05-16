package k8sreporter

import (
	"log"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"

	"github.com/openshift-kni/k8sreporter"
	"k8s.io/apimachinery/pkg/runtime"
)

// New creates a specific reporter for the cluster-node-tuning-operator
func New(reportPath string) *k8sreporter.KubernetesReporter {
	addToScheme := func(s *runtime.Scheme) error {
		err := performancev2.SchemeBuilder.AddToScheme(s)
		if err != nil {
			return err
		}

		return nil
	}

	dumpNamespace := func(ns string) bool {
		return ns == testutils.NamespaceTesting
	}

	crds := []k8sreporter.CRData{
		{Cr: &performancev2.PerformanceProfileList{}},
	}

	reporter, err := k8sreporter.New("", addToScheme, dumpNamespace, reportPath, crds...)
	if err != nil {
		log.Fatalf("Failed to initialize the reporter %s", err)
	}

	return reporter
}
