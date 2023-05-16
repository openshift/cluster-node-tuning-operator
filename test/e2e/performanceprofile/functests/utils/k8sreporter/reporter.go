package k8sreporter

import (
	"log"

	performancev1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1"
	performancev1alpha1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1alpha1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	"github.com/openshift-kni/k8sreporter"
	"k8s.io/apimachinery/pkg/runtime"
)

// New creates a specific reporter for cluster-node-tuning-operator
func New(kubeconfig, path, namespace string) *k8sreporter.KubernetesReporter {
	addToScheme := func(s *runtime.Scheme) error {
		err := performancev1.AddToScheme(s)
		if err != nil {
			return err
		}
		err = performancev1alpha1.AddToScheme(s)
		if err != nil {
			return err
		}
		err = performancev2.AddToScheme(s)
		if err != nil {
			return err
		}
		err = mcov1.AddToScheme(s)
		if err != nil {
			return err
		}
		return nil
	}

	dumpNamespace := func(ns string) bool {
		return ns == namespace
	}

	crds := []k8sreporter.CRData{
		{Cr: &mcov1.MachineConfigPoolList{}},
		{Cr: &mcov1.MachineConfigList{}},
		{Cr: &performancev1.PerformanceProfileList{}},
		{Cr: &performancev1alpha1.PerformanceProfileList{}},
		{Cr: &performancev2.PerformanceProfileList{}},
	}

	reporter, err := k8sreporter.New(kubeconfig, addToScheme, dumpNamespace, path, crds...)
	if err != nil {
		log.Fatalf("Failed to initialize the reporter %s", err)
	}

	return reporter
}
