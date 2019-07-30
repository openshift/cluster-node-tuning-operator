// +k8s:deepcopy-gen=package,register
// +k8s:defaulter-gen=TypeMeta
// +k8s:openapi-gen=true
// +groupName=tuned.openshift.io
package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TunedSpec defines the desired state of Tuned
type TunedSpec struct {
	Profile   []TunedProfile   `json:"profile"`
	Recommend []TunedRecommend `json:"recommend"`
}

type TunedProfile struct {
	Name *string `json:"name"`
	Data *string `json:"data"`
}

type TunedRecommend struct {
	Profile  *string      `json:"profile"`
	Priority *uint64      `json:"priority"`
	Match    []TunedMatch `json:"match,omitempty"`
}

type TunedMatch struct {
	Label *string      `json:"label"`
	Value *string      `json:"value,omitempty"`
	Type  *string      `json:"type,omitempty"`
	Match []TunedMatch `json:"match,omitempty"`
}

// TunedStatus defines the observed state of Tuned
type TunedStatus struct {
}

// +kubebuilder:object:root=true
// Tuned is the Schema for the tuneds API
type Tuned struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TunedSpec   `json:"spec,omitempty"`
	Status TunedStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// TunedList contains a list of Tuned
type TunedList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tuned `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Tuned{}, &TunedList{})
}
