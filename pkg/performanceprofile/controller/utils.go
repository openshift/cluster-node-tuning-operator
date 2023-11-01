package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	olmoperators "github.com/operator-framework/api/pkg/operators/install"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func ListPerformanceOperatorCSVs(k8sclient client.Client, options []client.ListOption, paginationLimit uint64, performanceOperatorDeploymentName string) ([]olmv1alpha1.ClusterServiceVersion, error) {
	// Register OLM types to the client
	olmoperators.Install(k8sclient.Scheme())
	var performanceOperatorCSVs []olmv1alpha1.ClusterServiceVersion

	csvs := &olmv1alpha1.ClusterServiceVersionList{}

	opts := []client.ListOption{}
	copy(opts, options)
	opts = append(options, client.Limit(paginationLimit))

	for {
		if err := k8sclient.List(context.TODO(), csvs, opts...); err != nil {
			if !errors.IsNotFound(err) {
				return performanceOperatorCSVs, err
			}
		}
		continueToken := csvs.GetContinue()

		for i := range csvs.Items {
			csv := &csvs.Items[i]
			deploymentSpecs := csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs

			for _, deployment := range deploymentSpecs {
				if deployment.Name == performanceOperatorDeploymentName {
					performanceOperatorCSVs = append(performanceOperatorCSVs, *csv)
					break
				}
			}

		}
		if continueToken == "" {
			// empty token means there is no more elements
			break
		}
		// add new continuation token and keep looking
		opts = append(opts, client.Continue(continueToken))
	}
	return performanceOperatorCSVs, nil
}
