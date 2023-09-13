package schedstat

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestParseDataGetCPUs(t *testing.T) {
	testCases := []struct {
		name          string
		schedStatData string
		expectedCPUs  []string
	}{
		{
			name:          "empty",
			schedStatData: "",
			expectedCPUs:  []string{},
		},
		{
			name:          "simple",
			schedStatData: simpleSchedStat,
			expectedCPUs: []string{
				"cpu0",
				"cpu1",
				"cpu2",
				"cpu3",
				"cpu4",
				"cpu5",
				"cpu6",
				"cpu7",
			},
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseData(strings.NewReader(tt.schedStatData))
			if err != nil {
				t.Errorf("failure: unexpected error %v", err)
			}
			gotCPUs := got.GetCPUs()

			expCPUs := make([]string, len(tt.expectedCPUs))
			copy(expCPUs, tt.expectedCPUs)
			sort.Strings(expCPUs)

			if !reflect.DeepEqual(gotCPUs, expCPUs) {
				t.Errorf("mismatched cpus got %v expected %v", gotCPUs, expCPUs)
			}
		})
	}
}

func TestParseDataHasDomains(t *testing.T) {
	testCases := []struct {
		name            string
		schedStatData   string
		expectedDomains map[string][]string
		expectedError   bool
	}{
		{
			name:            "empty",
			schedStatData:   "",
			expectedDomains: map[string][]string{},
			expectedError:   false,
		},
		{
			name:          "simple",
			schedStatData: simpleSchedStat,
			expectedDomains: map[string][]string{
				"cpu0": {"11", "ff"},
				"cpu1": {"22", "ff"},
				"cpu2": {"44", "ff"},
				"cpu3": {"88", "ff"},
				"cpu4": {"11", "ff"},
				"cpu5": {"22", "ff"},
				"cpu6": {"44", "ff"},
				"cpu7": {"88", "ff"},
			},
			expectedError: false,
		},
		{
			name:          "simple disabled",
			schedStatData: simpleSchedStatDisableBalanced,
			expectedDomains: map[string][]string{
				"cpu0": {"11", "ff"},
				"cpu1": {"22", "ff"},
				"cpu2": {},
				"cpu3": {"88", "ff"},
				"cpu4": {"11", "ff"},
				"cpu5": {"22", "ff"},
				"cpu6": {},
				"cpu7": {"88", "ff"},
			},
			expectedError: false,
		},
		{
			name:          "simple disabled all",
			schedStatData: simpleSchedStatDisableBalancedAll,
			expectedDomains: map[string][]string{
				"cpu0": {},
				"cpu1": {},
				"cpu2": {},
				"cpu3": {},
				"cpu4": {},
				"cpu5": {},
				"cpu6": {},
				"cpu7": {},
			},
			expectedError: false,
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseData(strings.NewReader(tt.schedStatData))
			gotErr := (err != nil)
			if gotErr != tt.expectedError {
				t.Errorf("failure: got error %v expected error %v", err, tt.expectedError)
			}
			for cpu, doms := range tt.expectedDomains {
				gotDoms, ok := got.GetDomains(cpu)
				if !ok {
					t.Errorf("unknown cpu %v", cpu)
				}

				expDoms := make([]string, len(doms))
				copy(expDoms, doms)
				sort.Strings(expDoms)

				if !reflect.DeepEqual(gotDoms, expDoms) {
					t.Errorf("cpu %v expected to have domains %v got %v", cpu, expDoms, gotDoms)
				}
			}
		})
	}
}

var simpleSchedStat string = `version 15
timestamp 4328437744
cpu0 0 0 0 0 0 0 3363757224997 275119326171 32616635
domain0 11 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
cpu1 0 0 0 0 0 0 3257930259442 177915611662 28095631
domain0 22 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
cpu2 0 0 0 0 0 0 2890383859386 179902429182 29632607
domain0 44 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
cpu3 0 0 0 0 0 0 3124153191647 138052433751 22860854
domain0 88 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
cpu4 0 0 0 0 0 0 3019772575212 119367715509 17187184
domain0 11 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
cpu5 0 0 0 0 0 0 2814250511342 139574268225 19597185
domain0 22 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
cpu6 0 0 0 0 0 0 3231608205700 116734060741 16809898
domain0 44 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
cpu7 0 0 0 0 0 0 2874987700096 158494309403 18071885
domain0 88 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0`

var simpleSchedStatDisableBalanced string = `version 15
timestamp 4328437744
cpu0 0 0 0 0 0 0 3363757224997 275119326171 32616635
domain0 11 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
cpu1 0 0 0 0 0 0 3257930259442 177915611662 28095631
domain0 22 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
cpu2 0 0 0 0 0 0 2890383859386 179902429182 29632607
cpu3 0 0 0 0 0 0 3124153191647 138052433751 22860854
domain0 88 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
cpu4 0 0 0 0 0 0 3019772575212 119367715509 17187184
domain0 11 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
cpu5 0 0 0 0 0 0 2814250511342 139574268225 19597185
domain0 22 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
cpu6 0 0 0 0 0 0 3231608205700 116734060741 16809898
cpu7 0 0 0 0 0 0 2874987700096 158494309403 18071885
domain0 88 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
domain1 ff 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0`

var simpleSchedStatDisableBalancedAll string = `version 15
timestamp 4328437744
cpu0 0 0 0 0 0 0 3363757224997 275119326171 32616635
cpu1 0 0 0 0 0 0 3257930259442 177915611662 28095631
cpu2 0 0 0 0 0 0 2890383859386 179902429182 29632607
cpu3 0 0 0 0 0 0 3124153191647 138052433751 22860854
cpu4 0 0 0 0 0 0 3019772575212 119367715509 17187184
cpu5 0 0 0 0 0 0 2814250511342 139574268225 19597185
cpu6 0 0 0 0 0 0 3231608205700 116734060741 16809898
cpu7 0 0 0 0 0 0 2874987700096 158494309403 18071885`
