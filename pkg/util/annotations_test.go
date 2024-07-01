package util

import (
	"reflect"
	"testing"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHasDeferredUpdateAnnotation(t *testing.T) {
	testCases := []struct {
		name     string
		anns     map[string]string
		expected bool
	}{
		{
			name:     "nil",
			expected: false,
		},
		{
			name:     "empty",
			anns:     map[string]string{},
			expected: false,
		},
		{
			name: "no-ann",
			anns: map[string]string{
				"foo": "bar",
				"baz": "2",
			},
			expected: false,
		},
		{
			name: "wrong-case",
			anns: map[string]string{
				"tuned.openshift.io/Deferred": "",
			},
			expected: false,
		},
		{
			name: "found",
			anns: map[string]string{
				"tuned.openshift.io/deferred": "",
			},
			expected: true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got := HasDeferredUpdateAnnotation(tt.anns)
			if got != tt.expected {
				t.Errorf("got=%v expected=%v", got, tt.expected)
			}
		})
	}
}

func TestSetDeferredUpdateAnnotation(t *testing.T) {
	testCases := []struct {
		name     string
		anns     map[string]string
		tuned    *tunedv1.Tuned
		expected map[string]string
	}{
		{
			name:     "nil",
			tuned:    &tunedv1.Tuned{},
			expected: map[string]string{},
		},
		{
			name: "nil-add",
			tuned: &tunedv1.Tuned{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"tuned.openshift.io/deferred": "",
					},
				},
			},
			expected: map[string]string{
				"tuned.openshift.io/deferred": "",
			},
		},
		{
			name: "existing-add",
			anns: map[string]string{
				"foobar": "42",
			},
			tuned: &tunedv1.Tuned{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"tuned.openshift.io/deferred": "",
					},
				},
			},
			expected: map[string]string{
				"foobar":                      "42",
				"tuned.openshift.io/deferred": "",
			},
		},
		{
			name:     "nil-remove",
			tuned:    &tunedv1.Tuned{},
			expected: map[string]string{},
		},
		{
			name: "existing-remove",
			anns: map[string]string{
				"foobar":                      "42",
				"tuned.openshift.io/deferred": "",
			},
			tuned: &tunedv1.Tuned{},
			expected: map[string]string{
				"foobar": "42",
			},
		},
		{
			name: "missing-remove",
			anns: map[string]string{
				"foobar": "42",
			},
			tuned: &tunedv1.Tuned{},
			expected: map[string]string{
				"foobar": "42",
			},
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got := SetDeferredUpdateAnnotation(tt.anns, tt.tuned)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("got=%v expected=%v", got, tt.expected)
			}
		})
	}
}

func TestToggleDeferredUpdateAnnotation(t *testing.T) {
	testCases := []struct {
		name     string
		anns     map[string]string
		toggle   bool
		expected map[string]string
	}{
		{
			name:     "nil",
			expected: map[string]string{},
		},
		{
			name:   "nil-add",
			toggle: true,
			expected: map[string]string{
				"tuned.openshift.io/deferred": "",
			},
		},
		{
			name: "existing-add",
			anns: map[string]string{
				"foobar": "42",
			},
			toggle: true,
			expected: map[string]string{
				"foobar":                      "42",
				"tuned.openshift.io/deferred": "",
			},
		},
		{
			name:     "nil-remove",
			expected: map[string]string{},
		},
		{
			name: "existing-remove",
			anns: map[string]string{
				"foobar":                      "42",
				"tuned.openshift.io/deferred": "",
			},
			expected: map[string]string{
				"foobar": "42",
			},
		},
		{
			name: "missing-remove",
			anns: map[string]string{
				"foobar": "42",
			},
			expected: map[string]string{
				"foobar": "42",
			},
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			anns := cloneMapStringString(tt.anns)
			got := ToggleDeferredUpdateAnnotation(tt.anns, tt.toggle)
			// must not mutate the argument
			if tt.anns != nil && !reflect.DeepEqual(anns, tt.anns) {
				t.Errorf("toggle must return a new copy")
			}
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("got=%v expected=%v", got, tt.expected)
			}
		})
	}
}
