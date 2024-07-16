package operator

import (
	"reflect"
	"testing"

	ntomf "github.com/openshift/cluster-node-tuning-operator/pkg/manifests"
)

func TestUpdateOperandDaemonSetFromConfig(t *testing.T) {
	testCases := []struct {
		desc            string
		opCfg           OperandConfig
		expectedCommand []string
	}{
		{
			desc:            "nil config",
			expectedCommand: []string{"/usr/bin/cluster-node-tuning-operator", "openshift-tuned", "--in-cluster", "-v=0"},
		},
		{
			desc: "set verbose config",
			opCfg: OperandConfig{
				Verbose: 5,
			},
			expectedCommand: []string{"/usr/bin/cluster-node-tuning-operator", "openshift-tuned", "--in-cluster", "-v=5"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			dsMf := ntomf.TunedDaemonSet()
			updateOperandDaemonSetFromConfig(dsMf, &tc.opCfg)
			cmd := dsMf.Spec.Template.Spec.Containers[0].Command // shortcut
			if !reflect.DeepEqual(tc.expectedCommand, cmd) {
				t.Errorf("wrong command %v expected %v", cmd, tc.expectedCommand)
			}
		})
	}

}
