// nto_util.go provides NTO-specific utilities: NtoResource CRUD operations,
// sysctl comparison and parsing, tuned profile management, NTO-specific wait
// helpers and assertions, certificate comparison, PAO profile verification,
// and sysctl comparison helpers.

package utils

import (
	"bytes"
	"context"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	extendedbindata "github.com/openshift/cluster-node-tuning-operator/test/extended/bindata"

	"k8s.io/apimachinery/pkg/util/wait"
)

// NtoResource holds the fields needed to create and manage NTO test resources.
type NtoResource struct {
	Name          string
	Namespace     string
	Template      string
	SysctlParam   string
	SysctlValue   string
	DeferredValue string
	Label         string
}

// bindataAssetKey returns the go-bindata asset name (paths under the testdata tree; see Makefile -prefix).
func bindataAssetKey(elem []string) string {
	key := filepath.ToSlash(filepath.Join(elem...))
	if key == "testdata" {
		return ""
	}
	return strings.TrimPrefix(key, "testdata/")
}

// TestdataFixturePathBase materializes a manifest from go-bindata into a file under the given
// base directory.  The caller is responsible for cleaning up baseDir.
func TestdataFixturePathBase(baseDir string, elem ...string) (string, error) {
	if len(elem) == 0 {
		return "", fmt.Errorf("must specify path")
	}
	key := bindataAssetKey(elem)
	data, err := extendedbindata.Asset(key)
	if err != nil {
		return "", fmt.Errorf("TestdataFixturePathBase: unknown asset %q (from %v): %v", key, elem, err)
	}
	dest := filepath.Join(baseDir, filepath.FromSlash(key))
	if existing, statErr := os.ReadFile(dest); statErr == nil && bytes.Equal(existing, data) {
		// file already materialized with current content — reuse it
		p, absErr := filepath.Abs(dest)
		if absErr != nil {
			return "", absErr
		}
		return p, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
		return "", fmt.Errorf("TestdataFixturePathBase: mkdir: %v", err)
	}
	if err := os.WriteFile(dest, data, 0644); err != nil {
		return "", fmt.Errorf("TestdataFixturePathBase: write %q: %v", dest, err)
	}
	p, absErr := filepath.Abs(dest)
	if absErr != nil {
		return "", absErr
	}
	return p, nil
}

// IsNTOInstalled reports whether the NTO operator deployment exists in the given namespace
// and has at least one ready replica. A deployment can exist but be in a Terminating or
// unavailable state; this check ensures it is actually functional before tests proceed.
func IsNTOInstalled(oc *CLI, namespace string) (bool, error) {
	const ntoDeploymentName = "cluster-node-tuning-operator"
	ntoDeployment, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("deployment", ntoDeploymentName, "-n", namespace, "-ojsonpath={.metadata.name}").Output()
	if err != nil {
		return false, err
	}
	if ntoDeployment != ntoDeploymentName {
		return false, nil
	}
	readyReplicas, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("deployment", ntoDeploymentName, "-n", namespace, "-ojsonpath={.status.readyReplicas}").Output()
	if err != nil {
		return false, fmt.Errorf("failed to get ready replicas for deployment %s: %w", ntoDeploymentName, err)
	}
	ready, err := strconv.Atoi(readyReplicas)
	if err != nil {
		return false, fmt.Errorf("failed to parse readyReplicas for deployment %s: %w", ntoDeploymentName, err)
	}
	return ready > 0, nil
}

// GetDefaultSMPAffinityBitMaskByCPUCores computes a hexadecimal CPU bitmask
// string with bits 0..cpuCores-1 set, representing the default SMP affinity
// for a worker node with the given number of CPU cores.
func GetDefaultSMPAffinityBitMaskByCPUCores(oc *CLI, workerNodeName string) (string, error) {
	cpuCoresStdOut, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", workerNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get CPU cores for node %s: %w", workerNodeName, err)
	}
	if cpuCoresStdOut == "" {
		return "", fmt.Errorf("CPU cores output is empty for node %s", workerNodeName)
	}

	cpuCores, err := strconv.Atoi(cpuCoresStdOut)
	if err != nil {
		return "", fmt.Errorf("failed to parse CPU cores %q for node %s: %w", cpuCoresStdOut, workerNodeName, err)
	}
	if cpuCores < 1 {
		return "", fmt.Errorf("invalid CPU cores %d for node %s: must be at least 1", cpuCores, workerNodeName)
	}

	// Build a bitmask with bits 0..cpuCores-1 all set, then format as hex.
	// Use big.Int to support CPU counts >= 64 without overflow.
	mask := new(big.Int).Lsh(big.NewInt(1), uint(cpuCores))
	mask.Sub(mask, big.NewInt(1))
	cpuHexMaskFmt := strings.TrimLeft(mask.Text(16), "0")
	Logf("there are %d cores on worker node %s, the hex mask is %s", cpuCores, workerNodeName, cpuHexMaskFmt)

	return cpuHexMaskFmt, nil
}

// ConvertCPUBitMaskToByte converts a hexadecimal CPU bitmask string to a byte slice
// where each byte represents the decimal value of the corresponding hex character.
// For example, "f" becomes []byte{15} and "ff" becomes []byte{15, 15}.
// The input is case-insensitive and validated for valid hex characters (0-9, a-f).
func ConvertCPUBitMaskToByte(cpuHexMask string) ([]byte, error) {
	if cpuHexMask == "" {
		return nil, fmt.Errorf("empty hex mask")
	}

	cpuHexMask = strings.ToLower(cpuHexMask)

	for _, c := range cpuHexMask {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return nil, fmt.Errorf("invalid hex character: %c", c)
		}
	}

	cpuBitsMask := make([]byte, 0, len(cpuHexMask))
	for i := 0; i < len(cpuHexMask); i++ {
		c := cpuHexMask[i]
		var value byte
		switch {
		case c >= '0' && c <= '9':
			value = c - '0'
		case c >= 'a' && c <= 'f':
			value = c - 'a' + 10
		}
		cpuBitsMask = append(cpuBitsMask, value)
	}
	var bitsMaskStr strings.Builder
	for _, b := range cpuBitsMask {
		fmt.Fprintf(&bitsMaskStr, "%04b", b)
	}
	Logf("The CPU HexMask is:\n%s\nThe CPU BitsMask is:\n%s\n", cpuHexMask, bitsMaskStr.String())
	return cpuBitsMask, nil
}

// ConvertIsolatedCPURange2CPUList converts a sysctl-style CPU range string
// into a flat list of individual CPU indices.
func ConvertIsolatedCPURange2CPUList(isolatedCPURange string) ([]int, error) {
	// Get a separated cpu number list
	cpuList := make([]int, 0, 8)
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
			if len(cpuRange) != 2 {
				return nil, fmt.Errorf("invalid CPU range %q: expected exactly two elements", cpuRangeList[i])
			}
			startCPU, err := strconv.Atoi(cpuRange[0])
			if err != nil {
				return nil, fmt.Errorf("invalid start CPU %q in range %q: %w", cpuRange[0], cpuRangeList[i], err)
			}
			endCPU, err := strconv.Atoi(cpuRange[1])
			if err != nil {
				return nil, fmt.Errorf("invalid end CPU %q in range %q: %w", cpuRange[1], cpuRangeList[i], err)
			}
			if endCPU < startCPU {
				return nil, fmt.Errorf("invalid CPU range %q: end CPU %d is less than start CPU %d", cpuRangeList[i], endCPU, startCPU)
			}
			for j := 0; j <= endCPU-startCPU; j++ {
				cpus := startCPU + j
				cpuList = append(cpuList, cpus)
			}
		} else {
			cpus, err := strconv.Atoi(cpuRangeList[i])
			if err != nil {
				return nil, fmt.Errorf("invalid CPU %q: %w", cpuRangeList[i], err)
			}
			cpuList = append(cpuList, cpus)
		}
	}
	return cpuList, nil
}

// normalizeAffinityMask strips whitespace and leading zeros from a CPU
// affinity bitmask string, collapsing an all-zero value to "0" so that
// trimming never produces an empty string that could spuriously match an
// unrelated mask.
func normalizeAffinityMask(mask string) string {
	mask = strings.TrimSpace(mask)
	mask = strings.TrimLeft(mask, "0")
	if mask == "" {
		mask = "0"
	}
	return mask
}

// AssertIsolateCPUCoresAffectedBitMask checks whether the actual IRQ SMP
// affinity mask on a worker node matches the affinity mask that results from
// isolating the given CPUs from the full CPU bitmask. Returns true if the
// values match, false otherwise.
func AssertIsolateCPUCoresAffectedBitMask(cpuBitsMask []byte, isolatedCPU []int, actualSMPAffinity string) (bool, error) {
	// Isolated CPU Range, 0,1,3-4,11-16,23-27
	//           27 26 25 24 ---------------------------------3 2 1 0
	//           27%4=3
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

	Logf("the total isolated CPUs is: %v\n", totalIsolatedCPUNum)
	if totalIsolatedCPUNum == 0 {
		return false, fmt.Errorf("no isolated CPUs provided, cannot compute affinity mask")
	}
	Logf("the max CPU that isolated is : %v\n", isolatedCPU[totalIsolatedCPUNum-1])

	// Work on a copy to avoid mutating the caller's slice.
	workingMask := make([]byte, totalCPUBitMaskGroups)
	copy(workingMask, cpuBitsMask)

	// The max CPU number is 27, Index is 15
	maxValueOfIsolatedCPUIndex := isolatedCPU[totalIsolatedCPUNum-1] / 4
	Logf("totalCPUGroupNum is: %v\nmaxCPUGroupIndex is: %v\n", totalCPUBitMaskGroups, maxValueOfIsolatedCPUIndex)
	maxValueOfCPUBitMaskGroupsIndex := totalCPUBitMaskGroups - 1
	for i := totalIsolatedCPUNum - 1; i >= 0; i-- {
		isolatedCPUIndex := isolatedCPU[i] / 4

		cpuBitsMaskIndex := maxValueOfCPUBitMaskGroupsIndex - isolatedCPUIndex
		// modIsolatedCPUby4 is 0-3; the corresponding bit mask is 1<<modIsolatedCPUby4
		// (0=>0001, 1=>0010, 2=>0100, 3=>1000)
		modIsolatedCPUby4 := isolatedCPU[i] % 4
		isolatedCPUMask := 1 << modIsolatedCPUby4

		valueOfCPUBitsMaskOnIndex := int(workingMask[cpuBitsMaskIndex]) ^ isolatedCPUMask
		Logf("%04b ^ %04b = %04b\n", workingMask[cpuBitsMaskIndex], isolatedCPUMask, valueOfCPUBitsMaskOnIndex)
		workingMask[cpuBitsMaskIndex] = byte(valueOfCPUBitsMaskOnIndex)
	}
	cpuBitsMaskStr := fmt.Sprintf("%x", workingMask)
	// Each byte of workingMask holds a nibble (0-15), but %x renders it as two
	// hex chars (e.g., 0x0f -> "0f"). Take the lower nibble of each byte and
	// strip leading zeros to match the kernel's compact hex representation.
	cpuBitsMaskRune := []rune(cpuBitsMaskStr)
	bitsMaskChars := make([]byte, 0, len(cpuBitsMaskRune)/2)
	for i := 1; i < len(cpuBitsMaskRune); i = i + 2 {
		bitsMaskChars = append(bitsMaskChars, byte(cpuBitsMaskRune[i]))
	}
	affinityCPUMask = normalizeAffinityMask(string(bitsMaskChars))
	actualSMPAffinity = normalizeAffinityMask(actualSMPAffinity)
	Logf("affinityCPUMask is: -%s-, actualSMPAffinity is -%s-\n", affinityCPUMask, actualSMPAffinity)
	return affinityCPUMask == actualSMPAffinity, nil
}

// AssertDefaultIRQSMPAffinityAffectedBitMask checks whether the default IRQ
// SMP affinity on worker nodes matches the affinity mask derived from the
// isolated CPUs. Returns true if the values match, false otherwise.
func AssertDefaultIRQSMPAffinityAffectedBitMask(cpuBitsMask []byte, isolatedCPU []int, defaultIRQSMPAffinity string) (bool, error) {
	// Isolated CPU Range, 0,1,3-4,11-16,23-27
	//           27 26 25 24 ---------------------------------3 2 1 0
	//           27%4=3
	// [1111     1111         1111 1111 1111 1111 1111         1111] cpuBitMask
	// [0000     1111         1000 0001 1111 1000 0001         1011] isolatedCPU
	// --------------------------------------------------------------
	// [0000     1111         1000 0001 1111 1000 0001         1011] affinityCPUMask
	//  0         1            2    3   4     5   6             7    cpuBitMaskGroupsIndex
	//            6            5    4   3     2   1             0    isolatedCPUIndex
	//     maxValueOfIsolatedCPUIndex

	var affinityCPUMask string
	totalCPUBitMaskGroups := len(cpuBitsMask)
	totalIsolatedCPUNum := len(isolatedCPU)

	Logf("the total isolated CPUs is: %v\n", totalIsolatedCPUNum)
	if totalIsolatedCPUNum == 0 {
		return false, fmt.Errorf("no isolated CPUs provided, cannot compute affinity mask")
	}
	Logf("the max CPU that isolated is : %v\n", isolatedCPU[totalIsolatedCPUNum-1])
	// Initialize all bits to zero of isolatedCPUMask first.
	isolatedCPUMaskGroup := make([]byte, totalCPUBitMaskGroups)

	Logf("the initial isolatedCPUMask is %04b\n", isolatedCPUMaskGroup)

	maxValueOfCPUBitMaskGroupsIndex := totalCPUBitMaskGroups - 1
	for i := totalIsolatedCPUNum - 1; i >= 0; i-- {
		isolatedCPUIndex := isolatedCPU[i] / 4

		cpuBitsMaskIndex := maxValueOfCPUBitMaskGroupsIndex - isolatedCPUIndex

		// modIsolatedCPUby4 is 0-3; the corresponding bit mask is 1<<modIsolatedCPUby4
		// (0=>0001, 1=>0010, 2=>0100, 3=>1000)
		modIsolatedCPUby4 := isolatedCPU[i] % 4
		isolatedCPUMask := 1 << modIsolatedCPUby4

		Logf("%04b | %04b = %04b\n", isolatedCPUMaskGroup[cpuBitsMaskIndex], isolatedCPUMask, int(isolatedCPUMaskGroup[cpuBitsMaskIndex])|isolatedCPUMask)
		valueOfCPUBitsMaskOnIndex := int(isolatedCPUMaskGroup[cpuBitsMaskIndex]) | isolatedCPUMask
		isolatedCPUMaskGroup[cpuBitsMaskIndex] = byte(valueOfCPUBitsMaskOnIndex)
	}
	// Convert the byte slice to a hex string, then extract the second character
	// of each two-character hex pair. Each byte produces two hex chars (e.g., 0xAB -> "ab");
	// taking the odd-indexed characters (1, 3, 5, ...) yields the lower nibble of each byte,
	// which corresponds to the bits affected by isolated CPUs (positions 0,1,2,3 within each group of 4).
	Logf("cpuBitsMask is: %04b\n", isolatedCPUMaskGroup)
	cpuBitsMaskStr := fmt.Sprintf("%x", isolatedCPUMaskGroup)
	cpuBitsMaskRune := []rune(cpuBitsMaskStr)
	bitsMaskChars := make([]byte, 0, len(cpuBitsMaskRune)/2)

	for i := 1; i < len(cpuBitsMaskRune); i = i + 2 {
		bitsMaskChars = append(bitsMaskChars, byte(cpuBitsMaskRune[i]))
	}
	// Normalize leading zeros independently on each side so that different-length
	// hex representations of the same value (e.g. "0010" vs "00010") compare equal.
	defaultIRQSMPAffinity = normalizeAffinityMask(defaultIRQSMPAffinity)
	affinityCPUMask = normalizeAffinityMask(string(bitsMaskChars))

	Logf("affinityCPUMask is: -%s-, defaultIRQSMPAffinity is -%s-\n", affinityCPUMask, defaultIRQSMPAffinity)
	var isMatch bool
	if affinityCPUMask == defaultIRQSMPAffinity {
		isMatch = true
	}
	return isMatch, nil
}

// CreateIRQSMPAffinityProfile processes the NtoResource template and creates
// an IRQ SMP affinity Tuned resource in the cluster.
func (ntoRes *NtoResource) CreateIRQSMPAffinityProfile(oc *CLI) error {
	processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args("-n", ntoRes.Namespace, "--ignore-unknown-parameters=true", "-f", ntoRes.Template, "-p", "TUNED_NAME="+ntoRes.Name, "-p", "SYSCTLPARM="+ntoRes.SysctlParam, "-p", "SYSCTLVALUE="+ntoRes.SysctlValue, "-o", "yaml").Output()
	if err != nil {
		return fmt.Errorf("failed to process template for IRQ SMP affinity profile %s: %w", ntoRes.Name, err)
	}
	err = oc.AsAdmin().WithoutNamespace().Run("create").Args("-n", ntoRes.Namespace, "-f", "-").InputString(processedTemplate).Execute()
	if err != nil {
		return fmt.Errorf("failed to create IRQ SMP affinity profile %s: %w", ntoRes.Name, err)
	}
	return nil
}

// Delete removes the NtoResource Tuned object from the cluster.
func (ntoRes *NtoResource) Delete(oc *CLI) error {
	return oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoRes.Namespace, "tuned", ntoRes.Name, "--ignore-not-found").Execute()
}

// Helper functions for NTO extended tests (from nto_helpers.go)

// GetNTOPodName checks all pods in a given namespace and returns the first NTO pod name found
func GetNTOPodName(oc *CLI, namespace string) (string, error) {
	podName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "pods", "-lname=cluster-node-tuning-operator", "-ojsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get NTO operator pod: %w", err)
	}
	if podName == "" {
		return "", fmt.Errorf("NTO operator pod was not found in namespace %s", namespace)
	}
	return podName, nil
}

// GetTunedState returns a string representation of the spec.managementState of the specified tuned in a given namespace
func GetTunedState(oc *CLI, namespace string, tunedName string) (string, error) {
	return oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", tunedName, "-n", namespace, "-o=jsonpath={.spec.managementState}").Output()
}

// PatchTunedState will patch the state of the specified tuned to that specified if supported, will throw an error if patch fails or state unsupported
func PatchTunedState(oc *CLI, namespace string, tunedName string, state string) error {
	state = strings.ToLower(state)
	switch state {
	case "unmanaged":
		return oc.AsAdmin().WithoutNamespace().Run("patch").Args("tuned", tunedName, "-p", `{"spec":{"managementState":"Unmanaged"}}`, "--type", "merge", "-n", namespace).Execute()
	case "managed":
		return oc.AsAdmin().WithoutNamespace().Run("patch").Args("tuned", tunedName, "-p", `{"spec":{"managementState":"Managed"}}`, "--type", "merge", "-n", namespace).Execute()
	case "removed":
		return oc.AsAdmin().WithoutNamespace().Run("patch").Args("tuned", tunedName, "-p", `{"spec":{"managementState":"Removed"}}`, "--type", "merge", "-n", namespace).Execute()
	default:
		return fmt.Errorf("specified state %s is unsupported", state)
	}
}

// GetTunedPriority returns a string representation of the spec.recommend.priority of the specified tuned in a given namespace
func GetTunedPriority(oc *CLI, namespace string, tunedName string) (string, error) {
	return oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", tunedName, "-n", namespace, "-o=jsonpath={.spec.recommend[*].priority}").Output()
}

// PatchTunedProfile will patch the priority of the specified tuned to that specified in a given YAML or JSON file.
// We cannot directly patch the value since it is nested within a list, thus the need for a patch file for this function.
func PatchTunedProfile(oc *CLI, namespace string, tunedName string, patchFile string) error {
	return oc.AsAdmin().WithoutNamespace().Run("patch").Args("tuned", tunedName, "--patch-file="+patchFile, "--type", "merge", "-n", namespace).Execute()
}

// GetTunedProfile returns a string representation of the status.tunedProfile of the given node in the given namespace
func GetTunedProfile(oc *CLI, namespace string, tunedNodeName string) (string, error) {
	return oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", tunedNodeName, "-n", namespace, "-o=jsonpath={.status.tunedProfile}").Output()
}

// GetTunedPodNameByNodeName returns the tuned pod name for a given node
func GetTunedPodNameByNodeName(oc *CLI, tunedNodeName, namespace string) (string, error) {
	podName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("pods", "-n", namespace, "-l", "name=tuned", "--field-selector=spec.nodeName="+tunedNodeName, "-o=jsonpath={.items[0].metadata.name}").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get tuned pod for node %s: %w", tunedNodeName, err)
	}
	if podName == "" {
		return "", fmt.Errorf("no tuned pod found for node %s", tunedNodeName)
	}
	Logf("the Tuned Pod Name is: %v", podName)
	return podName, nil
}

// CreateCustomTunedProfile processes the NtoResource template and creates a
// custom Tuned resource in the cluster.
func (ntoRes *NtoResource) CreateCustomTunedProfile(oc *CLI) error {
	processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args("-n", ntoRes.Namespace, "--ignore-unknown-parameters=true", "-f", ntoRes.Template, "-p", "TUNED_NAME="+ntoRes.Name, "-p", "SYSCTLPARM="+ntoRes.SysctlParam, "-p", "SYSCTLVALUE="+ntoRes.SysctlValue, "-o", "yaml").Output()
	if err != nil {
		return fmt.Errorf("failed to process template for tuned profile %s: %w", ntoRes.Name, err)
	}
	err = oc.AsAdmin().WithoutNamespace().Run("create").Args("-n", ntoRes.Namespace, "-f", "-").InputString(processedTemplate).Execute()
	if err != nil {
		return fmt.Errorf("failed to create tuned profile %s: %w", ntoRes.Name, err)
	}
	return nil
}

// CreateDebugTunedProfile processes the NtoResource template and creates a
// Tuned resource in the cluster with debugging optionally turned on.
func (ntoRes *NtoResource) CreateDebugTunedProfile(oc *CLI, isDebug bool) error {
	processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args("-n", ntoRes.Namespace, "--ignore-unknown-parameters=true", "-f", ntoRes.Template, "-p", "TUNED_NAME="+ntoRes.Name, "-p", "SYSCTLPARM="+ntoRes.SysctlParam, "-p", "SYSCTLVALUE="+ntoRes.SysctlValue, "-p", "ISDEBUG="+strconv.FormatBool(isDebug), "-o", "yaml").Output()
	if err != nil {
		return fmt.Errorf("failed to process template for debug tuned profile %s: %w", ntoRes.Name, err)
	}
	err = oc.AsAdmin().WithoutNamespace().Run("create").Args("-n", ntoRes.Namespace, "-f", "-").InputString(processedTemplate).Execute()
	if err != nil {
		return fmt.Errorf("failed to create debug tuned profile %s: %w", ntoRes.Name, err)
	}
	return nil
}

// ApplyNTOTunedProfile processes the NtoResource template and applies a Tuned
// resource to the cluster.
func (ntoRes *NtoResource) ApplyNTOTunedProfile(oc *CLI) error {
	processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args("-n", ntoRes.Namespace, "--ignore-unknown-parameters=true", "-f", ntoRes.Template, "-p", "TUNED_PROFILE="+ntoRes.Name, "-p", "SYSCTL_NAME="+ntoRes.SysctlParam, "-p", "SYSCTL_VALUE="+ntoRes.SysctlValue, "-p", "LABEL_NAME="+ntoRes.Label, "-o", "yaml").Output()
	if err != nil {
		return fmt.Errorf("failed to process template for NTO tuned profile %s: %w", ntoRes.Name, err)
	}
	err = oc.AsAdmin().WithoutNamespace().Run("apply").Args("-n", ntoRes.Namespace, "-f", "-").InputString(processedTemplate).Execute()
	if err != nil {
		return fmt.Errorf("failed to apply NTO tuned profile %s: %w", ntoRes.Name, err)
	}
	return nil
}

// ApplyNTOTunedProfileWithDeferredAnnotation processes the NtoResource template
// and applies a Tuned resource with a deferred annotation to the cluster.
func (ntoRes *NtoResource) ApplyNTOTunedProfileWithDeferredAnnotation(oc *CLI) error {
	processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args("-n", ntoRes.Namespace, "--ignore-unknown-parameters=true", "-f", ntoRes.Template, "-p", "TUNED_PROFILE="+ntoRes.Name, "-p", "SYSCTL_NAME="+ntoRes.SysctlParam, "-p", "SYSCTL_VALUE="+ntoRes.SysctlValue, "-p", "LABEL_NAME="+ntoRes.Label, "-p", "DEFERRED_VALUE="+ntoRes.DeferredValue, "-o", "yaml").Output()
	if err != nil {
		return fmt.Errorf("failed to process template for NTO tuned profile with deferred annotation %s: %w", ntoRes.Name, err)
	}
	err = oc.AsAdmin().WithoutNamespace().Run("apply").Args("-n", ntoRes.Namespace, "-f", "-").InputString(processedTemplate).Execute()
	if err != nil {
		return fmt.Errorf("failed to apply NTO tuned profile with deferred annotation %s: %w", ntoRes.Name, err)
	}
	return nil
}

// Parsing functions

// sysctlNumericValue extracts the leading integer value of the sysctl key from
// sysctl-style output (e.g. "kernel.pid_max = 4194304").
func sysctlNumericValue(input, key string) (string, error) {
	for _, line := range strings.Split(input, "\n") {
		idx := strings.Index(line, key)
		if idx < 0 {
			continue
		}
		rest := strings.TrimLeft(line[idx+len(key):], " \t")
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		rest = strings.TrimLeft(rest[1:], " \t")
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		if end > 0 {
			return rest[:end], nil
		}
	}
	return "", fmt.Errorf("%s not found in input", key)
}

// GetMaxUserWatchesValue parses out the value determining max_user_watches in inotify.conf.
func GetMaxUserWatchesValue(inotify string) (string, error) {
	return sysctlNumericValue(inotify, "fs.inotify.max_user_watches")
}

// GetMaxUserInstancesValue parses out the value determining max_user_instances in inotify.conf.
func GetMaxUserInstancesValue(inotify string) (string, error) {
	return sysctlNumericValue(inotify, "fs.inotify.max_user_instances")
}

// GetKernelPidMaxValue parses out the value determining pid_max in the kernel.
func GetKernelPidMaxValue(kernel string) (string, error) {
	return sysctlNumericValue(kernel, "kernel.pid_max")
}

// GetValueOfSysctlByName parses out the sysctl value from the kernel on a given node.
func GetValueOfSysctlByName(oc *CLI, tunedNodeName, sysctlparm string) (string, error) {
	sysctlValue, _, err := debugNode(oc, tunedNodeName, []string{"--quiet=true"}, false, true, "sysctl", "-n", sysctlparm)
	if err != nil {
		return "", fmt.Errorf("failed to get sysctl %s on node %s: %w", sysctlparm, tunedNodeName, err)
	}
	if sysctlValue == "" {
		return "", fmt.Errorf("sysctl output is empty for %s on node %s", sysctlparm, tunedNodeName)
	}
	return strings.TrimSpace(sysctlValue), nil
}

// Sysctl comparison functions

// getSysctlValue reads the sysctl value from a node using oc debug.
func getSysctlValue(oc *CLI, nodeName, sysctlparm string, options ...string) (string, error) {
	stdOut, _, err := DebugNodeWithOptionsAndChrootWithStdErr(oc, nodeName, append([]string{"--quiet=true"}, options...), "sysctl", "-n", sysctlparm)
	if err != nil {
		return "", fmt.Errorf("failed to get sysctl %s on %s: %w", sysctlparm, nodeName, err)
	}
	return stdOut, nil
}

// sysctlSearch builds the "sysctlparm = value" string used for display/logging.
func sysctlSearch(sysctlparm, value string) string {
	return sysctlparm + " = " + value
}

// CompareSpecifiedValueByNameOnLabelNode checks if the sysctl parameter equals the specified value on a labeled node.
// It retries on transient failures (e.g., oc debug command failing during node reboots or on SNO)
// with 15-second intervals up to 180 seconds, consistent with CompareSpecifiedValueByNameOnLabelNodeWithRetry.
func CompareSpecifiedValueByNameOnLabelNode(ctx context.Context, oc *CLI, labelNodeName, sysctlparm, specifiedvalue string) error {
	err := wait.PollUntilContextTimeout(ctx, 15*time.Second, 180*time.Second, false, func(_ context.Context) (bool, error) {
		stdOut, getErr := getSysctlValue(oc, labelNodeName, sysctlparm)
		if getErr != nil {
			Logf("failed to get sysctl %s on node %s: %v, retrying", sysctlparm, labelNodeName, getErr)
			return false, nil
		}
		Logf("the value on %v is: %v", labelNodeName, strings.TrimSpace(stdOut))
		if strings.TrimSpace(stdOut) != specifiedvalue {
			Logf("sysctl %s on node %v does not equal expected value %q: got %q, retrying", sysctlparm, labelNodeName, specifiedvalue, strings.TrimSpace(stdOut))
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("sysctl %s on node %s did not converge to expected value %q within timeout: %w", sysctlparm, labelNodeName, specifiedvalue, err)
	}
	return nil
}

// CompareSysctlDifferentFromSpecifiedValueByNameWithRetry polls all worker nodes and asserts
// that the sysctl parameter does not equal the specified value. It retries every 15 seconds
// for up to 3 minutes to allow NTO time to reconcile after pod deletion or label removal.
func CompareSysctlDifferentFromSpecifiedValueByNameWithRetry(ctx context.Context, oc *CLI, sysctlparm, specifiedvalue string) error {
	err := wait.PollUntilContextTimeout(ctx, 15*time.Second, 180*time.Second, false, func(_ context.Context) (bool, error) {
		nodeList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=", "-o=jsonpath={.items[*].metadata.name}").Output()
		if err != nil {
			Logf("error listing worker nodes: %v, retrying", err)
			return false, nil
		}
		nodes := strings.Fields(nodeList)
		if len(nodes) == 0 {
			return false, fmt.Errorf("no worker nodes found")
		}

		for _, node := range nodes {
			stdOut, err := getSysctlValue(oc, node, sysctlparm)
			if err != nil {
				Logf("error checking sysctl on %v: %v", node, err)
				return false, nil
			}
			Logf("the value is [ %v ] on %v", strings.TrimSpace(stdOut), node)
			if strings.TrimSpace(stdOut) == specifiedvalue {
				Logf("sysctl %v still equals %v on %v, retrying", sysctlparm, specifiedvalue, node)
				return false, nil
			}
		}
		Logf("sysctl %v is different from %v on all worker nodes", sysctlparm, specifiedvalue)
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("sysctl value did not change within timeout: %w", err)
	}
	return nil
}

// CompareSysctlValueOnAllWorkerNodesWithRetry polls all worker nodes and validates
// sysctl values across the cluster:
// - Checks that the tuned node has the expected sysctl value
// - Verifies that other worker nodes do NOT have the specified value
// - If defaultvalue is provided, ensures non-tuned nodes have the defaultvalue
// It retries every 15 seconds for up to 3 minutes to allow NTO time to reconcile.
func CompareSysctlValueOnAllWorkerNodesWithRetry(ctx context.Context, oc *CLI, tunedNodeName, sysctlparm, defaultvalue, specifiedvalue string) error {
	err := wait.PollUntilContextTimeout(ctx, 15*time.Second, 180*time.Second, false, func(_ context.Context) (bool, error) {
		nodeList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=", "-o=jsonpath={.items[*].metadata.name}").Output()
		if err != nil {
			Logf("error listing worker nodes: %v, retrying", err)
			return false, nil
		}
		nodes := strings.Fields(nodeList)
		if len(nodes) == 0 {
			return false, fmt.Errorf("no worker nodes found")
		}

		// Ensure the tuned node is in the worker node list; if not, it cannot be validated.
		tunedNodeFound := false
		for _, node := range nodes {
			if node == tunedNodeName {
				tunedNodeFound = true
				break
			}
		}
		if !tunedNodeFound {
			Logf("tuned node %v not found in worker node list %v, retrying", tunedNodeName, nodes)
			return false, nil
		}

		for _, node := range nodes {
			stdOut, err := getSysctlValue(oc, node, sysctlparm)
			if err != nil {
				Logf("error checking sysctl on %v: %v", node, err)
				return false, nil
			}
			Logf("the actual value is %v on %v", strings.TrimSpace(stdOut), node)
			if node == tunedNodeName {
				Logf("the expected value of %v should be %v on %v", sysctlparm, specifiedvalue, node)
				if strings.TrimSpace(stdOut) != specifiedvalue {
					Logf("sysctl %v not yet %v on %v, retrying", sysctlparm, specifiedvalue, node)
					return false, nil
				}
			} else {
				Logf("the expected value of %v shouldn't be %v on %v", sysctlparm, specifiedvalue, node)
				if strings.TrimSpace(stdOut) == specifiedvalue {
					Logf("sysctl %v still equals %v on %v, retrying", sysctlparm, specifiedvalue, node)
					return false, nil
				}
				if len(defaultvalue) > 0 {
					Logf("the expected default value of %v should be %v on %v", sysctlparm, defaultvalue, node)
					if strings.TrimSpace(stdOut) != defaultvalue {
						Logf("sysctl %v not yet default %v on %v, retrying", sysctlparm, defaultvalue, node)
						return false, nil
					}
				}
			}
		}
		Logf("sysctl %v is %v on %v and not on other worker nodes", sysctlparm, specifiedvalue, tunedNodeName)
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("sysctl value did not converge within timeout: %w", err)
	}
	return nil
}

// CompareSpecifiedValueByNameOnLabelNodeWithRetry polls the given node's sysctl parameter
// and asserts it matches the expected value.  It retries every 15 seconds for up to 3 minutes.
func CompareSpecifiedValueByNameOnLabelNodeWithRetry(ctx context.Context, oc *CLI, ntoNamespace, nodeName, sysctlparm, specifiedvalue string) error {
	search := sysctlSearch(sysctlparm, specifiedvalue)

	err := wait.PollUntilContextTimeout(ctx, 15*time.Second, 180*time.Second, false, func(_ context.Context) (bool, error) {
		sysctlOutput, _, err := DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, nodeName, []string{"--quiet=true", "--to-namespace=" + ntoNamespace}, "sysctl", "-n", sysctlparm)
		Logf("the actual value is [ %v ] on %v", sysctlOutput, nodeName)
		if err != nil {
			Logf("failed to get sysctl value on %v: %v, retrying", nodeName, err)
			return false, nil
		}

		if strings.TrimSpace(sysctlOutput) == specifiedvalue {
			Logf("matched '%s' on %s", search, nodeName)
			return true, nil
		}
		Logf("no match for '%s' on %s", search, nodeName)
		return false, nil
	})
	return err
}

// Assertion functions

// WaitForSchedulingDisabledNode waits until a node with 'SchedulingDisabled' or 'NotReady' status appears.
func WaitForSchedulingDisabledNode(ctx context.Context, oc *CLI) (string, error) {
	var nodeNames []string
	err := wait.PollUntilContextTimeout(ctx, 30*time.Second, 3*time.Minute, false, func(_ context.Context) (bool, error) {
		// Get nodes with SchedulingDisabled (unschedulable) via jsonpath
		unschedulableNodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-o=jsonpath={.items[?(@.spec.unschedulable==true)].metadata.name}").Output()
		if err != nil {
			Logf("failed to get unschedulable nodes: %v, retrying", err)
			return false, nil
		}
		unschedulableList := strings.Fields(strings.TrimSpace(unschedulableNodes))
		if len(unschedulableList) > 0 {
			Logf("'SchedulingDisabled' status found on nodes: %v", unschedulableList)
			nodeNames = unschedulableList
			return true, nil
		}

		nodesText, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes").Output()
		if err != nil {
			Logf("failed to get nodes: %v, retrying", err)
			return false, nil
		}
		for _, line := range strings.Split(nodesText, "\n") {
			if strings.Contains(line, "NotReady") {
				fields := strings.Fields(line)
				if len(fields) > 0 {
					Logf("'NotReady' status found on node: %v", fields[0])
					nodeNames = []string{fields[0]}
					return true, nil
				}
			}
		}

		Logf("no node with 'SchedulingDisabled' or 'NotReady' status found - retrying")
		return false, nil
	})
	if err != nil {
		return "", fmt.Errorf("no node was found with 'SchedulingDisabled' status within timeout limit (3 minutes): %w", err)
	}
	Logf("node Name is %v", nodeNames[0])
	return nodeNames[0], nil
}

// WaitForMasterNodeChanges waits until 'default_hugepagesz=2M' is present in /proc/cmdline on the master node.
func WaitForMasterNodeChanges(ctx context.Context, oc *CLI, masterNodeName string) error {
	err := wait.PollUntilContextTimeout(ctx, 1*time.Minute, 5*time.Minute, false, func(_ context.Context) (bool, error) {
		output, _, err := debugNode(oc, masterNodeName, []string{"--quiet=true"}, true, true, "cat", "/proc/cmdline")
		if err != nil {
			Logf("failed to get /proc/cmdline on %v: %v, retrying", masterNodeName, err)
			return false, nil
		}

		isMasterNodeChanged := strings.Contains(output, "default_hugepagesz=2M")
		if isMasterNodeChanged {
			Logf("node %v has expected changes:\n%v", masterNodeName, output)
			return true, nil
		}
		Logf("node %v does not have expected changes - retrying", masterNodeName)
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("node %s did not have expected changes within timeout limit: %w", masterNodeName, err)
	}
	return nil
}

// AssertDebugSettings checks whether the debug setting on a tuned profile
// matches the expected value "true" or "false".
func AssertDebugSettings(oc *CLI, tunedNodeName string, ntoNamespace string, isDebug string) (bool, error) {
	nodeProfile, err := oc.AsAdmin().WithoutNamespace().Run("describe").Args("profiles.tuned.openshift.io", tunedNodeName, "-n", ntoNamespace).Output()
	if err != nil {
		return false, fmt.Errorf("failed to describe profile: %w", err)
	}

	isMatch := false
	debugPrefix := "Debug:"
	for _, line := range strings.Split(nodeProfile, "\n") {
		if idx := strings.Index(line, debugPrefix); idx >= 0 && strings.TrimSpace(line[idx+len(debugPrefix):]) == isDebug {
			isMatch = true
			Logf("the result is: %v", line)
			break
		}
	}
	return isMatch, nil
}

// AssertNTOCustomProfileStatus returns whether the node profile matches the expected profile and condition statuses.
func AssertNTOCustomProfileStatus(oc *CLI, ntoNamespace string, tunedNodeName string, expectedProfile string, expectedAppliedStatus string, expectedDegradedStatus string) (bool, error) {
	currentProfile, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io", tunedNodeName, `-ojsonpath={.status.tunedProfile}`).Output()
	if err != nil {
		return false, fmt.Errorf("failed to get current profile: %w", err)
	}
	if currentProfile == "" {
		return false, fmt.Errorf("current profile is empty")
	}
	Logf("currentProfile is %v", currentProfile)

	appliedStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io", tunedNodeName, `-ojsonpath={.status.conditions[?(@.type=="Applied")].status}`).Output()
	if err != nil {
		return false, fmt.Errorf("failed to get applied status: %w", err)
	}
	if appliedStatus == "" {
		return false, fmt.Errorf("applied status is empty")
	}
	Logf("appliedStatus is %v", appliedStatus)

	degradedStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io", tunedNodeName, `-ojsonpath={.status.conditions[?(@.type=="Degraded")].status}`).Output()
	if err != nil {
		return false, fmt.Errorf("failed to get degraded status: %w", err)
	}
	if degradedStatus == "" {
		return false, fmt.Errorf("degraded status is empty")
	}
	Logf("degradedStatus is %v", degradedStatus)

	return appliedStatus == expectedAppliedStatus && degradedStatus == expectedDegradedStatus && currentProfile == expectedProfile, nil
}

// AssertNTOPodLogsLastLines polls the NTO pod logs until they contain the
// given filter keyword, or times out after timeDurationSec seconds.
func AssertNTOPodLogsLastLines(ctx context.Context, oc *CLI, namespace string, ntoPod string, lineN string, timeDurationSec int, filter string) error {
	regNTOPodLogs, err := regexp.Compile(".*" + filter + ".*")
	if err != nil {
		return fmt.Errorf("failed to compile regex for filter %q: %w", filter, err)
	}

	timeout := time.Duration(timeDurationSec) * time.Second
	if timeout < 15*time.Second {
		timeout = 15 * time.Second
	}

	err = wait.PollUntilContextTimeout(ctx, 15*time.Second, timeout, false, func(_ context.Context) (bool, error) {
		// Do not assert on log fetch errors: on SNO the API server may be temporarily unreachable during master node restart or certificate rotation.
		ntoPodLogs, logErr := oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", namespace, ntoPod, "--tail="+lineN).Output()
		if logErr != nil {
			Logf("error fetching logs for pod %s/%s: %v, retrying", namespace, ntoPod, logErr)
			return false, nil
		}

		isMatch := regNTOPodLogs.MatchString(ntoPodLogs)
		if isMatch {
			loglines := regNTOPodLogs.FindAllString(ntoPodLogs, -1)
			Logf("the logs of nto pod %v is: \n%v", ntoPod, loglines[0])
			return true, nil
		}
		Logf("the keywords of nto pod isn't found, try next")
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("the tuned pod's log doesn't contain the keywords, please check: %w", err)
	}
	return nil
}

// WaitForCOStatusWithKeywords polls the clusteroperator status until it contains the given keywords.
func WaitForCOStatusWithKeywords(ctx context.Context, oc *CLI, timeout time.Duration, keywords string) error {
	err := wait.PollUntilContextTimeout(ctx, time.Second, timeout, false, func(_ context.Context) (bool, error) {
		coStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("co").Output()
		if err != nil {
			Logf("error getting clusteroperator status: %v, retrying", err)
			return false, nil
		}
		if !strings.Contains(coStatus, keywords) {
			Logf("keywords %q not found in co status, retrying", keywords)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("keywords %q not found in clusteroperator status within timeout: %w", keywords, err)
	}
	return nil
}

// WaitForCONodeTuningStatusClear waits until warning messages disappear from co/node-tuning; they can clear with delay.
func WaitForCONodeTuningStatusClear(ctx context.Context, oc *CLI, timeDurationSec int, filter string) error {
	pollInterval := time.Duration(timeDurationSec/10) * time.Second
	if pollInterval < 5*time.Second {
		pollInterval = 5 * time.Second
	}

	err := wait.PollUntilContextTimeout(ctx, pollInterval, time.Duration(timeDurationSec)*time.Second, false, func(_ context.Context) (bool, error) {
		coNodeTuningStdOut, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("co/node-tuning").Output()
		if err != nil {
			Logf("error getting co/node-tuning: %v, retrying", err)
			return false, nil
		}

		if !strings.Contains(coNodeTuningStdOut, filter) {
			return true, nil
		}
		for _, line := range strings.Split(coNodeTuningStdOut, "\n") {
			if strings.Contains(line, filter) {
				Logf("the status of co/node-tuning is:%v \n%v\n", coNodeTuningStdOut, line)
				break
			}
		}
		Logf("the keywords of co/node-tuning still found, try next")
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("the checking of co/node-tuning met with unexpected error, please check: %w", err)
	}
	return nil
}

// MachineConfig and node validation functions

// GetPoolUpdatedMachineCount returns the UpdatedMachineCount for MCP 'pool'.
func GetPoolUpdatedMachineCount(ctx context.Context, oc *CLI, pool string) (int32, error) {
	var (
		explain error
		count   int32
	)

	startTime := time.Now()
	if err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		updatedMachineCount, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", pool, "-o=jsonpath={.status.updatedMachineCount}").Output()
		if err != nil {
			explain = err
			return false, nil
		}
		updated, err := strconv.ParseInt(strings.TrimSpace(updatedMachineCount), 10, 32)
		if err != nil {
			explain = err
			return false, nil
		}
		count = int32(updated)
		return true, nil
	}); err != nil {
		if explain != nil {
			return 0, fmt.Errorf("failed to get pool %s UpdatedMachineCount (waited %s): last error: %v: %w", pool, time.Since(startTime), explain, err)
		}
		return 0, fmt.Errorf("failed to get pool %s UpdatedMachineCount (waited %s): %w", pool, time.Since(startTime), err)
	}
	return count, nil
}

// WaitForPoolUpdatedMachineCount polls a pool until its UpdatedMachineCount equals to 'count'.
func WaitForPoolUpdatedMachineCount(ctx context.Context, oc *CLI, pool string, count int32) error {
	var explain error

	startTime := time.Now()
	if err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 20*time.Minute, true, func(ctx context.Context) (bool, error) {
		updatedMachineCount, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", pool, "-o=jsonpath={.status.updatedMachineCount}").Output()
		if err != nil {
			// This is not fatal.  On SNO, API server will be unavailable during reboots.
			explain = err
			return false, nil
		}
		updated, err := strconv.ParseInt(strings.TrimSpace(updatedMachineCount), 10, 32)
		if err != nil {
			explain = err
			return false, nil
		}
		if int32(updated) == count {
			return true, nil
		}
		return false, nil
	}); err != nil {
		if explain != nil {
			return fmt.Errorf("pool %s UpdatedMachineCount != %d (waited %s): last error: %v: %w", pool, count, time.Since(startTime), explain, err)
		}
		return fmt.Errorf("pool %s UpdatedMachineCount != %d (waited %s): %w", pool, count, time.Since(startTime), err)
	}
	return nil
}

// AssertTunedAppliedMC checks if a custom tuned profile was applied via MachineConfigPool.
func AssertTunedAppliedMC(oc *CLI, mcNameSubstring string, filter string) error {
	mcNameList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("mc", "--no-headers", "-oname").Output()
	if err != nil {
		return fmt.Errorf("failed to list MachineConfigs: %w", err)
	}
	Logf("the name of mcName is: %v", mcNameList)

	// Find MachineConfig names that contain the substring mcNameSubstring.
	var mcName []string
	for _, line := range strings.Split(mcNameList, "\n") {
		if strings.Contains(line, mcNameSubstring) {
			mcName = append(mcName, line)
		}
	}
	Logf("the expected names of mcName is: %v", mcName)
	if len(mcName) == 0 {
		return fmt.Errorf("no MachineConfig found matching substring %q", mcNameSubstring)
	}
	if len(mcName) > 1 {
		return fmt.Errorf("expected exactly one MachineConfig matching %q, found %d: %v", mcNameSubstring, len(mcName), mcName)
	}

	mcOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(mcName[0], "-oyaml").Output()
	if err != nil {
		return fmt.Errorf("failed to get MachineConfig %s: %w", mcName[0], err)
	}
	if !strings.Contains(mcOutput, filter) {
		return fmt.Errorf("MachineConfig %s does not contain expected filter %q", mcName[0], filter)
	}

	// Print machineconfig content by filter
	for _, line := range strings.Split(mcOutput, "\n") {
		if strings.Contains(line, filter) {
			Logf("the result is: %v", line)
			break
		}
	}
	return nil
}

// AssertTunedAppliedToNode checks if a custom tuned profile was applied to a given node.
func AssertTunedAppliedToNode(oc *CLI, tunedNodeName string, filter string) (bool, error) {
	cmdLineOutput, _, err := DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, tunedNodeName, []string{"--quiet=true"}, "cat", "/proc/cmdline")
	if err != nil {
		return false, fmt.Errorf("failed to read /proc/cmdline on node %s: %w", tunedNodeName, err)
	}
	if strings.Contains(cmdLineOutput, filter) {
		// /proc/cmdline is a single line; print it for diagnostics. This replaces
		// the former regexp.MustCompile(".*"+...QuoteMeta(filter)...+".*").FindAllString.
		Logf("the result is: %v", cmdLineOutput)
		return true, nil
	}
	Logf("the result mismatched the filter: %v", filter)
	return false, nil
}

// AssertIOTimeoutAndMaxRetries verifies that the NVMe io_timeout parameter
// is set to 4294967295 on all worker nodes that have the NVMe module loaded.
func AssertIOTimeoutAndMaxRetries(ctx context.Context, oc *CLI) error {
	nodeList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return fmt.Errorf("failed to list worker nodes: %w", err)
	}
	nodes := strings.Fields(nodeList)
	if len(nodes) == 0 {
		return fmt.Errorf("no worker nodes found")
	}

	err = wait.PollUntilContextTimeout(ctx, 15*time.Second, 3*time.Minute, false, func(_ context.Context) (bool, error) {
		checked := false
		for _, node := range nodes {
			checkOutput, checkStderr, checkErr := debugNode(oc, node, []string{"--quiet=true"}, true, true, "ls", "/sys/module/nvme_core/parameters/io_timeout")
			if checkErr != nil {
				if strings.Contains(checkStderr, "cannot access") || strings.Contains(checkStderr, "No such file") {
					Logf("node %s does not have NVMe module loaded, skipping", node)
					continue
				}
				Logf("failed to check NVMe module on node %s: %v, retrying", node, checkErr)
				return false, nil
			}
			if checkOutput == "" {
				Logf("node %s does not have NVMe module loaded, skipping", node)
				continue
			}
			checked = true
			timeoutOutput, _, err := debugNode(oc, node, []string{"--quiet=true"}, true, true, "cat", "/sys/module/nvme_core/parameters/io_timeout")
			if err != nil {
				Logf("failed to read io_timeout on node %s: %v, retrying", node, err)
				return false, nil
			}
			Logf("the value of io_timeout is : %v on node %v", timeoutOutput, node)
			if !strings.Contains(timeoutOutput, "4294967295") {
				Logf("io_timeout on node %s does not contain expected value 4294967295: got %q, retrying", node, timeoutOutput)
				return false, nil
			}
		}
		if !checked {
			return false, fmt.Errorf("no node exposed nvme_core io_timeout parameter; nothing was verified")
		}
		return true, nil
	})
	return err
}

// Certificate and service functions
var endpointRE = regexp.MustCompile(`^((\d{1,3}\.){3}\d{1,3}|[0-9a-fA-F:]+):\d+$`)

// GetServiceEndpoint returns the IP:port endpoint of the node-tuning-operator
// service in the given namespace.
func GetServiceEndpoint(oc *CLI, namespace string) (string, error) {
	endpointAddresses, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "endpoints/node-tuning-operator", "-ojsonpath={.subsets[*].addresses[*].ip}").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get endpoint addresses: %w", err)
	}
	if endpointAddresses == "" {
		return "", fmt.Errorf("endpoint addresses are empty")
	}

	endpointPorts, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "endpoints/node-tuning-operator", "-ojsonpath={.subsets[*].ports[*].port}").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get endpoint ports: %w", err)
	}
	if endpointPorts == "" {
		return "", fmt.Errorf("endpoint ports are empty")
	}

	addresses := strings.Split(strings.TrimSpace(endpointAddresses), " ")
	ports := strings.Split(strings.TrimSpace(endpointPorts), " ")
	if len(addresses) == 0 || len(ports) == 0 {
		return "", fmt.Errorf("no endpoint addresses or ports found")
	}

	endpoint := addresses[0] + ":" + ports[0]
	if !endpointRE.MatchString(endpoint) {
		return "", fmt.Errorf("endpoint %q does not match expected IP:port format", endpoint)
	}
	return endpoint, nil
}

// certsEqual compares two PEM-encoded certificates by decoding their first PEM
// block and comparing the DER bytes. This checks content equality only and does
// not validate certificate properties such as expiration dates or chain of trust.
func certsEqual(pemCert1, pemCert2 string) (bool, error) {
	block1, _ := pem.Decode([]byte(pemCert1))
	if block1 == nil {
		return false, fmt.Errorf("failed to parse PEM certificate from first input")
	}
	block2, _ := pem.Decode([]byte(pemCert2))
	if block2 == nil {
		return false, fmt.Errorf("failed to parse PEM certificate from second input")
	}
	return bytes.Equal(block1.Bytes, block2.Bytes), nil
}

// fetchOpenSSLCertificateFromNode connects to metricEndpoint via openssl
// s_client from tunedNodeName (chrooted debug pod) and pipes the output
// through pipeCmd (e.g. "openssl x509" to normalize, or a sed extraction of
// the raw PEM block). Both certificate-rotation polls below share this single
// command builder so a pod-spawn/exec failure is reported identically and
// distinguishably from a genuine certificate mismatch.
func fetchOpenSSLCertificateFromNode(oc *CLI, tunedNodeName, metricEndpoint string, recoverNsLabels bool, pipeCmd string) (string, error) {
	cmd := "/usr/bin/openssl s_client -connect " + metricEndpoint + " 2>/dev/null </dev/null | " + pipeCmd
	stdout, _, err := debugNode(oc, tunedNodeName, []string{"--quiet=true"}, true, recoverNsLabels, "/bin/bash", "-c", cmd)
	return stdout, err
}

// CompareCertificateBetweenOpenSSLandTLSSecret compares the certificate
// obtained via OpenSSL from the NTO service endpoint with the tls.crt stored
// in the node-tuning-operator-tls secret. Returns an error if they differ.
func CompareCertificateBetweenOpenSSLandTLSSecret(ctx context.Context, oc *CLI, ntoNamespace string, tunedNodeName string) error {
	metricEndpoint, err := GetServiceEndpoint(oc, ntoNamespace)
	if err != nil {
		return fmt.Errorf("failed to get service endpoint: %w", err)
	}
	var lastReason string
	err = wait.PollUntilContextTimeout(ctx, 15*time.Second, 180*time.Second, false, func(_ context.Context) (bool, error) {
		// Extract certificate from openssl that nto operator service endpoint
		openSSLOutputAfter, err := fetchOpenSSLCertificateFromNode(oc, tunedNodeName, metricEndpoint, true, `sed -ne '/-BEGIN CERTIFICATE-/,/-END CERTIFICATE-/p'`)
		if err != nil {
			lastReason = fmt.Sprintf("failed to get openssl certificate from node %s (endpoint unreachable or debug pod failed): %v", tunedNodeName, err)
			Logf("%s, retrying", lastReason)
			return false, nil
		}

		// Extract tls.crt from secret node-tuning-operator-tls
		encodeBase64tlsCertOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "secret", "node-tuning-operator-tls", `-ojsonpath={.data.tls\.crt}`).Output()
		if err != nil {
			lastReason = fmt.Sprintf("failed to get tls.crt from secret: %v", err)
			Logf("%s, retrying", lastReason)
			return false, nil
		}
		tlsCertOutput, err := BASE64DecodeStr(encodeBase64tlsCertOutput)
		if err != nil {
			lastReason = fmt.Sprintf("failed to decode tls.crt from secret: %v", err)
			Logf("%s, retrying", lastReason)
			return false, nil
		}

		isSame, err := certsEqual(openSSLOutputAfter, tlsCertOutput)
		if err != nil {
			lastReason = fmt.Sprintf("failed to compare certificates: %v", err)
			Logf("%s, retrying", lastReason)
			return false, nil
		}
		if isSame {
			Logf("the certificate is the same")
			return true, nil
		}
		lastReason = "the certificate served by the endpoint still differs from the tls.crt secret"
		Logf("%s, try next round", lastReason)
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("certificate served by node %s never matched tls.crt secret (last reason: %s): %w", tunedNodeName, lastReason, err)
	}
	return nil
}

// WaitForNTOCertificateRotation waits until the NTO metrics server certificate has been rotated.
func WaitForNTOCertificateRotation(ctx context.Context, oc *CLI, ntoNamespace string, tunedNodeName string, encodeBase64CertificateBefore string) error {
	metricEndpoint, err := GetServiceEndpoint(oc, ntoNamespace)
	if err != nil {
		return fmt.Errorf("failed to get service endpoint: %w", err)
	}
	var lastReason string
	err = wait.PollUntilContextTimeout(ctx, 15*time.Second, 300*time.Second, false, func(_ context.Context) (bool, error) {
		certificateAfter, err := fetchOpenSSLCertificateFromNode(oc, tunedNodeName, metricEndpoint, false, "/usr/bin/openssl x509")
		if err != nil {
			lastReason = fmt.Sprintf("failed to get certificate from node %s (endpoint unreachable or debug pod failed): %v", tunedNodeName, err)
			Logf("%s, retrying", lastReason)
			return false, nil
		}

		encodeBase64CertificateAfter := StringToBASE64(certificateAfter)

		if encodeBase64CertificateBefore != encodeBase64CertificateAfter {
			Logf("the certificate has been updated")
			return true, nil
		}
		lastReason = "the certificate served by the endpoint still matches the pre-rotation certificate"
		Logf("%s, try next round", lastReason)
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("NTO certificate on node %s was not rotated within timeout (last reason: %s): %w", tunedNodeName, lastReason, err)
	}
	return nil
}

// AssertNetworkChannelQueuesStatus checks if network channel queues are configured correctly.
func AssertNetworkChannelQueuesStatus(oc *CLI, namespace string, tunedNodeName string) (bool, error) {
	var isMatch bool
	findStr := `find /sys/class/net -type l -not -lname '*virtual*' -a -not -name 'enP*' -printf '%f\n'`
	ifNameList, _, err := DebugNodeWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"--quiet=true", "--to-namespace=" + namespace}, "bash", "-c", findStr)
	if err != nil {
		return false, fmt.Errorf("failed to list physical network interfaces on node %s: %w", tunedNodeName, err)
	}
	Logf("physical network list is: %v", ifNameList)
	if ifNameList == "" {
		return false, fmt.Errorf("no physical network interfaces found on node %s", tunedNodeName)
	}

	// Remove double quotes
	ifNameStr := strings.ReplaceAll(ifNameList, "\"", "")
	if ifNameStr == "" {
		return false, fmt.Errorf("physical network interface list is empty on node %s", tunedNodeName)
	}
	// Check all physical nic
	ifNames := strings.Split(ifNameStr, "\n")
	Logf("ifNames is: %v", ifNames)
	if len(ifNames) == 0 {
		return false, fmt.Errorf("no physical network interfaces parsed on node %s", tunedNodeName)
	}

	for _, ifName := range ifNames {
		if len(ifName) > 0 {
			ethToolsOutput, _, err := DebugNodeWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"--quiet=true", "--to-namespace=" + namespace}, "ethtool", "-l", ifName)
			if err != nil {
				return false, fmt.Errorf("failed to run ethtool on %s for interface %s: %w", tunedNodeName, ifName, err)
			}
			if ethToolsOutput == "" {
				return false, fmt.Errorf("ethtool output is empty for interface %s on node %s", ifName, tunedNodeName)
			}
			Logf("ethtool -l %v:, \n%v", ifName, ethToolsOutput)

			// Check whether any "Combined:" line carries a "1" value.
			for _, line := range strings.Split(ethToolsOutput, "\n") {
				if idx := strings.Index(line, "Combined:"); idx >= 0 {
					value := strings.TrimSpace(line[idx+len("Combined:"):])
					if value == "1" {
						isMatch = true
						break
					}
				}
			}
			if isMatch {
				break
			}
		}
	}
	return isMatch, nil
}

// validateProcessFilter validates that the processFilter only contains safe characters
// to prevent shell command injection. Only alphanumeric characters, hyphens,
// underscores, and dots are allowed.
func validateProcessFilter(processFilter string) error {
	matched, err := regexp.MatchString(`^[a-zA-Z0-9_.-]+$`, processFilter)
	if err != nil {
		return fmt.Errorf("failed to validate process filter: %w", err)
	}
	if !matched {
		return fmt.Errorf("invalid process filter: %q contains disallowed characters", processFilter)
	}
	return nil
}

// cpusAllowedListValue extracts the value following "Cpus_allowed_list:" from the
// output of `grep ^Cpus_allowed_list /proc/<pid>/status`. It returns the trimmed
// value, or "" if the field is absent.
func cpusAllowedListValue(status string) string {
	for _, line := range strings.Split(status, "\n") {
		if idx := strings.Index(line, "Cpus_allowed_list:"); idx >= 0 {
			return strings.TrimSpace(line[idx+len("Cpus_allowed_list:"):])
		}
	}
	return ""
}

// AssertProcessExcludedFromCgroupScheduler checks that a process is excluded from the cgroup scheduler.
func AssertProcessExcludedFromCgroupScheduler(oc *CLI, tunedNodeName string, namespace string, processFilter string, nodeCPUCores int) (bool, error) {
	if err := validateProcessFilter(processFilter); err != nil {
		return false, err
	}
	if nodeCPUCores < 2 {
		return false, fmt.Errorf("node %s has insufficient CPU cores (%d) for cgroup scheduler exclusion check; need at least 2", tunedNodeName, nodeCPUCores)
	}

	pIDCpusAllowedList, err := DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"-n", namespace, "--quiet=true", "--to-namespace=" + namespace}, "/bin/bash", "-c", "grep ^Cpus_allowed_list /proc/$(pgrep "+processFilter+" | tail -1)/status")
	if err != nil {
		return false, fmt.Errorf("failed to get Cpus_allowed_list for process %s on node %s: %w", processFilter, tunedNodeName, err)
	}
	Logf("actually Process's Cpus_allowed_list in /proc/$PID/status on worker nodes is: \n%v", pIDCpusAllowedList)

	// CPU = 2:
	//   In cgroup blacklist: 0-1 (N is CPU cores - 1)
	//   Not in cgroup blacklist: 0
	// CPU > 2:
	//   In cgroup blacklist: 0-N (N is CPU cores - 1)
	//   Not in cgroup blacklist: 0,2-N (N is CPU cores - 1)
	var expected string
	switch nodeCPUCores {
	case 2:
		expected = "0"
	case 3:
		// Kernel collapses single-element ranges: "2-2" → "2"
		expected = "0,2"
	default:
		expected = "0,2-" + strconv.Itoa(nodeCPUCores-1)
	}

	Logf("expected Process's Cpus_allowed_list in /proc/$PID/status on worker nodes is: \n%v", "Cpus_allowed_list:	"+expected)

	// Compare the entire value following "Cpus_allowed_list:" so a blacklist range such
	// as "0-10" (which ends in "0") is not mistakenly reported as the excluded value "0".
	isMatch := cpusAllowedListValue(pIDCpusAllowedList) == expected

	Logf("match cgroup Cpus_allowed_list for process %v is: %v", processFilter, isMatch)
	return isMatch, nil
}

// AssertProcessInCgroupSchedulerBlacklist checks that a process is present
// in the cgroup scheduler blacklist (Cpus_allowed_list = "0-N" where N is
// nodeCPUCores - 1), meaning it is excluded from all CPUs.
func AssertProcessInCgroupSchedulerBlacklist(oc *CLI, tunedNodeName string, namespace string, processFilter string, nodeCPUCores int) (bool, error) {
	if err := validateProcessFilter(processFilter); err != nil {
		return false, err
	}
	if nodeCPUCores < 2 {
		return false, fmt.Errorf("node %s has insufficient CPU cores (%d) for cgroup scheduler blacklist check; need at least 2", tunedNodeName, nodeCPUCores)
	}

	pIDCpusAllowedList, err := DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"-n", namespace, "--quiet=true", "--to-namespace=" + namespace}, "/bin/bash", "-c", "grep ^Cpus_allowed_list /proc/$(pgrep "+processFilter+" | tail -1)/status")
	if err != nil {
		return false, fmt.Errorf("failed to get Cpus_allowed_list for process %s on node %s: %w", processFilter, tunedNodeName, err)
	}
	Logf("actually Process's Cpus_allowed_list in /proc/$PID/status on worker nodes is: \n%v", pIDCpusAllowedList)

	// CPU = 2:
	//   In cgroup blacklist: 0-1
	//   Not in cgroup blacklist: 0
	// CPU > 2:
	//   In cgroup blacklist: 0-N (N is CPU cores - 1)
	//   Not in cgroup blacklist: 0,2-N (N is CPU cores - 1)

	expectedBlacklist := "0-" + strconv.Itoa(nodeCPUCores-1)
	Logf("expected Process's Cpus_allowed_list in /proc/$PID/status on worker nodes is: \n%v", "Cpus_allowed_list:	"+expectedBlacklist)
	// Compare the entire value following "Cpus_allowed_list:" (see comment in the
	// sibling function for why an exact value comparison is required).
	isMatch := cpusAllowedListValue(pIDCpusAllowedList) == expectedBlacklist

	Logf("match cgroup Cpus_allowed_list for process %v is: %v", processFilter, isMatch)
	return isMatch, nil
}

// ConfirmedTunedReady polls until the tuned resource with the given name
// appears in the namespace, indicating the tuned daemon has applied it.
func ConfirmedTunedReady(ctx context.Context, oc *CLI, ntoNamespace string, tunedName string, timeDurationSec int) error {
	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, time.Duration(timeDurationSec)*time.Second, false, func(_ context.Context) (bool, error) {
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "-n", ntoNamespace, "-o=jsonpath={.items[*].metadata.name}").Output()
		if err != nil {
			Logf("failed to get tuned status in %v: %v, retrying", ntoNamespace, err)
			return false, nil
		}

		for _, name := range strings.Fields(tunedNames) {
			if name == tunedName {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("tuned %s is not ready: %w", tunedName, err)
	}
	return nil
}

// SwitchThrottlectlOnOff runs throttlectl with the given state ("on" or "off")
// on the tuned node and verifies the change by polling /proc/sys/kernel/sched_rt_runtime_us.
// "off" disables RT throttling and sets sched_rt_runtime_us to -1 (unlimited),
// while "on" re-enables it with a finite runtime value.
func SwitchThrottlectlOnOff(ctx context.Context, oc *CLI, ntoNamespace, tunedNodeName string, throttlectlState string, timeDurationSec int) error {
	switch throttlectlState {
	case "on", "off":
	default:
		return fmt.Errorf("unexpected throttlectl state: %s, must be \"on\" or \"off\"", throttlectlState)
	}

	_, err := DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "/usr/bin/throttlectl", throttlectlState)
	if err != nil {
		return fmt.Errorf("failed to run throttlectl %v on %v: %w", throttlectlState, tunedNodeName, err)
	}

	// Poll only the status check to verify the change took effect.
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, time.Duration(timeDurationSec)*time.Second, false, func(_ context.Context) (bool, error) {
		schedRTRuntimeStatus, err := DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "cat", "/proc/sys/kernel/sched_rt_runtime_us")
		if err != nil {
			Logf("failed to get sched_rt_runtime_us from %v: %v, retrying", tunedNodeName, err)
			return false, nil
		}

		// When throttling is disabled the runtime is unlimited (-1); when enabled
		// it is a finite value (e.g. 950000).
		throttlingOff := strings.Contains(schedRTRuntimeStatus, "-1")
		if (throttlectlState == "off" && throttlingOff) || (throttlectlState == "on" && !throttlingOff) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("throttlectl status isn't correct: %w", err)
	}
	return nil
}

// WaitForTunedProfileApplied waits until a tuned profile is applied to a node.
// It first checks if the Profile resource exists, then verifies the profile is applied with the expected status.
// WaitForTunedProfileApplied waits for the tuned Profile resource for tunedNodeName
// to be applied with the expected status (default "True") and to reference tunedName.
func WaitForTunedProfileApplied(ctx context.Context, oc *CLI, namespace string, tunedNodeName string, tunedName string, expectedAppliedStatus ...string) error {
	return WaitForTunedProfileAppliedWithTimeout(ctx, oc, namespace, tunedNodeName, tunedName, 180*time.Second, expectedAppliedStatus...)
}

// WaitForTunedProfileAppliedWithTimeout is like WaitForTunedProfileApplied but
// allows the caller to specify the total timeout for the entire operation
// (profile-existence check plus applied-status poll).
// This is needed after events such as a node reboot that may take several minutes
// before tuned starts and applies the deferred profile.
func WaitForTunedProfileAppliedWithTimeout(ctx context.Context, oc *CLI, namespace string, tunedNodeName string, tunedName string, timeout time.Duration, expectedAppliedStatus ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	status := "True"
	if len(expectedAppliedStatus) > 0 {
		status = expectedAppliedStatus[0]
	}

	// First check if the Profile resource exists
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, false, func(_ context.Context) (bool, error) {
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", tunedNodeName).Output()
		if err != nil || strings.Contains(output, "NotFound") {
			Logf("Profile resource %s not found yet in namespace %s: %v", tunedNodeName, namespace, err)
			return false, nil
		}
		Logf("Profile resource %s found in namespace %s", tunedNodeName, namespace)
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("Profile resource should exist for node %s: %w", tunedNodeName, err)
	}

	// Then check if the profile is applied with the correct status
	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, false, func(_ context.Context) (bool, error) {
		appliedStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", tunedNodeName, `-ojsonpath={.status.conditions[?(@.type=="Applied")].status}`).Output()
		if err != nil {
			Logf("error getting profile status for %s: %v, retrying", tunedNodeName, err)
			return false, nil
		}

		tunedProfile, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", tunedNodeName, "-ojsonpath={.status.tunedProfile}").Output()
		if err != nil {
			Logf("error getting profile status for %s: %v, retrying", tunedNodeName, err)
			return false, nil
		}

		if !strings.Contains(appliedStatus, status) || strings.Contains(appliedStatus, "Unknown") {
			Logf("applied status not matching: got %s, want %s", appliedStatus, status)
			return false, nil
		}

		if tunedProfile != tunedName {
			Logf("Tuned profile name not matching: got %s, want %s", tunedProfile, tunedName)
			return false, nil
		}

		Logf("Profile %s successfully applied to node %s with status %s", tunedName, tunedNodeName, appliedStatus)
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("Profile %s should be applied to node %s: %w", tunedName, tunedNodeName, err)
	}
	return nil
}

// WaitForDefaultProfiles waits for the default tuned profiles to be applied across
// the cluster. Master/control-plane nodes should have "openshift-control-plane" and
// worker nodes should have "openshift-node". On SNO/compact clusters all nodes are
// expected to have "openshift-control-plane".
func WaitForDefaultProfiles(ctx context.Context, oc *CLI, namespace string) {
	const (
		expectedMasterProfile = "openshift-control-plane"
		expectedWorkerProfile = "openshift-node"
		pollInterval          = 10 * time.Second
		timeout               = 5 * time.Minute
	)

	masterNodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/control-plane=", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		Logf("WaitForDefaultProfiles: failed to list master nodes: %v", err)
		return
	}

	workerNodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		Logf("WaitForDefaultProfiles: failed to list worker nodes: %v", err)
		return
	}

	err = wait.PollUntilContextTimeout(ctx, pollInterval, timeout, false, func(_ context.Context) (bool, error) {
		allOk := true

		for _, node := range strings.Fields(masterNodes) {
			profile, err := GetTunedProfile(oc, namespace, node)
			if err != nil || profile != expectedMasterProfile {
				Logf("WaitForDefaultProfiles: node %q has profile %q, expected %q", node, profile, expectedMasterProfile)
				allOk = false
			}
		}

		for _, node := range strings.Fields(workerNodes) {
			// Skip nodes that also have the control-plane label (SNO/compact).
			roleLabels, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node/"+node, "-o", "jsonpath={.metadata.labels}").Output()
			if err != nil {
				Logf("WaitForDefaultProfiles: failed to get labels for node %s: %v", node, err)
				allOk = false
				continue
			}
			if strings.Contains(roleLabels, "node-role.kubernetes.io/control-plane") {
				continue
			}
			profile, err := GetTunedProfile(oc, namespace, node)
			if err != nil || profile != expectedWorkerProfile {
				Logf("WaitForDefaultProfiles: node %q has profile %q, expected %q", node, profile, expectedWorkerProfile)
				allOk = false
			}
		}

		return allOk, nil
	})
	if err != nil {
		Logf("WaitForDefaultProfiles: timed out waiting for default profiles: %v", err)
	}
}

// LogCurrentProfiles retrieves and logs the current tuned profile for each node.
func LogCurrentProfiles(oc *CLI, namespace string) string {
	output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io").Output()
	if err != nil {
		Logf("failed to get current profiles: %v", err)
		return ""
	}
	Logf("current profile for each node: \n%v", output)
	return output
}

// extractKubeletConfValue extracts the value assigned to key in a kubelet.conf
// line of the form `"key": "value",` (grep output), returning "" if key is
// absent or has no value, rather than only reporting whether key appears.
func extractKubeletConfValue(kubeletConfOutput, key string) string {
	for _, line := range strings.Split(kubeletConfOutput, "\n") {
		if !strings.Contains(line, key) {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		value := strings.TrimSpace(parts[1])
		value = strings.TrimSuffix(value, ",")
		value = strings.Trim(value, `"`)
		return value
	}
	return ""
}

// VerifyPAOProfile verifies that a PAO performance profile was applied correctly.
// It checks: tuned profile creation, profile application, hugepages, CPU Manager settings,
// Topology Manager settings, real-time kernel (conditional), and runtimeClass.
func VerifyPAOProfile(ctx context.Context, oc *CLI, namespace string, tunedNodeName string, profileName, runtimeClass string, expectRT bool) error {
	// Check tuned profile was created
	tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "tuned").Output()
	if err != nil {
		return fmt.Errorf("failed to list tuned profiles: %w", err)
	}
	if !strings.Contains(tunedNames, profileName) {
		return fmt.Errorf("tuned profile %s not found: got %s", profileName, tunedNames)
	}

	// Check current profiles
	output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io").Output()
	if err != nil {
		return fmt.Errorf("failed to list profiles: %w", err)
	}
	Logf("current profile for each node: \n%v", output)

	// Wait for tuned profile applied
	err = WaitForTunedProfileApplied(ctx, oc, namespace, tunedNodeName, profileName)
	if err != nil {
		return fmt.Errorf("profile %s not applied on node %s: %w", profileName, tunedNodeName, err)
	}

	// Verify post-application settings with retry. The kubelet may not have
	// restarted with the new configuration yet, so transient failures should
	// log and retry rather than failing the test immediately.
	const (
		pollInterval = 15 * time.Second
		timeout      = 5 * time.Minute
	)
	err = wait.PollUntilContextTimeout(ctx, pollInterval, timeout, false, func(_ context.Context) (bool, error) {
		// Check profile name on node
		nodeProfileName, err := GetTunedProfile(oc, namespace, tunedNodeName)
		if err != nil {
			Logf("failed to get tuned profile on node %s: %v, retrying", tunedNodeName, err)
			return false, nil
		}
		if !strings.Contains(nodeProfileName, profileName) {
			Logf("profile on node %s is %q, expected %q, retrying", tunedNodeName, nodeProfileName, profileName)
			return false, nil
		}

		// Check hugepages-1Gi
		nodeHugePagesOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.allocatable.hugepages-1Gi}").Output()
		if err != nil {
			Logf("failed to get hugepages-1Gi on node %s: %v, retrying", tunedNodeName, err)
			return false, nil
		}
		if !strings.Contains(nodeHugePagesOutput, "1Gi") {
			Logf("hugepages-1Gi on node %s is %q, expected 1Gi, retrying", tunedNodeName, nodeHugePagesOutput)
			return false, nil
		}

		// Check CPU Manager policy
		cpuManagerConfOutput, err := DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "/bin/bash", "-c", "cat /etc/kubernetes/kubelet.conf | grep cpuManager")
		if err != nil {
			Logf("failed to get CPU Manager policy on node %s: %v, retrying", tunedNodeName, err)
			return false, nil
		}
		if cpuManagerConfOutput == "" {
			Logf("CPU Manager policy output is empty on node %s, retrying", tunedNodeName)
			return false, nil
		}
		cpuManagerPolicy := extractKubeletConfValue(cpuManagerConfOutput, "cpuManagerPolicy")
		cpuManagerReconcilePeriod := extractKubeletConfValue(cpuManagerConfOutput, "cpuManagerReconcilePeriod")
		if cpuManagerPolicy == "" || cpuManagerReconcilePeriod == "" {
			Logf("CPU Manager policy on node %s has empty value(s): got %q, retrying", tunedNodeName, cpuManagerConfOutput)
			return false, nil
		}
		Logf("the settings of CPU Manager Policy on labeled nodes: cpuManagerPolicy=%s cpuManagerReconcilePeriod=%s", cpuManagerPolicy, cpuManagerReconcilePeriod)

		// Check CPU Manager reservedSystemCPUs
		cpuManagerConfOutput, err = DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "/bin/bash", "-c", "cat /etc/kubernetes/kubelet.conf | grep reservedSystemCPUs")
		if err != nil {
			Logf("failed to get reservedSystemCPUs on node %s: %v, retrying", tunedNodeName, err)
			return false, nil
		}
		if cpuManagerConfOutput == "" {
			Logf("reservedSystemCPUs output is empty on node %s, retrying", tunedNodeName)
			return false, nil
		}
		reservedSystemCPUs := extractKubeletConfValue(cpuManagerConfOutput, "reservedSystemCPUs")
		if reservedSystemCPUs == "" {
			Logf("reservedSystemCPUs on node %s has empty value: got %q, retrying", tunedNodeName, cpuManagerConfOutput)
			return false, nil
		}
		Logf("the settings of CPU Manager reservedSystemCPUs on labeled nodes: reservedSystemCPUs=%s", reservedSystemCPUs)

		// Check Topology Manager topologyManagerPolicy
		topologyManagerConfOutput, err := DebugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"--quiet=true"}, "/bin/bash", "-c", "cat /etc/kubernetes/kubelet.conf | grep topologyManagerPolicy")
		if err != nil {
			Logf("failed to get topologyManagerPolicy on node %s: %v, retrying", tunedNodeName, err)
			return false, nil
		}
		if topologyManagerConfOutput == "" {
			Logf("topologyManagerPolicy output is empty on node %s, retrying", tunedNodeName)
			return false, nil
		}
		topologyManagerPolicy := extractKubeletConfValue(topologyManagerConfOutput, "topologyManagerPolicy")
		if topologyManagerPolicy == "" {
			Logf("topologyManagerPolicy on node %s has empty value: got %q, retrying", tunedNodeName, topologyManagerConfOutput)
			return false, nil
		}
		Logf("the settings of CPU Manager topologyManagerPolicy on labeled nodes: topologyManagerPolicy=%s", topologyManagerPolicy)

		// Check realTime kernel (conditional). The kernel is a boot-time property
		// that will not change with retries, so a mismatch is a terminal error.
		kernelVersion, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.kernelVersion}").Output()
		if err != nil {
			Logf("failed to get kernel version on node %s: %v, retrying", tunedNodeName, err)
			return false, nil
		}
		if kernelVersion == "" {
			Logf("kernel version on node %s is empty, retrying", tunedNodeName)
			return false, nil
		}
		if expectRT && !strings.Contains(kernelVersion, "rt") {
			return false, fmt.Errorf("expected real-time kernel on node %s but got kernel version: %q", tunedNodeName, kernelVersion)
		}
		if !expectRT && strings.Contains(kernelVersion, "rt") {
			return false, fmt.Errorf("did not expect real-time kernel on node %s but got kernel version: %q", tunedNodeName, kernelVersion)
		}

		// Check runtimeClass
		runtimeClassOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("performanceprofile", runtimeClass, "-ojsonpath={.status.runtimeClass}").Output()
		if err != nil {
			Logf("failed to get runtimeClass for performance profile %s: %v, retrying", runtimeClass, err)
			return false, nil
		}
		if runtimeClassOutput == "" {
			Logf("runtimeClass output is empty for performance profile %s, retrying", runtimeClass)
			return false, nil
		}
		if !strings.Contains(runtimeClassOutput, "performance-"+runtimeClass) {
			Logf("runtimeClass for performance profile %s is %q, expected performance-%s, retrying", runtimeClass, runtimeClassOutput, runtimeClass)
			return false, nil
		}
		Logf("the settings of runtimeClass on labeled nodes: \n%v", runtimeClassOutput)

		return true, nil
	})
	if err != nil {
		return fmt.Errorf("failed to verify PAO profile on node %s: %w", tunedNodeName, err)
	}

	return nil
}
