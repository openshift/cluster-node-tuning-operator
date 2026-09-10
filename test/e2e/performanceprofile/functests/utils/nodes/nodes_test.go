package nodes

import (
	"testing"

	"k8s.io/utils/cpuset"
)

func TestGetTwoSiblingsFromCPUSet(t *testing.T) {
	tests := []struct {
		name        string
		siblings    map[int]map[int][]int
		cpuSet      cpuset.CPUSet
		wantCPUSet  cpuset.CPUSet
		expectError bool
	}{
		{
			name:        "empty siblings map returns error",
			siblings:    map[int]map[int][]int{},
			cpuSet:      cpuset.New(0, 1, 2, 3),
			wantCPUSet:  cpuset.New(),
			expectError: true,
		},
		{
			name:        "nil siblings map returns error",
			siblings:    nil,
			cpuSet:      cpuset.New(0, 1),
			wantCPUSet:  cpuset.New(),
			expectError: true,
		},
		{
			name: "two siblings both in cpuSet returns that sibling set",
			siblings: map[int]map[int][]int{
				0: {0: {0, 16}}, // NUMA 0, core 0: CPUs 0 and 16 (HT pair)
			},
			cpuSet:      cpuset.New(0, 1, 2, 16),
			wantCPUSet:  cpuset.New(0, 16),
			expectError: false,
		},
		{
			name: "two siblings only one in cpuSet returns error",
			siblings: map[int]map[int][]int{
				0: {0: {0, 16}},
			},
			cpuSet:      cpuset.New(0, 1, 2), // 16 not in set
			wantCPUSet:  cpuset.New(),
			expectError: true,
		},
		{
			name: "first core not subset second core is subset returns second",
			siblings: map[int]map[int][]int{
				0: {
					0: {0, 16}, // not in cpuSet
					1: {2, 18}, // both in cpuSet
				},
			},
			cpuSet:      cpuset.New(2, 18, 4, 5),
			wantCPUSet:  cpuset.New(2, 18),
			expectError: false,
		},
		{
			name: "single CPU sibling returns error (wanted 2)",
			siblings: map[int]map[int][]int{
				0: {0: {5}}, // single CPU - core must have exactly 2 siblings
			},
			cpuSet:      cpuset.New(1, 2, 5, 6),
			wantCPUSet:  cpuset.New(),
			expectError: true,
		},
		{
			name: "multiple NUMA nodes first matching core returned",
			siblings: map[int]map[int][]int{
				0: {0: {0, 16}}, // not in cpuSet (no 16)
				1: {0: {1, 17}}, // both in cpuSet
			},
			cpuSet:      cpuset.New(1, 17, 2, 18),
			wantCPUSet:  cpuset.New(1, 17),
			expectError: false,
		},
		{
			name: "no core's siblings are subset of cpuSet returns error",
			siblings: map[int]map[int][]int{
				0: {0: {0, 16}, 1: {2, 18}},
			},
			cpuSet:      cpuset.New(10, 11, 12),
			wantCPUSet:  cpuset.New(),
			expectError: true,
		},
		{
			name: "empty cpuSet returns error",
			siblings: map[int]map[int][]int{
				0: {0: {0, 16}},
			},
			cpuSet:      cpuset.New(),
			wantCPUSet:  cpuset.New(),
			expectError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetTwoSiblingsFromCPUSet(tt.siblings, tt.cpuSet)
			if (err != nil) != tt.expectError {
				t.Errorf("GetTwoSiblingsFromCPUSet() error = %v, expectError %v", err, tt.expectError)
				return
			}
			if !got.Equals(tt.wantCPUSet) {
				t.Errorf("GetTwoSiblingsFromCPUSet() got = %v, want %v", got.String(), tt.wantCPUSet.String())
			}
		})
	}
}

func TestFindCmdlineParam(t *testing.T) {
	tests := []struct {
		name    string
		cmdline string
		key     string
		want    string
	}{
		{
			name:    "parameter found",
			cmdline: "BOOT_IMAGE=/boot/vmlinuz ro isolcpus=managed_irq,2-63 nohz=on",
			key:     "isolcpus",
			want:    "managed_irq,2-63",
		},
		{
			name:    "parameter not found",
			cmdline: "BOOT_IMAGE=/boot/vmlinuz ro nohz=on",
			key:     "isolcpus",
			want:    "",
		},
		{
			name:    "parameter at start",
			cmdline: "isolcpus=domain,managed_irq,1-2 nohz=on",
			key:     "isolcpus",
			want:    "domain,managed_irq,1-2",
		},
		{
			name:    "parameter at end",
			cmdline: "nohz=on systemd.cpu_affinity=0,1",
			key:     "systemd.cpu_affinity",
			want:    "0,1",
		},
		{
			name:    "similar prefix does not match",
			cmdline: "tuned.non_isolcpus_extra=foo tuned.non_isolcpus=00000003",
			key:     "tuned.non_isolcpus",
			want:    "00000003",
		},
		{
			name:    "empty cmdline",
			cmdline: "",
			key:     "isolcpus",
			want:    "",
		},
		{
			name:    "key with no value",
			cmdline: "ro nosoftlockup nohz=on",
			key:     "nosoftlockup",
			want:    "",
		},
		{
			name:    "hex mask value",
			cmdline: "nohz=on tuned.non_isolcpus=00000003 rcu_nocbs=2-63",
			key:     "tuned.non_isolcpus",
			want:    "00000003",
		},
		{
			name:    "real cmdline isolcpus",
			cmdline: "BOOT_IMAGE=(hd0,gpt3)/boot/ostree/rhcos-b5f894dad39b93d3aceb46fd2e8cc92e9391125c539028ee90827c2c9dde949c/vmlinuz-6.12.0-211.39.1.el10_2.x86_64 rw ostree=/ostree/boot.0/rhcos/b5f894dad39b93d3aceb46fd2e8cc92e9391125c539028ee90827c2c9dde949c/0 ignition.platform.id=openstack console=ttyS0,115200n8 console=tty0 root=UUID=b11263d2-70a4-42e5-b560-6b9a858c609e rw rootflags=prjquota boot=UUID=e4674eb0-4b1d-47b6-ab36-c80cec65838d systemd.unified_cgroup_hierarchy=1 cgroup_no_v1=all skew_tick=1 tsc=reliable rcupdate.rcu_normal_after_boot=1 rcutree.nohz_full_patience_delay=1000 nohz=on rcu_nocbs=4-37,40-77 tuned.non_isolcpus=0000c000,00000000,0000000f systemd.cpu_affinity=0,1,2,3,78,79 intel_iommu=on iommu=pt isolcpus=managed_irq,4-37,40-77 nohz_full=4-37,40-77 tsc=reliable nosoftlockup nmi_watchdog=0 mce=off skew_tick=1 rcutree.kthread_prio=11 processor.max_cstate=1 intel_idle.max_cstate=0 idle=poll default_hugepagesz=1G hugepagesz=2M hugepages=20 intel_pstate=disable",
			key:     "isolcpus",
			want:    "managed_irq,4-37,40-77",
		},
		{
			name:    "real cmdline tuned.non_isolcpus",
			cmdline: "BOOT_IMAGE=(hd0,gpt3)/boot/ostree/rhcos-b5f894dad39b93d3aceb46fd2e8cc92e9391125c539028ee90827c2c9dde949c/vmlinuz-6.12.0-211.39.1.el10_2.x86_64 rw ostree=/ostree/boot.0/rhcos/b5f894dad39b93d3aceb46fd2e8cc92e9391125c539028ee90827c2c9dde949c/0 ignition.platform.id=openstack console=ttyS0,115200n8 console=tty0 root=UUID=b11263d2-70a4-42e5-b560-6b9a858c609e rw rootflags=prjquota boot=UUID=e4674eb0-4b1d-47b6-ab36-c80cec65838d systemd.unified_cgroup_hierarchy=1 cgroup_no_v1=all skew_tick=1 tsc=reliable rcupdate.rcu_normal_after_boot=1 rcutree.nohz_full_patience_delay=1000 nohz=on rcu_nocbs=4-37,40-77 tuned.non_isolcpus=0000c000,00000000,0000000f systemd.cpu_affinity=0,1,2,3,78,79 intel_iommu=on iommu=pt isolcpus=managed_irq,4-37,40-77 nohz_full=4-37,40-77 tsc=reliable nosoftlockup nmi_watchdog=0 mce=off skew_tick=1 rcutree.kthread_prio=11 processor.max_cstate=1 intel_idle.max_cstate=0 idle=poll default_hugepagesz=1G hugepagesz=2M hugepages=20 intel_pstate=disable",
			key:     "tuned.non_isolcpus",
			want:    "0000c000,00000000,0000000f",
		},
		{
			name:    "real cmdline systemd.cpu_affinity",
			cmdline: "BOOT_IMAGE=(hd0,gpt3)/boot/ostree/rhcos-b5f894dad39b93d3aceb46fd2e8cc92e9391125c539028ee90827c2c9dde949c/vmlinuz-6.12.0-211.39.1.el10_2.x86_64 rw ostree=/ostree/boot.0/rhcos/b5f894dad39b93d3aceb46fd2e8cc92e9391125c539028ee90827c2c9dde949c/0 ignition.platform.id=openstack console=ttyS0,115200n8 console=tty0 root=UUID=b11263d2-70a4-42e5-b560-6b9a858c609e rw rootflags=prjquota boot=UUID=e4674eb0-4b1d-47b6-ab36-c80cec65838d systemd.unified_cgroup_hierarchy=1 cgroup_no_v1=all skew_tick=1 tsc=reliable rcupdate.rcu_normal_after_boot=1 rcutree.nohz_full_patience_delay=1000 nohz=on rcu_nocbs=4-37,40-77 tuned.non_isolcpus=0000c000,00000000,0000000f systemd.cpu_affinity=0,1,2,3,78,79 intel_iommu=on iommu=pt isolcpus=managed_irq,4-37,40-77 nohz_full=4-37,40-77 tsc=reliable nosoftlockup nmi_watchdog=0 mce=off skew_tick=1 rcutree.kthread_prio=11 processor.max_cstate=1 intel_idle.max_cstate=0 idle=poll default_hugepagesz=1G hugepagesz=2M hugepages=20 intel_pstate=disable",
			key:     "systemd.cpu_affinity",
			want:    "0,1,2,3,78,79",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindCmdlineParam(tt.cmdline, tt.key)
			if got != tt.want {
				t.Errorf("FindCmdlineParam(%q, %q) = %q, want %q", tt.cmdline, tt.key, got, tt.want)
			}
		})
	}
}
