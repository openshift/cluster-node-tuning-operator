package util

import (
	"reflect"
	"testing"
)

func TestHasDeferredUpdateAnnotation(t *testing.T) {
	testCases := []struct {
		name     string
		anns     map[string]string
		vals     []string
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
		{
			name: "found single",
			anns: map[string]string{
				"tuned.openshift.io/deferred": "foo",
			},
			vals:     []string{"foo"},
			expected: true,
		},
		{
			name: "found partial",
			anns: map[string]string{
				"tuned.openshift.io/deferred": "foo,bar",
			},
			vals:     []string{"foo"},
			expected: true,
		},
		{
			name: "found partial",
			anns: map[string]string{
				"tuned.openshift.io/deferred": "bar",
			},
			vals:     []string{"foo", "bar"},
			expected: false,
		},
		{
			name: "missing single",
			anns: map[string]string{
				"tuned.openshift.io/deferred": "",
			},
			vals:     []string{"foo"},
			expected: true, // no ann value means accept everything
		},
		{
			name: "missing partial",
			anns: map[string]string{
				"tuned.openshift.io/deferred": "bar,foo",
			},
			vals:     []string{"foo", "bar", "baz"},
			expected: false,
		},
		{
			name: "wildcard - implicit",
			anns: map[string]string{
				"tuned.openshift.io/deferred": "",
			},
			vals:     []string{"foo", "bar", "baz"},
			expected: true,
		},
		{
			name: "wildcard - explicit",
			anns: map[string]string{
				"tuned.openshift.io/deferred": "*",
			},
			vals:     []string{"foo", "bar", "baz"},
			expected: true,
		},
		{
			name: "wildcard - mixed",
			anns: map[string]string{
				"tuned.openshift.io/deferred": "*,foo",
			},
			vals:     []string{"foo", "bar", "baz"},
			expected: true,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got := HasDeferredUpdateAnnotation(tt.anns, tt.vals...)
			if got != tt.expected {
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
