package util

import (
	"reflect"
	"testing"
)

func TestHasDeferredUpdateAnnotation(t *testing.T) {
	testCases := []struct {
		name     string
		anns     map[string]string
		expected DeferMode
	}{
		{
			name:     "nil",
			expected: DeferNever,
		},
		{
			name:     "empty",
			anns:     map[string]string{},
			expected: DeferNever,
		},
		{
			name: "no-ann",
			anns: map[string]string{
				"foo": "bar",
				"baz": "2",
			},
			expected: DeferNever,
		},
		{
			name: "wrong-case",
			anns: map[string]string{
				"tuned.openshift.io/Deferred": "",
			},
			expected: DeferNever,
		},
		{
			name: "found",
			anns: map[string]string{
				"tuned.openshift.io/deferred": "",
			},
			expected: DeferNever,
		},
		{
			name: "found-explicit",
			anns: map[string]string{
				"tuned.openshift.io/deferred": "always",
			},
			expected: DeferAlways,
		},
		{
			name: "found-wrong-case",
			anns: map[string]string{
				"tuned.openshift.io/deferred": "Always",
			},
			expected: DeferNever,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got := GetDeferredUpdateAnnotation(tt.anns)
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
		mode     DeferMode
		expected map[string]string
	}{
		{
			name:     "nil",
			mode:     DeferNever,
			expected: map[string]string{},
		},
		{
			name: "add",
			mode: DeferAlways,
			expected: map[string]string{
				"tuned.openshift.io/deferred": "always",
			},
		},
		{
			name: "add-existing",
			anns: map[string]string{
				"foobar": "42",
			},
			mode: DeferAlways,
			expected: map[string]string{
				"foobar":                      "42",
				"tuned.openshift.io/deferred": "always",
			},
		},
		{
			name: "add-overwrite",
			anns: map[string]string{
				"foobar":                      "42",
				"tuned.openshift.io/deferred": "",
			},
			mode: DeferAlways,
			expected: map[string]string{
				"foobar":                      "42",
				"tuned.openshift.io/deferred": "always",
			},
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got := SetDeferredUpdateAnnotation(tt.anns, tt.mode)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("got=%v expected=%v", got, tt.expected)
			}
		})
	}
}

func TestDeleteDeferredUpdateAnnotation(t *testing.T) {
	testCases := []struct {
		name     string
		anns     map[string]string
		expected map[string]string
	}{
		{
			name:     "nil",
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
			got := DeleteDeferredUpdateAnnotation(tt.anns)
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
