package controller

const CgroupMountPoint = "/sys/fs/cgroup"

type CpuSet struct {
	Cpus      string
	Mems      string
	Effective string
	// Partition only applicable for cgroupv2
	Partition string
	Exclusive string
	// SchedLoadBalance true if enabled, only applicable for cgroupv1
	SchedLoadBalance bool
}

type Cpu struct {
	Quota   string
	Period  string
	cpuStat map[string]string
}
