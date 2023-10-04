package util

import (
	"errors"
	"fmt"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

func mockAPIStatusError(reason metav1.StatusReason, code int) *apierrors.StatusError {
	return &apierrors.StatusError{ErrStatus: metav1.Status{
		Reason: reason,
		Code:   int32(code),
	}}
}

func TestIsNoMatchError(t *testing.T) {
	defaultErr := discovery.ErrGroupDiscoveryFailed{
		Groups: map[schema.GroupVersion]error{
			{
				Group:   "apps",
				Version: "v1",
			}: fmt.Errorf("resource does not exist"),
		},
	}

	testCases := []struct {
		desc          string
		err           error
		expectedMatch bool
	}{
		{
			desc:          "should match discovery.ErrGroupDiscoveryFailed",
			err:           &defaultErr,
			expectedMatch: true,
		},
		{
			desc:          "should match meta.NoResourceMatchError",
			err:           &meta.NoResourceMatchError{},
			expectedMatch: true,
		},
		{
			desc:          "should unwrap error tree and match discovery.ErrGroupDiscoveryFailed",
			err:           fmt.Errorf("wrapped error %w", fmt.Errorf("inner %w", &defaultErr)),
			expectedMatch: true,
		},
		{
			desc:          "should unwrap error tree and match meta.NoResourceMatchError",
			err:           fmt.Errorf("wrapped error %w", fmt.Errorf("inner %w", &meta.NoResourceMatchError{})),
			expectedMatch: true,
		},
		{
			desc:          "should not match regular error",
			err:           errors.New("some other error"),
			expectedMatch: false,
		},
		{
			desc:          "should not match on api error: NotFound",
			err:           mockAPIStatusError(metav1.StatusReasonNotFound, 404),
			expectedMatch: false,
		},
		{
			desc:          "should not match on api error: Gone",
			err:           mockAPIStatusError(metav1.StatusReasonGone, 410),
			expectedMatch: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			if IsNoMatchError(tc.err) != tc.expectedMatch {
				t.Errorf("error did not match expected: %T", tc.err)
			}
		})
	}
}
