package status

import "fmt"

// DedicatedCPUsPrerequisiteError is returned when spec.cpu.dedicated is set
// but neither Workload Partitioning (CPUPartitioningAllNodes) nor the
// strict-cpu-reservation Kubelet CPUManager policy option is enabled.
type DedicatedCPUsPrerequisiteError struct {
	Message string
}

func (e *DedicatedCPUsPrerequisiteError) Error() string {
	return e.Message
}

func NewDedicatedCPUsPrerequisiteError() *DedicatedCPUsPrerequisiteError {
	return &DedicatedCPUsPrerequisiteError{
		Message: fmt.Sprintf("dedicated CPUs require either Workload Partitioning (CPUPartitioningAllNodes) " +
			"or the strict-cpu-reservation Kubelet CPUManager policy option to be enabled; " +
			"without one of these, Burstable and BestEffort QoS pods can still be scheduled on dedicated CPUs"),
	}
}
