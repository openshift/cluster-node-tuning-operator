package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type TunedList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Tuned `json:"items"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Tuned struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              TunedSpec   `json:"spec"`
	Status            TunedStatus `json:"status,omitempty"`
}

type TunedSpec struct {
	Profiles  []TunedProfile `json:"profiles"`
	Recommend *string        `json:"recommend"`
}

type TunedProfile struct {
	Name *string `json:"name"`
	Data *string `json:"data"`
}

type TunedStatus struct {
	// Fill me
}
