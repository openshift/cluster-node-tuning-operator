package client

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift"
)

type dataPlaneImpl struct {
	client.Client
}

func (dpi *dataPlaneImpl) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if !hypershift.IsReadableFromControlPlane(obj) {
		return fmt.Errorf("the provided object %s might not be presented on hypershift cluster while using this client."+
			"please use ControlPlaneClient client instead", key.String())
	}
	return dpi.Client.Get(ctx, key, obj, opts...)
}

func (dpi *dataPlaneImpl) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if !hypershift.IsReadableFromControlPlane(list) {
		return fmt.Errorf("the provided list of %s objects might not be presented on hypershift cluster while using this client."+
			"please use ControlPlaneClient client instead", list.GetObjectKind())
	}
	return dpi.Client.List(ctx, list, opts...)
}

func (dpi *dataPlaneImpl) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if !hypershift.IsWriteableToControlPlane(obj) {
		return fmt.Errorf("the provided object %s/%s might not get created on hypershift cluster while using this client."+
			"please use ControlPlaneClient client instead", obj.GetObjectKind(), obj.GetName())
	}
	return dpi.Client.Create(ctx, obj, opts...)
}

func (dpi *dataPlaneImpl) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if !hypershift.IsWriteableToControlPlane(obj) {
		return fmt.Errorf("the provided object %s/%s might not get deleted on hypershift cluster while using this client."+
			"please use ControlPlaneClient client instead", obj.GetObjectKind(), obj.GetName())
	}
	return dpi.Client.Delete(ctx, obj, opts...)
}

func (dpi *dataPlaneImpl) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if !hypershift.IsWriteableToControlPlane(obj) {
		return fmt.Errorf("the provided object %s/%s might not get updated on hypershift cluster while using this client."+
			"please use ControlPlaneClient client instead", obj.GetObjectKind(), obj.GetName())
	}
	return dpi.Client.Update(ctx, obj, opts...)
}

func (dpi *dataPlaneImpl) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if !hypershift.IsWriteableToControlPlane(obj) {
		return fmt.Errorf("the provided object %s/%s might not get patched on hypershift cluster while using this client."+
			"please use ControlPlaneClient client instead", obj.GetObjectKind(), obj.GetName())
	}
	return dpi.Client.Patch(ctx, obj, patch, opts...)
}

func (dpi *dataPlaneImpl) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	if !hypershift.IsWriteableToControlPlane(obj) {
		return fmt.Errorf("the provided object %s/%s might not get deleted on hypershift cluster while using this client."+
			"please use ControlPlaneClient client instead", obj.GetObjectKind(), obj.GetName())
	}
	return dpi.Client.DeleteAllOf(ctx, obj, opts...)
}
