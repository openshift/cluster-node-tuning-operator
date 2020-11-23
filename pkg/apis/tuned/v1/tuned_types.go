package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// TunedDefaultResourceName is the name of the Node Tuning Operator's default custom tuned resource
	TunedDefaultResourceName = "default"

	// TunedRenderedResourceName is the name of the Node Tuning Operator's tuned resource combined out of
	// all the other custom tuned resources
	TunedRenderedResourceName = "rendered"

	// TunedClusterOperatorResourceName is the name of the clusteroperator resource
	// that reflects the node tuning operator status.
	TunedClusterOperatorResourceName = "node-tuning"
)

/////////////////////////////////////////////////////////////////////////////////
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Tuned is a collection of rules that allows cluster-wide deployment
// of node-level sysctls and more flexibility to add custom tuning
// specified by user needs.  These rules are translated and passed to all
// containerized Tuned daemons running in the cluster in the format that
// the daemons understand. The responsibility for applying the node-level
// tuning then lies with the containerized Tuned daemons. More info:
// https://github.com/openshift/cluster-node-tuning-operator
type Tuned struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the specification of the desired behavior of Tuned. More info:
	// https://git.k8s.io/community/contributors/devel/api-conventions.md#spec-and-status
	Spec   TunedSpec   `json:"spec,omitempty"`
	Status TunedStatus `json:"status,omitempty"`
}

type TunedSpec struct {
	// managementState indicates whether the registry instance represented
	// by this config instance is under operator management or not.  Valid
	// values are Force, Managed, Unmanaged, and Removed.
	// +optional
	ManagementState operatorv1.ManagementState `json:"managementState,omitempty" protobuf:"bytes,1,opt,name=managementState,casttype=github.com/openshift/api/operator/v1.ManagementState"`
	// Tuned profiles.
	Profile []TunedProfile `json:"profile"`
	// Selection logic for all Tuned profiles.
	Recommend []TunedRecommend `json:"recommend"`
}

// A Tuned profile.
type TunedProfile struct {
	// Name of the Tuned profile to be used in the recommend section.
	Name *string `json:"name"`
	// Specification of the Tuned profile to be consumed by the Tuned daemon.
	Data *string `json:"data"`
}

// Selection logic for a single Tuned profile.
type TunedRecommend struct {
	// Name of the Tuned profile to recommend.
	Profile *string `json:"profile"`

	// Tuned profile priority. Highest priority is 0.
	// +kubebuilder:validation:Minimum=0
	Priority *uint64 `json:"priority"`
	// Rules governing application of a Tuned profile connected by logical OR operator.
	Match []TunedMatch `json:"match,omitempty"`
	// MachineConfigLabels specifies the labels for a MachineConfig. The MachineConfig is created
	// automatically to apply additional host settings (e.g. kernel boot parameters) profile 'Profile'
	// needs and can only be applied by creating a MachineConfig. This involves finding all
	// MachineConfigPools with machineConfigSelector matching the MachineConfigLabels and setting the
	// profile 'Profile' on all nodes that match the MachineConfigPools' nodeSelectors.
	MachineConfigLabels map[string]string `json:"machineConfigLabels,omitempty"`

	// Optional operand configuration.
	// +optional
	Operand OperandConfig `json:"operand,omitempty"`
}

// Rules governing application of a Tuned profile.
type TunedMatch struct {
	// Node or Pod label name.
	Label *string `json:"label"`
	// Node or Pod label value. If omitted, the presence of label name is enough to match.
	Value *string `json:"value,omitempty"`
	// Match type: [node/pod]. If omitted, "node" is assumed.
	// +kubebuilder:validation:Enum={"node","pod"}
	Type *string `json:"type,omitempty"`

	// Additional rules governing application of the tuned profile connected by logical AND operator.
	Match []TunedMatch `json:"match,omitempty"`
}

type OperandConfig struct {
	// turn debugging on/off for the Tuned daemon: true/false (default is false)
	Debug bool `json:"debug"`
}

// TunedStatus is the status for a Tuned resource
type TunedStatus struct {
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TunedList is a list of Tuned resources
type TunedList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tuned `json:"items"`
}

/////////////////////////////////////////////////////////////////////////////////
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Profile is a specification for a Profile resource
type Profile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProfileSpec   `json:"spec,omitempty"`
	Status ProfileStatus `json:"status,omitempty"`
}

type ProfileSpec struct {
	Config ProfileConfig `json:"config"`
}

type ProfileConfig struct {
	// Tuned profile to apply
	TunedProfile string `json:"tunedProfile"`
	// option to debug Tuned daemon execution
	// +optional
	Debug bool `json:"debug"`
}

// ProfileStatus is the status for a Profile resource; the status is for internal use only
// and its fields may be changed/removed in the future.
type ProfileStatus struct {
	// kernel parameters calculated by tuned for the active Tuned profile
	// +optional
	Bootcmdline string `json:"bootcmdline"`
	// deploy stall daemon: https://github.com/bristot/stalld/
	// +optional
	Stalld bool `json:"stalld"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// ProfileList is a list of Profile resources
type ProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Profile `json:"items"`
}
