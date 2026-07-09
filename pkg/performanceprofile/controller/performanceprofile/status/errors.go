package status

import "fmt"

// OvsDpdkCPUsPrerequisiteError is returned when spec.cpu.ovsDpdk is set
// but neither Workload Partitioning (CPUPartitioningAllNodes) nor the
// strict-cpu-reservation Kubelet CPUManager policy option is enabled.
type OvsDpdkCPUsPrerequisiteError struct {
	Message string
}

func (e *OvsDpdkCPUsPrerequisiteError) Error() string {
	return e.Message
}

func NewOvsDpdkCPUsPrerequisiteError() *OvsDpdkCPUsPrerequisiteError {
	return &OvsDpdkCPUsPrerequisiteError{
		Message: fmt.Sprintf("OVS-DPDK CPUs require either Workload Partitioning (CPUPartitioningAllNodes) " +
			"or the strict-cpu-reservation Kubelet CPUManager policy option to be enabled; " +
			"without one of these, Burstable and BestEffort QoS pods can still be scheduled on OVS-DPDK CPUs"),
	}
}
