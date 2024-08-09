package tuned

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
)

func TestRecommendFileRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	rfPath := filepath.Join(tmpDir, "50-test.conf")
	profName := "test-recommend"

	err := TunedRecommendFileWritePath(rfPath, profName)
	if err != nil {
		t.Fatalf("unexpected error writing profile %q path %q: %v", profName, rfPath, err)
	}

	got, err := TunedRecommendFileReadPath(rfPath)
	if err != nil {
		t.Fatalf("unexpected error reading from path %q: %v", rfPath, err)
	}

	if got != profName {
		t.Errorf("profile name got %q expected %q", got, profName)
	}
}

func TestFilterAndSortProfiles(t *testing.T) {
	testCases := []struct {
		name     string
		profiles []tunedv1.TunedProfile
		expected []tunedv1.TunedProfile
	}{
		{
			name:     "nil",
			expected: []tunedv1.TunedProfile{},
		},
		{
			name:     "empty",
			profiles: []tunedv1.TunedProfile{},
			expected: []tunedv1.TunedProfile{},
		},
		{
			name: "single",
			profiles: []tunedv1.TunedProfile{
				{
					Name: newString("aaa"),
					Data: newString("data"),
				},
			},
			expected: []tunedv1.TunedProfile{
				{
					Name: newString("aaa"),
					Data: newString("data"),
				},
			},
		},
		{
			name: "single, partial",
			profiles: []tunedv1.TunedProfile{
				{
					Name: newString("aaa"),
				},
			},
			expected: []tunedv1.TunedProfile{},
		},
		{
			name: "multi,sorted",
			profiles: []tunedv1.TunedProfile{
				{
					Name: newString("aaa"),
					Data: newString("data"),
				},
				{
					Name: newString("bbb"),
					Data: newString("data"),
				},
				{
					Name: newString("ccc"),
					Data: newString("data"),
				},
			},
			expected: []tunedv1.TunedProfile{
				{
					Name: newString("aaa"),
					Data: newString("data"),
				},
				{
					Name: newString("bbb"),
					Data: newString("data"),
				},
				{
					Name: newString("ccc"),
					Data: newString("data"),
				},
			},
		},
		{
			name: "multi,reverse",
			profiles: []tunedv1.TunedProfile{
				{
					Name: newString("ccc"),
					Data: newString("data"),
				},
				{
					Name: newString("bbb"),
					Data: newString("data"),
				},
				{
					Name: newString("aaa"),
					Data: newString("data"),
				},
			},
			expected: []tunedv1.TunedProfile{
				{
					Name: newString("aaa"),
					Data: newString("data"),
				},
				{
					Name: newString("bbb"),
					Data: newString("data"),
				},
				{
					Name: newString("ccc"),
					Data: newString("data"),
				},
			},
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got := filterAndSortProfiles(tt.profiles)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("got=%#v expected=%#v", got, tt.expected)
			}
		})
	}
}

func TestProfileFingerprint(t *testing.T) {
	testCases := []struct {
		name               string
		profiles           []tunedv1.TunedProfile
		recommendedProfile string
		expected           string
	}{
		// all hashes computed manually (well, using throwaway go code and shell tools) on developer box
		{
			name:     "nil",
			expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "no-name",
			profiles: []tunedv1.TunedProfile{
				{
					Data: newString("test-data"),
				},
			},
			expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "no-data",
			profiles: []tunedv1.TunedProfile{
				{
					Name: newString("test-name"),
				},
			},
			expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "minimal",
			profiles: []tunedv1.TunedProfile{
				{
					Name: newString("test-name"),
					Data: newString("test-data"),
				},
			},
			expected: "a186000422feab857329c684e9fe91412b1a5db084100b37a98cfc95b62aa867",
		},
		{
			name: "minimal-multi-entry",
			profiles: []tunedv1.TunedProfile{
				{
					Name: newString("test-name-1"),
					Data: newString("test-data-1"),
				},
				{
					Name: newString("test-name-2"),
					Data: newString("test-data-2"),
				},
			},
			expected: "72e7e1930db49379e31aa370d4274f9caada231c775a704db7e78dc856e67662",
		},
		{
			name: "skip-no-data",
			profiles: []tunedv1.TunedProfile{
				{
					Name: newString("test-name-1"),
					Data: newString("test-data-1"),
				},
				{
					// intentionally out of order in between the two valid profiles
					Name: newString("test-name-3"),
				},
				{
					Name: newString("test-name-2"),
					Data: newString("test-data-2"),
				},
			},
			expected: "72e7e1930db49379e31aa370d4274f9caada231c775a704db7e78dc856e67662",
		},
		{
			name: "skip-no-name",
			profiles: []tunedv1.TunedProfile{
				{
					Name: newString("test-name-1"),
					Data: newString("test-data-1"),
				},
				{
					Name: newString("test-name-2"),
					Data: newString("test-data-2"),
				},
				{
					Data: newString("test-data-3"),
				},
			},
			expected: "72e7e1930db49379e31aa370d4274f9caada231c775a704db7e78dc856e67662",
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got := profilesFingerprint(tt.profiles, tt.recommendedProfile)
			if got != tt.expected {
				t.Errorf("got=%v expected=%v", got, tt.expected)
			}
		})
	}
}

func TestChangeString(t *testing.T) {
	testCases := []struct {
		name     string
		change   Change
		expected string
	}{
		{
			name:     "empty",
			change:   Change{},
			expected: "tuned.Change{}",
		},
		// common cases
		{
			name: "profile",
			change: Change{
				profile: true,
			},
			expected: "tuned.Change{profile:true}",
		},
		{
			name: "profileStatus",
			change: Change{
				profileStatus: true,
			},
			expected: "tuned.Change{profileStatus:true}",
		},
		// check all the fields are represented. Keep me last
		{
			name:     "full",
			change:   fullChange(),
			expected: fmt.Sprintf("%#v", fullChange()),
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.change.String()
			if got != tt.expected {
				t.Errorf("got=%v expected=%v", got, tt.expected)
			}
		})
	}
}

func fullChange() Change {
	return Change{
		profile:            true,
		profileStatus:      true,
		tunedReload:        true,
		nodeRestart:        true,
		debug:              true,
		provider:           "test-provider",
		reapplySysctl:      true,
		recommendedProfile: "test-profile",
		deferredMode:       util.DeferAlways,
		message:            "test-message",
	}
}

func newString(s string) *string {
	return &s
}
