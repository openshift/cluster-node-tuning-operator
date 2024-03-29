// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	"context"

	v1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeTuneds implements TunedInterface
type FakeTuneds struct {
	Fake *FakeTunedV1
	ns   string
}

var tunedsResource = v1.SchemeGroupVersion.WithResource("tuneds")

var tunedsKind = v1.SchemeGroupVersion.WithKind("Tuned")

// Get takes name of the tuned, and returns the corresponding tuned object, and an error if there is any.
func (c *FakeTuneds) Get(ctx context.Context, name string, options metav1.GetOptions) (result *v1.Tuned, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewGetAction(tunedsResource, c.ns, name), &v1.Tuned{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1.Tuned), err
}

// List takes label and field selectors, and returns the list of Tuneds that match those selectors.
func (c *FakeTuneds) List(ctx context.Context, opts metav1.ListOptions) (result *v1.TunedList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewListAction(tunedsResource, tunedsKind, c.ns, opts), &v1.TunedList{})

	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1.TunedList{ListMeta: obj.(*v1.TunedList).ListMeta}
	for _, item := range obj.(*v1.TunedList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested tuneds.
func (c *FakeTuneds) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewWatchAction(tunedsResource, c.ns, opts))

}

// Create takes the representation of a tuned and creates it.  Returns the server's representation of the tuned, and an error, if there is any.
func (c *FakeTuneds) Create(ctx context.Context, tuned *v1.Tuned, opts metav1.CreateOptions) (result *v1.Tuned, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewCreateAction(tunedsResource, c.ns, tuned), &v1.Tuned{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1.Tuned), err
}

// Update takes the representation of a tuned and updates it. Returns the server's representation of the tuned, and an error, if there is any.
func (c *FakeTuneds) Update(ctx context.Context, tuned *v1.Tuned, opts metav1.UpdateOptions) (result *v1.Tuned, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateAction(tunedsResource, c.ns, tuned), &v1.Tuned{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1.Tuned), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeTuneds) UpdateStatus(ctx context.Context, tuned *v1.Tuned, opts metav1.UpdateOptions) (*v1.Tuned, error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateSubresourceAction(tunedsResource, "status", c.ns, tuned), &v1.Tuned{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1.Tuned), err
}

// Delete takes name of the tuned and deletes it. Returns an error if one occurs.
func (c *FakeTuneds) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewDeleteActionWithOptions(tunedsResource, c.ns, name, opts), &v1.Tuned{})

	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeTuneds) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	action := testing.NewDeleteCollectionAction(tunedsResource, c.ns, listOpts)

	_, err := c.Fake.Invokes(action, &v1.TunedList{})
	return err
}

// Patch applies the patch and returns the patched tuned.
func (c *FakeTuneds) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (result *v1.Tuned, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewPatchSubresourceAction(tunedsResource, c.ns, name, pt, data, subresources...), &v1.Tuned{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1.Tuned), err
}
