// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1alpha1

import (
	machineconfigurationv1alpha1 "github.com/openshift/api/machineconfiguration/v1alpha1"
	internal "github.com/openshift/client-go/machineconfiguration/applyconfigurations/internal"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	managedfields "k8s.io/apimachinery/pkg/util/managedfields"
	v1 "k8s.io/client-go/applyconfigurations/meta/v1"
)

// MachineOSBuildApplyConfiguration represents a declarative configuration of the MachineOSBuild type for use
// with apply.
type MachineOSBuildApplyConfiguration struct {
	v1.TypeMetaApplyConfiguration    `json:",inline"`
	*v1.ObjectMetaApplyConfiguration `json:"metadata,omitempty"`
	Spec                             *MachineOSBuildSpecApplyConfiguration   `json:"spec,omitempty"`
	Status                           *MachineOSBuildStatusApplyConfiguration `json:"status,omitempty"`
}

// MachineOSBuild constructs a declarative configuration of the MachineOSBuild type for use with
// apply.
func MachineOSBuild(name string) *MachineOSBuildApplyConfiguration {
	b := &MachineOSBuildApplyConfiguration{}
	b.WithName(name)
	b.WithKind("MachineOSBuild")
	b.WithAPIVersion("machineconfiguration.openshift.io/v1alpha1")
	return b
}

// ExtractMachineOSBuild extracts the applied configuration owned by fieldManager from
// machineOSBuild. If no managedFields are found in machineOSBuild for fieldManager, a
// MachineOSBuildApplyConfiguration is returned with only the Name, Namespace (if applicable),
// APIVersion and Kind populated. It is possible that no managed fields were found for because other
// field managers have taken ownership of all the fields previously owned by fieldManager, or because
// the fieldManager never owned fields any fields.
// machineOSBuild must be a unmodified MachineOSBuild API object that was retrieved from the Kubernetes API.
// ExtractMachineOSBuild provides a way to perform a extract/modify-in-place/apply workflow.
// Note that an extracted apply configuration will contain fewer fields than what the fieldManager previously
// applied if another fieldManager has updated or force applied any of the previously applied fields.
// Experimental!
func ExtractMachineOSBuild(machineOSBuild *machineconfigurationv1alpha1.MachineOSBuild, fieldManager string) (*MachineOSBuildApplyConfiguration, error) {
	return extractMachineOSBuild(machineOSBuild, fieldManager, "")
}

// ExtractMachineOSBuildStatus is the same as ExtractMachineOSBuild except
// that it extracts the status subresource applied configuration.
// Experimental!
func ExtractMachineOSBuildStatus(machineOSBuild *machineconfigurationv1alpha1.MachineOSBuild, fieldManager string) (*MachineOSBuildApplyConfiguration, error) {
	return extractMachineOSBuild(machineOSBuild, fieldManager, "status")
}

func extractMachineOSBuild(machineOSBuild *machineconfigurationv1alpha1.MachineOSBuild, fieldManager string, subresource string) (*MachineOSBuildApplyConfiguration, error) {
	b := &MachineOSBuildApplyConfiguration{}
	err := managedfields.ExtractInto(machineOSBuild, internal.Parser().Type("com.github.openshift.api.machineconfiguration.v1alpha1.MachineOSBuild"), fieldManager, b, subresource)
	if err != nil {
		return nil, err
	}
	b.WithName(machineOSBuild.Name)

	b.WithKind("MachineOSBuild")
	b.WithAPIVersion("machineconfiguration.openshift.io/v1alpha1")
	return b, nil
}

// WithKind sets the Kind field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Kind field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithKind(value string) *MachineOSBuildApplyConfiguration {
	b.TypeMetaApplyConfiguration.Kind = &value
	return b
}

// WithAPIVersion sets the APIVersion field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the APIVersion field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithAPIVersion(value string) *MachineOSBuildApplyConfiguration {
	b.TypeMetaApplyConfiguration.APIVersion = &value
	return b
}

// WithName sets the Name field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Name field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithName(value string) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.ObjectMetaApplyConfiguration.Name = &value
	return b
}

// WithGenerateName sets the GenerateName field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the GenerateName field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithGenerateName(value string) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.ObjectMetaApplyConfiguration.GenerateName = &value
	return b
}

// WithNamespace sets the Namespace field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Namespace field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithNamespace(value string) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.ObjectMetaApplyConfiguration.Namespace = &value
	return b
}

// WithUID sets the UID field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the UID field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithUID(value types.UID) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.ObjectMetaApplyConfiguration.UID = &value
	return b
}

// WithResourceVersion sets the ResourceVersion field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ResourceVersion field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithResourceVersion(value string) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.ObjectMetaApplyConfiguration.ResourceVersion = &value
	return b
}

// WithGeneration sets the Generation field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Generation field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithGeneration(value int64) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.ObjectMetaApplyConfiguration.Generation = &value
	return b
}

// WithCreationTimestamp sets the CreationTimestamp field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the CreationTimestamp field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithCreationTimestamp(value metav1.Time) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.ObjectMetaApplyConfiguration.CreationTimestamp = &value
	return b
}

// WithDeletionTimestamp sets the DeletionTimestamp field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the DeletionTimestamp field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithDeletionTimestamp(value metav1.Time) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.ObjectMetaApplyConfiguration.DeletionTimestamp = &value
	return b
}

// WithDeletionGracePeriodSeconds sets the DeletionGracePeriodSeconds field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the DeletionGracePeriodSeconds field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithDeletionGracePeriodSeconds(value int64) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	b.ObjectMetaApplyConfiguration.DeletionGracePeriodSeconds = &value
	return b
}

// WithLabels puts the entries into the Labels field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, the entries provided by each call will be put on the Labels field,
// overwriting an existing map entries in Labels field with the same key.
func (b *MachineOSBuildApplyConfiguration) WithLabels(entries map[string]string) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	if b.ObjectMetaApplyConfiguration.Labels == nil && len(entries) > 0 {
		b.ObjectMetaApplyConfiguration.Labels = make(map[string]string, len(entries))
	}
	for k, v := range entries {
		b.ObjectMetaApplyConfiguration.Labels[k] = v
	}
	return b
}

// WithAnnotations puts the entries into the Annotations field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, the entries provided by each call will be put on the Annotations field,
// overwriting an existing map entries in Annotations field with the same key.
func (b *MachineOSBuildApplyConfiguration) WithAnnotations(entries map[string]string) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	if b.ObjectMetaApplyConfiguration.Annotations == nil && len(entries) > 0 {
		b.ObjectMetaApplyConfiguration.Annotations = make(map[string]string, len(entries))
	}
	for k, v := range entries {
		b.ObjectMetaApplyConfiguration.Annotations[k] = v
	}
	return b
}

// WithOwnerReferences adds the given value to the OwnerReferences field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the OwnerReferences field.
func (b *MachineOSBuildApplyConfiguration) WithOwnerReferences(values ...*v1.OwnerReferenceApplyConfiguration) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithOwnerReferences")
		}
		b.ObjectMetaApplyConfiguration.OwnerReferences = append(b.ObjectMetaApplyConfiguration.OwnerReferences, *values[i])
	}
	return b
}

// WithFinalizers adds the given value to the Finalizers field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Finalizers field.
func (b *MachineOSBuildApplyConfiguration) WithFinalizers(values ...string) *MachineOSBuildApplyConfiguration {
	b.ensureObjectMetaApplyConfigurationExists()
	for i := range values {
		b.ObjectMetaApplyConfiguration.Finalizers = append(b.ObjectMetaApplyConfiguration.Finalizers, values[i])
	}
	return b
}

func (b *MachineOSBuildApplyConfiguration) ensureObjectMetaApplyConfigurationExists() {
	if b.ObjectMetaApplyConfiguration == nil {
		b.ObjectMetaApplyConfiguration = &v1.ObjectMetaApplyConfiguration{}
	}
}

// WithSpec sets the Spec field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Spec field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithSpec(value *MachineOSBuildSpecApplyConfiguration) *MachineOSBuildApplyConfiguration {
	b.Spec = value
	return b
}

// WithStatus sets the Status field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Status field is set to the value of the last call.
func (b *MachineOSBuildApplyConfiguration) WithStatus(value *MachineOSBuildStatusApplyConfiguration) *MachineOSBuildApplyConfiguration {
	b.Status = value
	return b
}

// GetName retrieves the value of the Name field in the declarative configuration.
func (b *MachineOSBuildApplyConfiguration) GetName() *string {
	b.ensureObjectMetaApplyConfigurationExists()
	return b.ObjectMetaApplyConfiguration.Name
}
