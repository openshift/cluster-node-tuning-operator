package infrastructure

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
)

// IsVM checks if a given node's underlying infrastructure is a VM
func IsVM(node *corev1.Node) (bool, error) {
	cmd := []string{
		"/bin/bash",
		"-c",
		"systemd-detect-virt > /dev/null ; echo $?",
	}
	output, err := nodes.ExecCommandOnMachineConfigDaemon(node, cmd)
	if err != nil {
		return false, err
	}

	statusCode := strings.TrimSpace(string(output))
	isVM := string(statusCode) == "0"

	return isVM, nil
}
