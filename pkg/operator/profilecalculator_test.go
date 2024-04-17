package operator

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
)

func tunedProfileToString(tunedProfile tunedv1.TunedProfile) string {
	var (
		name, data string
		sb         strings.Builder
	)

	if tunedProfile.Name == nil {
		name = "<nil>"
	} else {
		name = *tunedProfile.Name
	}
	if tunedProfile.Data == nil {
		data = "<nil>"
	} else {
		data = *tunedProfile.Data
	}
	sb.WriteString(fmt.Sprintf("Name: %s; Data: %s", name, data))

	return sb.String()
}

func tunedProfilesToString(tunedProfiles []tunedv1.TunedProfile) string {
	var sb strings.Builder

	for i, tunedProfile := range tunedProfiles {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(tunedProfileToString(tunedProfile))
	}

	return sb.String()
}

func TestTunedProfiles(t *testing.T) {
	profileData := "[main] # a dummy TuneD profile with no configuration"
	profilePriority := uint64(20)

	var (
		tests = []struct {
			input          []*tunedv1.Tuned
			expectedOutput []tunedv1.TunedProfile
		}{
			{
				input: []*tunedv1.Tuned{
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: tunedv1.SchemeGroupVersion.String(),
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "profile-b",
							Namespace: "openshift-cluster-node-tuning-operator",
							UID:       types.UID(utilrand.String(5)),
						},
						Spec: tunedv1.TunedSpec{
							Profile: []tunedv1.TunedProfile{
								{
									Name: ptr.To("b"),
									Data: &profileData,
								},
							},
							Recommend: []tunedv1.TunedRecommend{
								{
									Priority: ptr.To(profilePriority),
									Profile:  ptr.To("b"),
								},
							},
						},
					},
					{
						TypeMeta: metav1.TypeMeta{
							APIVersion: tunedv1.SchemeGroupVersion.String(),
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "profile-a",
							Namespace: "openshift-cluster-node-tuning-operator",
							UID:       types.UID(utilrand.String(5)),
						},
						Spec: tunedv1.TunedSpec{
							Profile: []tunedv1.TunedProfile{
								{
									Name: ptr.To("a"),
									Data: &profileData,
								},
							},
							Recommend: []tunedv1.TunedRecommend{
								{
									Priority: ptr.To(profilePriority),
									Profile:  ptr.To("a"),
								},
							},
						},
					},
				},
				expectedOutput: []tunedv1.TunedProfile{
					{
						Name: ptr.To("a"),
						Data: &profileData,
					},
					{
						Name: ptr.To("b"),
						Data: &profileData,
					},
				},
			},
		}
	)

	for i, tc := range tests {
		tunedProfilesSorted := TunedProfiles(tc.input)

		if !reflect.DeepEqual(tc.expectedOutput, tunedProfilesSorted) {
			t.Errorf(
				"failed test case %d:\n\twant:\n%s\n\thave:\n%s",
				i+1,
				tunedProfilesToString(tc.expectedOutput),
				tunedProfilesToString(tunedProfilesSorted),
			)
		}
	}
}
