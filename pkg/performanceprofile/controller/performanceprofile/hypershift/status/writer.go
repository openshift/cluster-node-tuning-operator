package status

import (
	"context"
	"k8s.io/klog/v2"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/status"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
)

var _ status.Writer = &writer{}

type writer struct {
	controlPlaneClient client.Client
	dataPlaneClient    client.Client
}

func NewWriter(controlPlaneClient client.Client, dataPlaneClient client.Client) status.Writer {
	return &writer{controlPlaneClient: controlPlaneClient, dataPlaneClient: dataPlaneClient}
}

func (w writer) Update(ctx context.Context, object client.Object, conditions []conditionsv1.Condition) error {
	// TODO a preliminary work is needed on hypershift side
	klog.InfoS("implement me")
	return nil
}

func (w writer) UpdateOwnedConditions(ctx context.Context, object client.Object) error {
	// TODO a preliminary work is needed on hypershift side
	klog.InfoS("implement me")
	return nil
}
