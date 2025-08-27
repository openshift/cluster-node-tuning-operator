package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	o "github.com/onsi/gomega"
)

var (
	fixtureDirLock sync.Once
	fixtureDir     string
)

// Logf is a simple logging function to replace e2e.Logf
func Logf(format string, args ...interface{}) {
	fmt.Printf(format+"\n", args...)
}

// FixturePath returns the path to a test fixture file
func FixturePath(elem ...string) string {
	if len(elem) == 0 {
		panic("must specify path")
	}

	// Determine the base directory for test fixtures
	// This should be the absolute path to test/extended/testdata
	fixtureDirLock.Do(func() {
		// Get the current working directory or package directory
		cwd, err := os.Getwd()
		if err != nil {
			panic(err)
		}

		// Find the project root by looking for go.mod
		projectRoot := cwd
		for {
			if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); err == nil {
				break
			}
			parent := filepath.Dir(projectRoot)
			if parent == projectRoot {
				// Reached filesystem root without finding go.mod
				panic("could not find project root (go.mod)")
			}
			projectRoot = parent
		}

		fixtureDir = filepath.Join(projectRoot, "test", "extended", "testdata")
	})

	// Build the path to the fixture
	var pathComponents []string
	switch {
	case len(elem) > 3 && elem[0] == ".." && elem[1] == ".." && elem[2] == "examples":
		pathComponents = append([]string{filepath.Dir(filepath.Dir(fixtureDir))}, elem[2:]...)
	case len(elem) > 3 && elem[0] == ".." && elem[1] == ".." && elem[2] == "install":
		pathComponents = append([]string{filepath.Dir(filepath.Dir(fixtureDir))}, elem[2:]...)
	case len(elem) > 3 && elem[0] == ".." && elem[1] == "integration":
		pathComponents = append([]string{filepath.Dir(fixtureDir), "test"}, elem[1:]...)
	case elem[0] == "testdata":
		pathComponents = append([]string{fixtureDir}, elem[1:]...)
	default:
		pathComponents = append([]string{fixtureDir}, elem...)
	}

	fullPath := filepath.Join(pathComponents...)

	// Verify the path exists
	if _, err := os.Stat(fullPath); err != nil {
		panic(fmt.Sprintf("Fixture path does not exist: %s (error: %v)", fullPath, err))
	}

	p, err := filepath.Abs(fullPath)
	if err != nil {
		panic(err)
	}
	return p
}

// IsNTOPodInstalled will return true if any pod is found in the given namespace, and false otherwise
func IsNTOPodInstalled(oc *CLI, namespace string) bool {
	Logf("checking if pod is found in namespace %s...", namespace)

	ntoDeployment, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("deployment", "-n", namespace, "-ojsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())

	if len(ntoDeployment) == 0 {
		Logf("no deployment cluster-node-tuning-operator found in namespace %s :(", namespace)
		return false
	}
	Logf("deployment %v found in namespace %s!", ntoDeployment, namespace)
	return true
}

func GetDefaultSMPAffinityBitMaskbyCPUCores(oc *CLI, workerNodeName string) string {
	// Get CPU number in specified worker nodes
	cpuCoresStdOut, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", workerNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(cpuCoresStdOut).NotTo(o.BeEmpty())

	cpuCores, err := strconv.Atoi(cpuCoresStdOut)
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(cpuCoresStdOut).NotTo(o.BeEmpty())

	cpuHexMask := make([]byte, 0, 2)
	if cpuCores%4 != 0 {
		modCPUCoresby4 := cpuCores % 4
		var cpuCoresMask int
		switch modCPUCoresby4 {
		case 3:
			cpuCoresMask = 7
		case 2:
			cpuCoresMask = 3
		case 1:
			cpuCoresMask = 1
		}
		cpuHexMask = append(cpuHexMask, byte(cpuCoresMask))
	}

	for i := 0; i < cpuCores/4; i++ {
		cpuHexMask = append(cpuHexMask, 15)
	}

	cpuHexMaskStr := fmt.Sprintf("%x", cpuHexMask)
	cpuHexMaskFmt := strings.ReplaceAll(cpuHexMaskStr, "0", "")
	Logf("There are %d cores on worker node %s, the hex mask is %s", cpuCores, workerNodeName, cpuHexMaskFmt)

	return cpuHexMaskFmt
}

func ConvertCPUBitMaskToByte(cpuHexMask string) []byte {
	cpuHexMaskChars := []rune(cpuHexMask)
	cpuBitsMask := make([]byte, 0)
	cpuNum := 0
	for i := 0; i < len(cpuHexMaskChars); i++ {
		switch cpuHexMaskChars[i] {
		case 'f':
			cpuBitsMask = append(cpuBitsMask, 15)
			cpuNum = cpuNum + 4
		case '7':
			cpuBitsMask = append(cpuBitsMask, 7)
			cpuNum = cpuNum + 3
		case '3':
			cpuBitsMask = append(cpuBitsMask, 3)
			cpuNum = cpuNum + 2
		case '1':
			cpuBitsMask = append(cpuBitsMask, 1)
			cpuNum = cpuNum + 1
		}
	}
	Logf("The total CPU number is %v\nThe CPU HexMask is:\n%s\nThe CPU BitsMask is:\n%b\n", cpuNum, cpuHexMask, cpuBitsMask)
	return cpuBitsMask
}

func ConvertIsolatedCPURange2CPUList(isolatedCPURange string) []byte {
	// Get a separated cpu number list
	cpuList := make([]byte, 0, 8)
	// From [1,2,4-5,12-17,24-28,30-32]
	// To   [1 2 4 5 12 13 14 15 16 17 24 25 26 27 28 30 31 32]
	cpuRangeList := strings.Split(isolatedCPURange, ",")

	for i := 0; i < len(cpuRangeList); i++ {
		// if CPU range is 12-17 which contain "-"
		if strings.Contains(cpuRangeList[i], "-") {
			// Ignore such senario when cpu setting as 45-,-46
			if strings.HasPrefix(cpuRangeList[i], "-") {
				continue
			}
			// startCPU is 12
			// endCPU is 17
			// the CPU range must be two numbers
			cpuRange := strings.Split(cpuRangeList[i], "-")
			endCPU, _ := strconv.Atoi(cpuRange[1])
			startCPU, _ := strconv.Atoi(cpuRange[0])
			for i := 0; i <= endCPU-startCPU; i++ {
				cpus := startCPU + i
				cpuList = append(cpuList, byte(cpus))
			}
		} else {
			cpus, _ := strconv.Atoi(cpuRangeList[i])
			// Ignore 1,2,<no number>
			if len(cpuRangeList[i]) != 0 {
				cpuList = append(cpuList, byte(cpus))
			}
		}
	}
	return cpuList
}

func AssertIsolateCPUCoresAffectedBitMask(cpuBitsMask []byte, isolatedCPU []byte) string {
	// Isolated CPU Range, 0,1,3-4,11-16,23-27
	//           27 26 25 24 ---------------------------------3 2 1 0
	//           27%6=3
	// [1111     1111         1111 1111 1111 1111 1111         1111] cpuBitMask
	// [0000     1111         1000 0001 1111 1000 0001         1011] isolatedCPU
	// --------------------------------------------------------------
	// [1111     0000         0111 1110 0000 0111 1110         0100] affinityCPUMask
	//  0         1            2    3   4     5   6             7    cpuBitMaskGroupsIndex
	//            6            5    4   3     2   1             0    isolatedCPUIndex
	//     maxValueOfIsolatedCPUIndex
	var affinityCPUMask string
	totalCPUBitMaskGroups := len(cpuBitsMask)
	totalIsolatedCPUNum := len(isolatedCPU)

	Logf("The total isolated CPUs is: %v\n", totalIsolatedCPUNum)
	Logf("The max CPU that isolated is : %v\n", int(isolatedCPU[totalIsolatedCPUNum-1]))

	// The max CPU number is 27, Index is 15
	maxValueOfIsolatedCPUIndex := int(isolatedCPU[totalIsolatedCPUNum-1]) / 4
	Logf("totalCPUGroupNum is: %v\nmaxCPUGroupIndex is: %v\n", totalCPUBitMaskGroups, maxValueOfIsolatedCPUIndex)
	maxValueOfCPUBitMaskGroupsIndex := totalCPUBitMaskGroups - 1
	for i := totalIsolatedCPUNum - 1; i >= 0; i-- {
		isolatedCPUIndex := int(isolatedCPU[i]) / 4

		cpuBitsMaskIndex := maxValueOfCPUBitMaskGroupsIndex - isolatedCPUIndex
		// 3 => 1000 2=>0100 1=>0010 0=>0000
		modIsolatedCPUby4 := int(isolatedCPU[i] % 4)
		var isolatedCPUMask int
		switch modIsolatedCPUby4 {
		case 3:
			isolatedCPUMask = 8
		case 2:
			isolatedCPUMask = 4
		case 1:
			isolatedCPUMask = 2
		case 0:
			isolatedCPUMask = 1
		}

		valueOfCPUBitsMaskOnIndex := int(cpuBitsMask[cpuBitsMaskIndex]) ^ isolatedCPUMask
		Logf("%04b ^ %04b = %04b\n", cpuBitsMask[cpuBitsMaskIndex], isolatedCPUMask, valueOfCPUBitsMaskOnIndex)
		cpuBitsMask[cpuBitsMaskIndex] = byte(valueOfCPUBitsMaskOnIndex)
	}
	cpuBitsMaskStr := fmt.Sprintf("%x", cpuBitsMask)
	affinityCPUMask = strings.ReplaceAll(cpuBitsMaskStr, "0", "")
	Logf("affinityCPUMask is: %s\n", affinityCPUMask)
	return affinityCPUMask
}

func AssertDefaultIRQSMPAffinityAffectedBitMask(cpuBitsMask []byte, isolatedCPU []byte, defaultIRQSMPAffinity string) bool {
	defaultIRQSMPAffinity = strings.ReplaceAll(defaultIRQSMPAffinity, "\n", "")

	// Isolated CPU Range, 0,1,3-4,11-16,23-27
	//           27 26 25 24 ---------------------------------3 2 1 0
	//           27%6=3
	// [1111     1111         1111 1111 1111 1111 1111         1111] cpuBitMask
	// [0000     1111         1000 0001 1111 1000 0001         1011] isolatedCPU
	// --------------------------------------------------------------
	// [0000     1111         1000 0001 1111 1000 0001         1011] affinityCPUMask
	//  0         1            2    3   4     5   6             7    cpuBitMaskGroupsIndex
	//            6            5    4   3     2   1             0    isolatedCPUIndex
	//     maxValueOfIsolatedCPUIndex

	var affinityCPUMask string
	var isMatch bool
	totalCPUBitMaskGroups := len(cpuBitsMask)
	totalIsolatedCPUNum := len(isolatedCPU)

	Logf("The total isolated CPUs is: %v\n", totalIsolatedCPUNum)
	Logf("The max CPU that isolated is : %v\n", int(isolatedCPU[totalIsolatedCPUNum-1]))
	isolatedCPUMaskGroup := make([]byte, totalCPUBitMaskGroups)

	Logf("The initial isolatedCPUMask is %04b\n", isolatedCPUMaskGroup)

	maxValueOfCPUBitMaskGroupsIndex := totalCPUBitMaskGroups - 1
	for i := totalIsolatedCPUNum - 1; i >= 0; i-- {
		isolatedCPUIndex := int(isolatedCPU[i]) / 4

		cpuBitsMaskIndex := maxValueOfCPUBitMaskGroupsIndex - isolatedCPUIndex

		// 3 => 1000 2=>0100 1=>0010 0=>0000
		modIsolatedCPUby4 := int(isolatedCPU[i] % 4)
		var isolatedCPUMask int
		switch modIsolatedCPUby4 {
		case 3:
			isolatedCPUMask = 8
		case 2:
			isolatedCPUMask = 4
		case 1:
			isolatedCPUMask = 2
		case 0:
			isolatedCPUMask = 1
		}

		Logf("%04b | %04b = %04b\n", isolatedCPUMaskGroup[cpuBitsMaskIndex], isolatedCPUMask, int(isolatedCPUMaskGroup[cpuBitsMaskIndex])|isolatedCPUMask)
		valueOfCPUBitsMaskOnIndex := int(isolatedCPUMaskGroup[cpuBitsMaskIndex]) | isolatedCPUMask
		isolatedCPUMaskGroup[cpuBitsMaskIndex] = byte(valueOfCPUBitsMaskOnIndex)
	}
	// Remove additional 0 in the isolatedCPUMaskGroup
	Logf("cpuBitsMask is: %04b\n", isolatedCPUMaskGroup)
	cpuBitsMaskStr := fmt.Sprintf("%x", isolatedCPUMaskGroup)
	cpuBitsMaskRune := []rune(cpuBitsMaskStr)
	bitsMaskChars := make([]byte, 0, 2)

	for i := 1; i < len(cpuBitsMaskRune); i = i + 2 {
		bitsMaskChars = append(bitsMaskChars, byte(cpuBitsMaskRune[i]))
	}
	affinityCPUMask = string(bitsMaskChars)
	// If defaultIRQSMPAffinity start with 0, ie, 00020, remove 000 and change to 20
	if strings.HasPrefix(defaultIRQSMPAffinity, "0") || strings.HasPrefix(affinityCPUMask, "0") {
		defaultIRQSMPAffinity = strings.TrimLeft(defaultIRQSMPAffinity, "0")
		affinityCPUMask = strings.TrimLeft(affinityCPUMask, "0")
	}

	Logf("affinityCPUMask is: -%s-, defaultIRQSMPAffinity is -%s-\n", affinityCPUMask, defaultIRQSMPAffinity)
	if affinityCPUMask == defaultIRQSMPAffinity {
		isMatch = true
	}
	return isMatch
}

type NtoResource struct {
	Name        string
	Namespace   string
	Template    string
	Sysctlparm  string
	Sysctlvalue string
}

func (ntoRes *NtoResource) CreateIRQSMPAffinityProfileIfNotExist(oc *CLI) {
	output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", ntoRes.Name, "-n", ntoRes.Namespace).Output()
	if strings.Contains(output, "NotFound") || strings.Contains(output, "No resources") || err != nil {
		Logf("no tuned in project: %s, create one: %s", ntoRes.Namespace, ntoRes.Name)
		processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args("--ignore-unknown-parameters=true", "-f", ntoRes.Template, "-p", "TUNED_NAME="+ntoRes.Name, "-p", "SYSCTLPARM="+ntoRes.Sysctlparm, "-p", "SYSCTLVALUE="+ntoRes.Sysctlvalue, "-o", "yaml").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		Logf("Processed template:\n%s", processedTemplate)
		err = oc.AsAdmin().WithoutNamespace().Run("create").Args("-n", ntoRes.Namespace, "-f", "-").InputString(processedTemplate).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		Logf("Successfully created tuned resource: %s in namespace: %s", ntoRes.Name, ntoRes.Namespace)

		// Wait for the Tuned resource to be created and ready
		o.Eventually(func() bool {
			output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", ntoRes.Name, "-n", ntoRes.Namespace).Output()
			if err != nil || strings.Contains(output, "NotFound") {
				Logf("Tuned resource not yet available: %v", err)
				return false
			}
			Logf("Tuned resource %s is now available", ntoRes.Name)
			return true
		}, "30s", "5s").Should(o.BeTrue(), "Tuned resource should be created")
	} else {
		Logf("already exist %v in project: %s", ntoRes.Name, ntoRes.Namespace)
	}
}

func (ntoRes *NtoResource) Delete(oc *CLI) {
	_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoRes.Namespace, "tuned", ntoRes.Name, "--ignore-not-found").Execute()
}

// AssertIfTunedProfileApplied checks the logs for a given tuned pod in a given namespace to see if the expected profile was applied
func (ntoRes *NtoResource) AssertIfTunedProfileApplied(oc *CLI, namespace string, tunedNodeName string, tunedName string, expectedAppliedStatus string) {
	Logf("Checking if tuned profile %s is applied to node %s", tunedName, tunedNodeName)

	// First check if the Profile resource exists
	o.Eventually(func() bool {
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", tunedNodeName).Output()
		if err != nil || strings.Contains(output, "NotFound") {
			Logf("Profile resource %s not found yet in namespace %s: %v", tunedNodeName, namespace, err)
			return false
		}
		Logf("Profile resource %s found in namespace %s", tunedNodeName, namespace)
		return true
	}, "60s", "5s").Should(o.BeTrue(), "Profile resource should exist for node "+tunedNodeName)

	// Then check if the profile is applied with the correct status
	o.Eventually(func() bool {
		appliedStatus, err1 := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", tunedNodeName, `-ojsonpath='{.status.conditions[?(@.type=="Applied")].status}'`).Output()
		tunedProfile, err2 := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", tunedNodeName, "-ojsonpath={.status.tunedProfile}").Output()

		Logf("Checking profile status: appliedStatus=%s (err=%v), tunedProfile=%s (err=%v), expectedStatus=%s, expectedProfile=%s",
			appliedStatus, err1, tunedProfile, err2, expectedAppliedStatus, tunedName)

		if err1 != nil || err2 != nil {
			Logf("Error getting profile status: err1=%v, err2=%v", err1, err2)
			return false
		}

		if !strings.Contains(appliedStatus, expectedAppliedStatus) || strings.Contains(appliedStatus, "Unknown") {
			Logf("Applied status not matching: got %s, want %s", appliedStatus, expectedAppliedStatus)
			return false
		}

		if tunedProfile != tunedName {
			Logf("Tuned profile name not matching: got %s, want %s", tunedProfile, tunedName)
			return false
		}

		Logf("Profile %s successfully applied to node %s with status %s", tunedName, tunedNodeName, appliedStatus)
		return true
	}, "180s", "5s").Should(o.BeTrue(), fmt.Sprintf("Profile %s should be applied to node %s", tunedName, tunedNodeName))
}

func ChoseOneWorkerNodeToRunCase(oc *CLI, choseBy int) string {
	// Prior to choose worker nodes with machineset
	var tunedNodeName string
	machinesetOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", "-n", "openshift-machine-api", "-o=jsonpath={.items[*].metadata.name}").Output()
	machineSetExists := (err == nil && len(machinesetOutput) > 0)

	if machineSetExists {
		machinesetName := GetWorkerMachinesetName(oc, choseBy)
		Logf("machinesetName is %v in ChoseOneWorkerNodeToRunCase", machinesetName)

		if len(machinesetName) != 0 {
			machinesetReplicas, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", "-n", "openshift-machine-api", machinesetName, "-o=jsonpath={.spec.replicas}").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			if !strings.Contains(machinesetReplicas, "0") {
				// First check if any nodes exist with this machineset label
				nodeNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "machine.openshift.io/cluster-api-machineset="+machinesetName, "-o=jsonpath={.items[*].metadata.name}").Output()
				o.Expect(err).NotTo(o.HaveOccurred())
				if len(strings.TrimSpace(nodeNames)) > 0 {
					// Get the first node name
					nodeList := strings.Fields(nodeNames)
					tunedNodeName = nodeList[0]
					o.Expect(tunedNodeName).NotTo(o.BeEmpty())
				} else {
					Logf("No nodes found for machineset %s, falling back to node selection without machineset", machinesetName)
					tunedNodeName = ChoseOneWorkerNodeNotByMachineset(oc, choseBy)
				}
			} else {
				tunedNodeName = ChoseOneWorkerNodeNotByMachineset(oc, choseBy)
			}
		} else {
			tunedNodeName = ChoseOneWorkerNodeNotByMachineset(oc, choseBy)
		}
	} else {
		tunedNodeName = ChoseOneWorkerNodeNotByMachineset(oc, choseBy)
		Logf("the tunedNodeName that we get inside ChoseOneWorkerNodeToRunCase when choseBy %v is %v ", choseBy, tunedNodeName)
	}
	return tunedNodeName
}

// GetLinuxWorkerMachinesets returns a list of Linux worker machinesets, filtering out Windows and edge nodes
func GetLinuxWorkerMachinesets(oc *CLI) []string {
	machinesetList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", "openshift-machine-api", "machineset", "-ojsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())

	workerMachineSets := strings.Split(machinesetList, " ")
	Logf("workerMachineSets is %v", workerMachineSets)

	linuxMachineset := make([]string, 0, len(workerMachineSets))
	for _, machineset := range workerMachineSets {
		// Skip windows and edge nodes
		if strings.Contains(machineset, "windows") || strings.Contains(machineset, "edge") {
			Logf("skip windows or edge node [ %v ]", machineset)
		} else if len(machineset) > 0 {
			linuxMachineset = append(linuxMachineset, machineset)
		}
	}
	Logf("linuxMachineset is %v", linuxMachineset)
	return linuxMachineset
}

func GetWorkerMachinesetName(oc *CLI, machineseetSN int) string {
	linuxMachineset := GetLinuxWorkerMachinesets(oc)

	var machinesetName string
	if machineseetSN < len(linuxMachineset) {
		machinesetName = linuxMachineset[machineseetSN]
	}

	Logf("machinesetName is %v in GetWorkerMachinesetName", machinesetName)
	return machinesetName
}

func ChoseOneWorkerNodeNotByMachineset(oc *CLI, choseBy int) string {
	var tunedNodeName string
	workerNodesStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=,kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	workerNodes := strings.Fields(workerNodesStr)
	o.Expect(len(workerNodes)).To(o.BeNumerically(">", 0), "No worker nodes found")

	switch choseBy {
	// 0 means the first worker node, 1 means the last worker node
	case 0:
		tunedNodeName = workerNodes[0]
		Logf("the tunedNodeName that we get inside ChoseOneWorkerNodeNotByMachineset when choseBy 0 is %v ", tunedNodeName)
		o.Expect(tunedNodeName).NotTo(o.BeEmpty())
	case 1:
		tunedNodeName = workerNodes[len(workerNodes)-1]
		Logf("the tunedNodeName that we get inside ChoseOneWorkerNodeNotByMachineset when choseBy 1 is %v ", tunedNodeName)
		o.Expect(tunedNodeName).NotTo(o.BeEmpty())
	default:
		Logf("Invalid parameter for choseBy is %v ", choseBy)
	}
	return tunedNodeName
}

// Helper functions for NTO extended tests (from nto_helpers.go)

func GetFirstLinuxWorkerNode(oc *CLI) (string, error) {
	workerNodesStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=,kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return "", err
	}
	nodes := strings.Fields(strings.TrimSpace(workerNodesStr))
	if len(nodes) == 0 {
		return "", fmt.Errorf("no Linux worker nodes found")
	}
	return nodes[0], nil
}

func IsSNOCluster(oc *CLI) bool {
	nodeCount, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "--no-headers").Output()
	if err != nil {
		return false
	}
	return len(strings.Split(strings.TrimSpace(nodeCount), "\n")) == 1
}

func DebugNodeRetryWithOptionsAndChrootWithStdErr(oc *CLI, nodeName string, options []string, command ...string) (string, string, error) {
	stdout, stderr, err := DebugNode(oc, nodeName, options, true, true, command...)
	return stdout, stderr, err
}

// StringsSliceElementsHasPrefix checks if any element in the slice has the given prefix
func StringsSliceElementsHasPrefix(slice []string, prefix string, caseSensitive bool) (bool, int) {
	for i, s := range slice {
		if caseSensitive {
			if strings.HasPrefix(s, prefix) {
				return true, i
			}
		} else {
			if strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix)) {
				return true, i
			}
		}
	}
	return false, -1
}

// IsNamespacePrivileged checks if a namespace has privileged security context constraints
func IsNamespacePrivileged(oc *CLI, namespace string) (bool, error) {
	labels, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("namespace", namespace, "-o=jsonpath={.metadata.labels}").Output()
	if err != nil {
		return false, err
	}
	return strings.Contains(labels, "pod-security.kubernetes.io/enforce:privileged") ||
		strings.Contains(labels, "security.openshift.io/scc.podSecurityLabelSync:false"), nil
}

// SetNamespacePrivileged sets privileged labels on a namespace
func SetNamespacePrivileged(oc *CLI, namespace string) error {
	err := oc.AsAdmin().WithoutNamespace().Run("label").Args("namespace", namespace, "pod-security.kubernetes.io/enforce=privileged", "pod-security.kubernetes.io/audit=privileged", "pod-security.kubernetes.io/warn=privileged", "security.openshift.io/scc.podSecurityLabelSync=false", "--overwrite").Execute()
	return err
}

// RecoverNamespaceRestricted recovers namespace to restricted labels
func RecoverNamespaceRestricted(oc *CLI, namespace string) {
	_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("namespace", namespace, "pod-security.kubernetes.io/enforce-", "pod-security.kubernetes.io/audit-", "pod-security.kubernetes.io/warn-", "security.openshift.io/scc.podSecurityLabelSync-").Execute()
}

// IsDefaultNodeSelectorEnabled checks if default node selector is enabled
func IsDefaultNodeSelectorEnabled(oc *CLI) bool {
	selector, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("scheduler", "cluster", "-o=jsonpath={.spec.defaultNodeSelector}").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(selector) != ""
}

// IsWorkerNode checks if a node has the worker role
func IsWorkerNode(oc *CLI, nodeName string) bool {
	labels, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", nodeName, "-o=jsonpath={.metadata.labels}").Output()
	if err != nil {
		return false
	}
	return strings.Contains(labels, "node-role.kubernetes.io/worker")
}

// IsSpecifiedAnnotationKeyExist checks if a specific annotation key exists on a resource
func IsSpecifiedAnnotationKeyExist(oc *CLI, resource, namespace, annotationKey string) bool {
	args := []string{resource}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, "-o=jsonpath={.metadata.annotations}")
	annotations, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(args...).Output()
	if err != nil {
		return false
	}
	return strings.Contains(annotations, annotationKey)
}

// AddAnnotationsToSpecificResource adds annotations to a specific resource
func AddAnnotationsToSpecificResource(oc *CLI, resource, namespace, annotation string) error {
	args := []string{resource}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, annotation, "--overwrite")
	return oc.AsAdmin().WithoutNamespace().Run("annotate").Args(args...).Execute()
}

// RemoveAnnotationFromSpecificResource removes an annotation from a specific resource
func RemoveAnnotationFromSpecificResource(oc *CLI, resource, namespace, annotationKey string) error {
	args := []string{resource}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, annotationKey+"-")
	return oc.AsAdmin().WithoutNamespace().Run("annotate").Args(args...).Execute()
}

// DebugNode is the core function for launching debug containers
func DebugNode(oc *CLI, nodeName string, cmdOptions []string, needChroot bool, recoverNsLabels bool, cmd ...string) (stdOut string, stdErr string, err error) {
	var (
		debugNodeNamespace string
		isNsPrivileged     bool
		cargs              []string
		outputError        error
	)

	cargs = []string{"node/" + nodeName}

	// Enhance for debug node namespace used logic
	// if "--to-namespace=" option is used, then uses the input options' namespace, otherwise use oc.Namespace()
	// if oc.Namespace() is empty, uses "default" namespace instead
	hasToNamespaceInCmdOptions, index := StringsSliceElementsHasPrefix(cmdOptions, "--to-namespace=", false)
	if hasToNamespaceInCmdOptions {
		debugNodeNamespace = strings.TrimPrefix(cmdOptions[index], "--to-namespace=")
	} else {
		debugNodeNamespace = oc.Namespace()
		if debugNodeNamespace == "" {
			debugNodeNamespace = "default"
		}
	}

	// Running oc debug node command in normal projects
	// (normal projects mean projects that are not clusters default projects like: "openshift-xxx" et al)
	// need extra configuration on 4.12+ ocp test clusters
	// https://github.com/openshift/oc/blob/master/pkg/helpers/cmd/errors.go#L24-L29
	if !strings.HasPrefix(debugNodeNamespace, "openshift-") {
		isNsPrivileged, outputError = IsNamespacePrivileged(oc, debugNodeNamespace)
		if outputError != nil {
			return "", "", outputError
		}
		if !isNsPrivileged {
			if recoverNsLabels {
				defer RecoverNamespaceRestricted(oc, debugNodeNamespace)
			}
			outputError = SetNamespacePrivileged(oc, debugNodeNamespace)
			if outputError != nil {
				return "", "", outputError
			}
		}
	}

	// For default nodeSelector enabled test clusters we need to add the extra annotation to avoid the debug pod's
	// nodeSelector overwritten by the scheduler
	if IsDefaultNodeSelectorEnabled(oc) && !IsWorkerNode(oc, nodeName) && !IsSpecifiedAnnotationKeyExist(oc, "ns/"+debugNodeNamespace, "", `openshift.io/node-selector`) {
		if err := AddAnnotationsToSpecificResource(oc, "ns/"+debugNodeNamespace, "", `openshift.io/node-selector=`); err != nil {
			Logf("Warning: failed to add annotation: %v", err)
		}
		defer func() {
			if err := RemoveAnnotationFromSpecificResource(oc, "ns/"+debugNodeNamespace, "", `openshift.io/node-selector`); err != nil {
				Logf("Warning: failed to remove annotation: %v", err)
			}
		}()
	}

	if len(cmdOptions) > 0 {
		cargs = append(cargs, cmdOptions...)
	}
	if !hasToNamespaceInCmdOptions {
		cargs = append(cargs, "--to-namespace="+debugNodeNamespace)
	}
	if needChroot {
		cargs = append(cargs, "--", "chroot", "/host")
	} else {
		cargs = append(cargs, "--")
	}
	cargs = append(cargs, cmd...)

	return oc.AsAdmin().WithoutNamespace().Run("debug").Args(cargs...).Outputs()
}
