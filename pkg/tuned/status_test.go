package tuned

import (
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
)

func TestComputeStatusConditions(t *testing.T) {
	testCases := []struct {
		name     string
		status   Bits
		stderr   string
		conds    []tunedv1.StatusCondition
		expected []tunedv1.StatusCondition
	}{
		{
			name: "nil",
			expected: []tunedv1.StatusCondition{
				{
					Type:   tunedv1.TunedProfileApplied,
					Status: corev1.ConditionFalse,
					LastTransitionTime: metav1.Time{
						Time: testTime(),
					},
					Reason:  "Failed",
					Message: "The TuneD daemon profile not yet applied, or application failed.",
				},
				{
					Type:   tunedv1.TunedDegraded,
					Status: corev1.ConditionFalse,
					LastTransitionTime: metav1.Time{
						Time: testTime(),
					},
					Reason:  "AsExpected",
					Message: "No warning or error messages observed applying the TuneD daemon profile.",
				},
			},
		},
		{
			name:   "only-deferred",
			status: scDeferred,
			expected: []tunedv1.StatusCondition{
				{
					Type:   tunedv1.TunedProfileApplied,
					Status: corev1.ConditionFalse,
					LastTransitionTime: metav1.Time{
						Time: testTime(),
					},
					Reason:  "Deferred",
					Message: "The TuneD daemon profile is waiting for the next node restart",
				},
				{
					Type:   tunedv1.TunedDegraded,
					Status: corev1.ConditionTrue,
					LastTransitionTime: metav1.Time{
						Time: testTime(),
					},
					Reason:  "TunedDeferredUpdate",
					Message: "Profile will be applied at the next node restart",
				},
			},
		},
		{
			name:   "error-deferred",
			status: scError | scDeferred,
			stderr: "test-error",
			expected: []tunedv1.StatusCondition{
				{
					Type:   tunedv1.TunedProfileApplied,
					Status: corev1.ConditionFalse,
					LastTransitionTime: metav1.Time{
						Time: testTime(),
					},
					Reason:  "Deferred",
					Message: "The TuneD daemon profile is waiting for the next node restart: test-error",
				},
				{
					Type:   tunedv1.TunedDegraded,
					Status: corev1.ConditionTrue,
					LastTransitionTime: metav1.Time{
						Time: testTime(),
					},
					Reason:  "TunedError",
					Message: "TuneD daemon issued one or more error message(s) during profile application. TuneD stderr: test-error",
				},
			},
		},
		{
			name:   "sysctl-deferred",
			status: scSysctlOverride | scDeferred,
			stderr: "test-error",
			expected: []tunedv1.StatusCondition{
				{
					Type:   tunedv1.TunedProfileApplied,
					Status: corev1.ConditionFalse,
					LastTransitionTime: metav1.Time{
						Time: testTime(),
					},
					Reason:  "Deferred",
					Message: "The TuneD daemon profile is waiting for the next node restart: test-error",
				},
				{
					Type:   tunedv1.TunedDegraded,
					Status: corev1.ConditionTrue,
					LastTransitionTime: metav1.Time{
						Time: testTime(),
					},
					Reason:  "TunedDeferredUpdate",
					Message: "Profile will be applied at the next node restart: test-error",
				},
			},
		},
		{
			name:   "warning-deferred",
			status: scWarn | scDeferred,
			stderr: "test-error",
			expected: []tunedv1.StatusCondition{
				{
					Type:   tunedv1.TunedProfileApplied,
					Status: corev1.ConditionFalse,
					LastTransitionTime: metav1.Time{
						Time: testTime(),
					},
					Reason:  "Deferred",
					Message: "The TuneD daemon profile is waiting for the next node restart: test-error",
				},
				{
					Type:   tunedv1.TunedDegraded,
					Status: corev1.ConditionTrue,
					LastTransitionTime: metav1.Time{
						Time: testTime(),
					},
					Reason:  "TunedDeferredUpdate",
					Message: "Profile will be applied at the next node restart: test-error",
				},
			},
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got := clearTimestamps(computeStatusConditions(tt.status, tt.stderr, tt.conds))
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("got=%#v expected=%#v", got, tt.expected)
			}
		})
	}
}

func clearTimestamps(conds []tunedv1.StatusCondition) []tunedv1.StatusCondition {
	ret := make([]tunedv1.StatusCondition, 0, len(conds))
	for idx := range conds {
		cond := conds[idx] // local copy
		cond.LastTransitionTime = metav1.Time{
			Time: testTime(),
		}
		ret = append(ret, cond)
	}
	return ret
}

func testTime() time.Time {
	return time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
}
