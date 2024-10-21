/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
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

// FakeProfiles implements ProfileInterface
type FakeProfiles struct {
	Fake *FakeTunedV1
	ns   string
}

var profilesResource = v1.SchemeGroupVersion.WithResource("profiles")

var profilesKind = v1.SchemeGroupVersion.WithKind("Profile")

// Get takes name of the profile, and returns the corresponding profile object, and an error if there is any.
func (c *FakeProfiles) Get(ctx context.Context, name string, options metav1.GetOptions) (result *v1.Profile, err error) {
	emptyResult := &v1.Profile{}
	obj, err := c.Fake.
		Invokes(testing.NewGetActionWithOptions(profilesResource, c.ns, name, options), emptyResult)

	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1.Profile), err
}

// List takes label and field selectors, and returns the list of Profiles that match those selectors.
func (c *FakeProfiles) List(ctx context.Context, opts metav1.ListOptions) (result *v1.ProfileList, err error) {
	emptyResult := &v1.ProfileList{}
	obj, err := c.Fake.
		Invokes(testing.NewListActionWithOptions(profilesResource, profilesKind, c.ns, opts), emptyResult)

	if obj == nil {
		return emptyResult, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1.ProfileList{ListMeta: obj.(*v1.ProfileList).ListMeta}
	for _, item := range obj.(*v1.ProfileList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested profiles.
func (c *FakeProfiles) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewWatchActionWithOptions(profilesResource, c.ns, opts))

}

// Create takes the representation of a profile and creates it.  Returns the server's representation of the profile, and an error, if there is any.
func (c *FakeProfiles) Create(ctx context.Context, profile *v1.Profile, opts metav1.CreateOptions) (result *v1.Profile, err error) {
	emptyResult := &v1.Profile{}
	obj, err := c.Fake.
		Invokes(testing.NewCreateActionWithOptions(profilesResource, c.ns, profile, opts), emptyResult)

	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1.Profile), err
}

// Update takes the representation of a profile and updates it. Returns the server's representation of the profile, and an error, if there is any.
func (c *FakeProfiles) Update(ctx context.Context, profile *v1.Profile, opts metav1.UpdateOptions) (result *v1.Profile, err error) {
	emptyResult := &v1.Profile{}
	obj, err := c.Fake.
		Invokes(testing.NewUpdateActionWithOptions(profilesResource, c.ns, profile, opts), emptyResult)

	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1.Profile), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeProfiles) UpdateStatus(ctx context.Context, profile *v1.Profile, opts metav1.UpdateOptions) (result *v1.Profile, err error) {
	emptyResult := &v1.Profile{}
	obj, err := c.Fake.
		Invokes(testing.NewUpdateSubresourceActionWithOptions(profilesResource, "status", c.ns, profile, opts), emptyResult)

	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1.Profile), err
}

// Delete takes name of the profile and deletes it. Returns an error if one occurs.
func (c *FakeProfiles) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewDeleteActionWithOptions(profilesResource, c.ns, name, opts), &v1.Profile{})

	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeProfiles) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	action := testing.NewDeleteCollectionActionWithOptions(profilesResource, c.ns, opts, listOpts)

	_, err := c.Fake.Invokes(action, &v1.ProfileList{})
	return err
}

// Patch applies the patch and returns the patched profile.
func (c *FakeProfiles) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (result *v1.Profile, err error) {
	emptyResult := &v1.Profile{}
	obj, err := c.Fake.
		Invokes(testing.NewPatchSubresourceActionWithOptions(profilesResource, c.ns, name, pt, data, opts, subresources...), emptyResult)

	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1.Profile), err
}
