// helpers.go provides general-purpose test helpers that are framework-agnostic with
// no Ginkgo/Gomega dependencies! It replaces functions from
// github.com/openshift/origin/test/extended/util/compat_otp.
// Contents: template/resource helpers, pod/log operations, MachineSet lifecycle,
// systemctl helpers, and base64/string utilities.

package utils

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/yaml"
)

const (
	machineAPINamespace = "openshift-machine-api"
	mapiMachineset      = "machinesets.machine.openshift.io"
	mapiMachine         = "machines.machine.openshift.io"
)

// Sentinel errors for WaitForMachinesRunning so callers can decide to skip.
var (
	ErrInsufficientInstanceCapacity        = fmt.Errorf("insufficient instance capacity")
	ErrInsufficientResources               = fmt.Errorf("insufficient resources")
	ErrPGClusterPlacementGroupNotSupported = fmt.Errorf("pgcluster placement group zone is not supported")
)

// newResourceFromTemplate processes a template and applies resources in a namespace.
func newResourceFromTemplate(oc *CLI, namespace string, args ...string) error {
	processArgs := []string{"process"}
	applyArgs := []string{}
	if len(namespace) != 0 {
		processArgs = append(processArgs, "-n", namespace)
		applyArgs = append(applyArgs, "-n", namespace)
	} else {
		// Cluster-scoped resources have no namespace to pass. Without "-n",
		// "oc process" falls back to contacting the server using the current
		// kubeconfig context's namespace, which may not exist on the target
		// cluster. "--local" avoids the server round-trip entirely.
		processArgs = append(processArgs, "--local=true")
	}
	processArgs = append(processArgs, args...)
	applyArgs = append(applyArgs, "-f", "-")
	processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run(processArgs[0]).Args(processArgs[1:]...).Output()
	if err != nil {
		return fmt.Errorf("failed to process template: %w", err)
	}

	err = oc.AsAdmin().WithoutNamespace().Run("apply").Args(applyArgs...).InputString(processedTemplate).Execute()
	if err != nil {
		return fmt.Errorf("failed to apply resource: %w", err)
	}
	return nil
}

// ApplyNsResourceFromTemplate processes a template and applies resources in a namespace.
func ApplyNsResourceFromTemplate(oc *CLI, namespace string, args ...string) error {
	return newResourceFromTemplate(oc, namespace, args...)
}

// ApplyClusterResourceFromTemplate processes a template and creates cluster-scoped resources.
func ApplyClusterResourceFromTemplate(oc *CLI, args ...string) error {
	return newResourceFromTemplate(oc, "", args...)
}

// CreateOperatorResourceByYaml creates a non-template YAML resource. When namespace is empty, creates cluster-scoped resources.
func CreateOperatorResourceByYaml(oc *CLI, namespace string, yamlFile string) error {
	var err error
	if len(namespace) == 0 {
		err = oc.AsAdmin().WithoutNamespace().Run("create").Args("-f", yamlFile).Execute()
	} else {
		err = oc.AsAdmin().WithoutNamespace().Run("create").Args("-f", yamlFile, "-n", namespace).Execute()
	}
	return err
}

// AssertPodToBeReady waits for a pod to be Running with the Ready condition True.
func AssertPodToBeReady(ctx context.Context, oc *CLI, podName string, namespace string) error {
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 3*time.Minute, false, func(_ context.Context) (bool, error) {
		podStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("pod", podName, "-n", namespace, "-o=jsonpath={.status.phase}").Output()
		if err != nil {
			Logf("error getting status for pod %s/%s: %v, retrying", namespace, podName, err)
			return false, nil
		}
		if podStatus != "Running" {
			return false, nil
		}
		readyStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("pod", podName, "-n", namespace, `-ojsonpath={.status.conditions[?(@.type=="Ready")].status}`).Output()
		if err != nil {
			Logf("error getting Ready condition for pod %s/%s: %v, retrying", namespace, podName, err)
			return false, nil
		}
		return strings.TrimSpace(readyStatus) == "True", nil
	})
	if err != nil {
		return fmt.Errorf("pod %s/%s did not become Ready within timeout: %w", namespace, podName, err)
	}
	return nil
}

// GetPodNodeName gets the node name where a pod is running
func GetPodNodeName(oc *CLI, namespace string, podName string) (string, error) {
	nodeName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("pod", podName, "-n", namespace, "-o=jsonpath={.spec.nodeName}").Output()
	return nodeName, err
}

// LabelPod adds or removes a label from a pod
func LabelPod(oc *CLI, namespace string, podName string, label string) error {
	return oc.AsAdmin().WithoutNamespace().Run("label").Args("pod", podName, "-n", namespace, label, "--overwrite").Execute()
}

// RemoteShPod executes a command in a pod
func RemoteShPod(oc *CLI, namespace string, podName string, cmd ...string) (string, error) {
	if len(cmd) == 0 {
		return "", fmt.Errorf("RemoteShPod: no command provided for pod %s/%s", namespace, podName)
	}
	args := []string{"exec", podName, "-n", namespace, "--"}
	args = append(args, cmd...)
	output, err := oc.AsAdmin().WithoutNamespace().Run(args[0]).Args(args[1:]...).Output()
	return output, err
}

// GetClusterVersion returns the cluster version as string value (Ex: 4.8) and cluster build (Ex: 4.8.0-0.nightly-2021-09-28-165247)
func GetClusterVersion(oc *CLI) (string, string, error) {
	clusterBuild, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("clusterversion", "-o", "jsonpath={..desired.version}").Output()
	if err != nil {
		return "", "", err
	}
	splitValues := strings.Split(clusterBuild, ".")
	if len(splitValues) < 2 {
		return "", clusterBuild, fmt.Errorf("unexpected cluster version format: %q", clusterBuild)
	}
	clusterVersion := splitValues[0] + "." + splitValues[1]
	return clusterVersion, clusterBuild, nil
}

// WaitForMCPUpdate waits until a MachineConfigPool reports fully updated machine counts.
func WaitForMCPUpdate(ctx context.Context, oc *CLI, mcpName string, timeDurationSec int) error {
	// Poll at ~1/10 of the total timeout, but never faster than 5s to avoid busy-waiting
	// when timeDurationSec is small (e.g. < 10).
	pollInterval := time.Duration(timeDurationSec/10) * time.Second
	if pollInterval < 5*time.Second {
		pollInterval = 5 * time.Second
	}
	err := wait.PollUntilContextTimeout(ctx, pollInterval, time.Duration(timeDurationSec)*time.Second, false, func(_ context.Context) (bool, error) {
		var (
			mcpMachineCount         string
			mcpReadyMachineCount    string
			mcpUpdatedMachineCount  string
			mcpDegradedMachineCount string
			mcpUpdatingStatus       string
			mcpUpdatedStatus        string
			err                     error
		)

		mcpUpdatingStatus, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, `-ojsonpath={.status.conditions[?(@.type=="Updating")].status}`).Output()
		if err != nil {
			Logf("MachineConfigPool [%v] failed to get updating status: %v", mcpName, err)
			return false, nil
		}
		if mcpUpdatingStatus == "" {
			Logf("MachineConfigPool [%v] updating status is empty, retrying", mcpName)
			return false, nil
		}
		mcpUpdatedStatus, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, `-ojsonpath={.status.conditions[?(@.type=="Updated")].status}`).Output()
		if err != nil {
			Logf("MachineConfigPool [%v] failed to get updated status: %v", mcpName, err)
			return false, nil
		}
		if mcpUpdatedStatus == "" {
			Logf("MachineConfigPool [%v] updated status is empty, retrying", mcpName)
			return false, nil
		}

		// On SNO the API server may be temporarily unreachable during node reboot;
		// treat any failure here as a retry rather than a hard error.
		mcpMachineCount, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, "-o=jsonpath={.status.machineCount}").Output()
		if err != nil {
			Logf("MachineConfigPool [%v] failed to get machineCount: %v", mcpName, err)
			return false, nil
		}
		mcpReadyMachineCount, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, "-o=jsonpath={.status.readyMachineCount}").Output()
		if err != nil {
			Logf("MachineConfigPool [%v] failed to get readyMachineCount: %v", mcpName, err)
			return false, nil
		}
		mcpUpdatedMachineCount, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, "-o=jsonpath={.status.updatedMachineCount}").Output()
		if err != nil {
			Logf("MachineConfigPool [%v] failed to get updatedMachineCount: %v", mcpName, err)
			return false, nil
		}
		mcpDegradedMachineCount, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, "-o=jsonpath={.status.degradedMachineCount}").Output()
		if err != nil {
			Logf("MachineConfigPool [%v] failed to get degradedMachineCount: %v", mcpName, err)
			return false, nil
		}
		if mcpUpdatingStatus == "False" && mcpUpdatedStatus == "True" && mcpMachineCount == mcpReadyMachineCount && mcpMachineCount == mcpUpdatedMachineCount && mcpDegradedMachineCount == "0" {
			Logf("MachineConfigPool [%v] checks succeeded!", mcpName)
			return true, nil
		}

		Logf("MachineConfigPool [%v] checks failed, the following values were found:\nmachineCount: %v (ready=%v, updated=%v, degraded=%v)\nmcpUpdatingStatus: %v (expected: False)\nmcpUpdatedStatus: %v (expected: True)\nRetrying", mcpName, mcpMachineCount, mcpReadyMachineCount, mcpUpdatedMachineCount, mcpDegradedMachineCount, mcpUpdatingStatus, mcpUpdatedStatus)
		return false, nil
	})
	return err
}

// WaitForMCPUpdateStarted waits until a MachineConfigPool has started (or is
// in the middle of) an update cycle, i.e. its Updating condition is True.
// Unlike WaitForMCPUpdate it does not require the pool to fully converge.  On
// pools where applying a new configuration requires sequential node reboots
// (e.g. the master pool), full convergence routinely outlives the time a
// single test case is allowed to run; tests that only need to verify that the
// pool picked up a new configuration should use this instead of
// WaitForMCPUpdate and await the full convergence in their cleanup.
func WaitForMCPUpdateStarted(ctx context.Context, oc *CLI, mcpName string, timeDurationSec int) error {
	pollInterval := time.Duration(timeDurationSec/10) * time.Second
	if pollInterval < 5*time.Second {
		pollInterval = 5 * time.Second
	}
	err := wait.PollUntilContextTimeout(ctx, pollInterval, time.Duration(timeDurationSec)*time.Second, false, func(_ context.Context) (bool, error) {
		mcpUpdatingStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, `-ojsonpath={.status.conditions[?(@.type=="Updating")].status}`).Output()
		if err != nil {
			Logf("MachineConfigPool [%v] failed to get updating status: %v, retrying", mcpName, err)
			return false, nil
		}
		if mcpUpdatingStatus == "True" {
			Logf("MachineConfigPool [%v] update cycle started", mcpName)
			return true, nil
		}
		Logf("MachineConfigPool [%v] updating status is %q, waiting for the update cycle to start", mcpName, mcpUpdatingStatus)
		return false, nil
	})
	return err
}

// WaitForNoPodsAvailableByKind used for checking no pods in a certain namespace
func WaitForNoPodsAvailableByKind(ctx context.Context, oc *CLI, kind string, name string, namespace string) error {
	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 180*time.Second, false, func(_ context.Context) (bool, error) {
		kindNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(kind, name, "-n", namespace, "-oname").Output()
		if err != nil {
			if strings.Contains(err.Error(), "NotFound") {
				Logf("resource %s/%s not found in %s", kind, name, namespace)
				return true, nil
			}
			Logf("error getting %s/%s in %s: %v, retrying", kind, name, namespace, err)
			return false, nil
		}
		if strings.Contains(kindNames, "NotFound") || strings.Contains(kindNames, "No resources") || len(kindNames) == 0 {
			Logf("all resources of kind %s have been terminated", kind)
			return true, nil
		}
		Logf("the pod is still terminating, waiting for a while: \n%v", kindNames)
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("timed out waiting for %s/%s to disappear in %s: %w", kind, name, namespace, err)
	}
	return nil
}

func getPodLogs(oc *CLI, podName, namespace string) (string, error) {
	return oc.AsAdmin().WithoutNamespace().Run("logs").Args(podName, "-n", namespace).Output()
}

func followPodLogsForKeyword(oc *CLI, podName, namespace, keyword string, durationSeconds int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(durationSeconds)*time.Second)
	defer cancel()

	found := oc.AsAdmin().WithoutNamespace().Run("logs").Args("-f", podName, "-n", namespace).FollowUntilContains(ctx, keyword)
	if found {
		Logf("found keyword %q in pod %s/%s logs", keyword, namespace, podName)
		return true
	}
	Logf("keyword %q not found in pod %s/%s logs within %d seconds", keyword, namespace, podName, durationSeconds)
	return false
}

// AssertPodLogsContain checks if pod logs contain a specific keyword.
func AssertPodLogsContain(oc *CLI, podName, namespace, keyword string) (bool, error) {
	logs, err := getPodLogs(oc, podName, namespace)
	if err != nil {
		return false, fmt.Errorf("failed to get pod logs for %s/%s: %w", namespace, podName, err)
	}
	return strings.Contains(logs, keyword), nil
}

// StreamPodLogsForKeyword follows pod logs (oc logs -f) and returns a channel that
// receives true as soon as keyword appears, or false on timeout. Start this before triggering
// the action that produces the expected log line.
func StreamPodLogsForKeyword(oc *CLI, podName, namespace, keyword string, durationSeconds int) <-chan bool {
	ch := make(chan bool, 1)
	go func() {
		ch <- followPodLogsForKeyword(oc, podName, namespace, keyword, durationSeconds)
	}()
	return ch
}

// MachineSetsExist checks if MachineSet resources exist in the cluster
func MachineSetsExist(oc *CLI) (bool, error) {
	items, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("machinesets", "-A", "-o=jsonpath={.items}").Output()
	if err != nil {
		return false, fmt.Errorf("failed to list machinesets: %w", err)
	}
	items = strings.TrimSpace(items)
	return items != "" && items != "[]", nil
}

// RemoteShPodWithBash executes a bash command in a pod
func RemoteShPodWithBash(oc *CLI, namespace string, podName string, bashCmd string) (string, error) {
	output, err := oc.AsAdmin().WithoutNamespace().Run("exec").Args(podName, "-n", namespace, "--", "bash", "-c", bashCmd).Output()
	return output, err
}

// StringToBASE64 converts a string to base64
func StringToBASE64(input string) string {
	return base64.StdEncoding.EncodeToString([]byte(input))
}

// BASE64DecodeStr decodes a base64-encoded string
func BASE64DecodeStr(src string) (string, error) {
	plaintext, err := base64.StdEncoding.DecodeString(src)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64 string: %w", err)
	}
	return string(plaintext), nil
}

// ImplStringArrayContains checks if a string array contains a specific value
func ImplStringArrayContains(arr []string, str string) bool {
	for _, a := range arr {
		if a == str {
			return true
		}
	}
	return false
}

// CheckPlatform returns the cluster's IaaS platform name.
func CheckPlatform(oc *CLI) (string, error) {
	output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("infrastructure", "cluster", "-o=jsonpath={.status.platformStatus.type}").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get cluster platform: %w", err)
	}
	return strings.ToLower(output), nil
}

// GetFirstLinuxMachineSets returns the first non-windows, non-edge machineset name.
func GetFirstLinuxMachineSets(oc *CLI) (string, error) {
	machinesets, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(mapiMachineset, "-o=jsonpath={.items[*].metadata.name}", "-n", machineAPINamespace).Output()
	if err != nil {
		return "", fmt.Errorf("failed to list machinesets: %w", err)
	}

	var regularMachineset []string
	for _, machineset := range strings.Fields(machinesets) {
		if strings.Contains(machineset, "windows") || strings.Contains(machineset, "edge") {
			continue
		}
		regularMachineset = append(regularMachineset, machineset)
	}
	if len(regularMachineset) == 0 {
		return "", fmt.Errorf("no linux machinesets found")
	}
	return regularMachineset[0], nil
}

// GetMachineSetInstanceType returns the instance type of the first linux machineset.
func GetMachineSetInstanceType(oc *CLI) (string, error) {
	firstMachinesetName, err := GetFirstLinuxMachineSets(oc)
	if err != nil {
		return "", err
	}
	Logf("got %v from machineset list", firstMachinesetName)

	iaasPlatform, err := CheckPlatform(oc)
	if err != nil {
		return "", err
	}
	var instanceType string
	switch iaasPlatform {
	case "aws":
		instanceType, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", firstMachinesetName, "-n", machineAPINamespace, "-ojsonpath={.spec.template.spec.providerSpec.value.instanceType}").Output()
	case "azure":
		instanceType, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", firstMachinesetName, "-n", machineAPINamespace, "-ojsonpath={.spec.template.spec.providerSpec.value.vmSize}").Output()
	case "gcp":
		instanceType, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", firstMachinesetName, "-n", machineAPINamespace, "-ojsonpath={.spec.template.spec.providerSpec.value.machineType}").Output()
	case "ibmcloud":
		instanceType, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", firstMachinesetName, "-n", machineAPINamespace, "-ojsonpath={.spec.template.spec.providerSpec.value.profile}").Output()
	case "alibabacloud":
		instanceType, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", firstMachinesetName, "-n", machineAPINamespace, "-ojsonpath={.spec.template.spec.providerSpec.value.instanceType}").Output()
	default:
		return "", fmt.Errorf("unsupported platform %q", iaasPlatform)
	}
	if err != nil {
		return "", fmt.Errorf("failed to get instance type for machineset %s: %w", firstMachinesetName, err)
	}
	if instanceType == "" {
		return "", fmt.Errorf("instance type is empty for machineset %s on platform %s", firstMachinesetName, iaasPlatform)
	}
	return instanceType, nil
}

// hasTokenSuffix reports whether s ends with sub as a whole size token, i.e.
// sub is the entire string or is preceded by a "." "-" or "_" separator.
func hasTokenSuffix(s, sub string) bool {
	if s == sub {
		return true
	}
	if !strings.HasSuffix(s, sub) {
		return false
	}
	sepIdx := len(s) - len(sub) - 1
	if sepIdx < 0 {
		return false
	}
	switch s[sepIdx] {
	case '.', '-', '_':
		return true
	default:
		return false
	}
}

func converseInstanceType(currentInstanceType, sSubString, tSubString string) string {
	if hasTokenSuffix(currentInstanceType, sSubString) {
		return strings.ReplaceAll(currentInstanceType, sSubString, tSubString)
	}
	if hasTokenSuffix(currentInstanceType, tSubString) {
		return strings.ReplaceAll(currentInstanceType, tSubString, sSubString)
	}
	Logf("converseInstanceType: instance type %q does not contain %q or %q, returning empty", currentInstanceType, sSubString, tSubString)
	return ""
}

// SpecifyMachinesetWithDifferentInstanceType used for specify cpu type that different from default one.
func SpecifyMachinesetWithDifferentInstanceType(oc *CLI) (string, error) {
	var expectedInstanceType string
	// Check cloud provider name
	iaasPlatform, err := CheckPlatform(oc)
	if err != nil {
		return "", err
	}

	// Get instance type of the first machineset
	currentInstanceType, err := GetMachineSetInstanceType(oc)
	if err != nil {
		return "", err
	}

	switch iaasPlatform {
	case "aws":
		// we use m6i.2xlarge as default instance type, if current machineset instanceType is "m6i.2xlarge", we use "m6i.xlarge"
		expectedInstanceType = converseInstanceType(currentInstanceType, "2xlarge", "xlarge")
		if len(expectedInstanceType) == 0 {
			expectedInstanceType = "m6i.xlarge"
		}
	case "azure":
		// we use Standard_DS3_v2 as default instance type, if current machineset instanceType is "Standard_DS3_v2", we use "Standard_DS2_v2"
		expectedInstanceType = converseInstanceType(currentInstanceType, "DS3_v2", "DS2_v2")
		if len(expectedInstanceType) == 0 {
			expectedInstanceType = "Standard_DS2_v2"
		}
	case "gcp":
		// we use n1-standard-4 as default instance type, if current machineset instanceType is "n1-standard-4", we use "n1-standard-2"
		expectedInstanceType = converseInstanceType(currentInstanceType, "standard-4", "standard-2")
		if len(expectedInstanceType) == 0 {
			expectedInstanceType = "n1-standard-2"
		}

	case "ibmcloud":
		// we use bx2-4x16 as default instance type, if current machineset instanceType is "bx2-4x16", we use "bx2d-2x8"
		expectedInstanceType = converseInstanceType(currentInstanceType, "4x16", "2x8")
		if len(expectedInstanceType) == 0 {
			expectedInstanceType = "bx2d-2x8"
		}
	case "alibabacloud":
		// we use ecs.g6.xlarge as default instance type, if current machineset instanceType is "ecs.g6.xlarge", we use "ecs.g6.large"
		expectedInstanceType = converseInstanceType(currentInstanceType, "xlarge", "large")
		if len(expectedInstanceType) == 0 {
			expectedInstanceType = "ecs.g6.large"
		}
	default:
		return "", fmt.Errorf("unsupported cloud provider %q", iaasPlatform)
	}
	return expectedInstanceType, nil
}

// replaceYAMLLineValue replaces the value of the (first) "key:" occurrence on every
// line of yaml that contains "key:", preserving any leading whitespace.
func replaceYAMLLineValue(yaml, key, value string) string {
	needle := key + ":"
	lines := strings.Split(yaml, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, needle); idx >= 0 {
			lines[i] = line[:idx] + needle + " " + value
		}
	}
	return strings.Join(lines, "\n")
}

// CreateMachinesetByInstanceType creates a machineset with the specified name and instance type.
func CreateMachinesetByInstanceType(oc *CLI, machinesetName string, instanceType string) error {
	ocGetMachineset, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(mapiMachineset, "-n", machineAPINamespace, "-oname").Output()
	if err != nil {
		return fmt.Errorf("failed to list machinesets: %w", err)
	}
	if ocGetMachineset == "" {
		return fmt.Errorf("no machinesets found")
	}
	Logf("existing machinesets:\n%v", ocGetMachineset)

	firstMachinesetName, err := GetFirstLinuxMachineSets(oc)
	if err != nil {
		return err
	}
	Logf("got %v from machineset list", firstMachinesetName)

	machinesetYamlOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(mapiMachineset, firstMachinesetName, "-n", machineAPINamespace, "-oyaml").Output()
	if err != nil {
		return fmt.Errorf("failed to get machineset %s: %w", firstMachinesetName, err)
	}
	if machinesetYamlOutput == "" {
		return fmt.Errorf("machineset %s YAML output is empty", firstMachinesetName)
	}

	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(machinesetYamlOutput), &obj.Object); err != nil {
		return fmt.Errorf("failed to parse machineset %s YAML: %w", firstMachinesetName, err)
	}

	// Strip server-managed metadata and status so the fetched machineset can be
	// recreated under a new name; ownerReferences are intentionally preserved.
	unstructured.RemoveNestedField(obj.Object, "metadata", "resourceVersion")
	unstructured.RemoveNestedField(obj.Object, "metadata", "uid")
	unstructured.RemoveNestedField(obj.Object, "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(obj.Object, "metadata", "generation")
	unstructured.RemoveNestedField(obj.Object, "metadata", "selfLink")
	unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")
	unstructured.RemoveNestedField(obj.Object, "status")

	obj.SetName(machinesetName)
	// The machineset-name label must match metadata.name for the machineset
	// controller to adopt the machines it creates.
	if err := unstructured.SetNestedField(obj.Object, machinesetName, "spec", "selector", "matchLabels", "machine.openshift.io/cluster-api-machineset"); err != nil {
		return fmt.Errorf("failed to set selector label for machineset %s: %w", machinesetName, err)
	}
	if err := unstructured.SetNestedField(obj.Object, machinesetName, "spec", "template", "metadata", "labels", "machine.openshift.io/cluster-api-machineset"); err != nil {
		return fmt.Errorf("failed to set template label for machineset %s: %w", machinesetName, err)
	}

	sanitizedYamlOutput, err := yaml.Marshal(obj.Object)
	if err != nil {
		return fmt.Errorf("failed to serialize machineset %s: %w", machinesetName, err)
	}
	newMachinesetYaml := string(sanitizedYamlOutput)

	iaasPlatform, err := CheckPlatform(oc)
	if err != nil {
		return err
	}
	switch iaasPlatform {
	case "aws", "alibabacloud":
		Logf("instanceType is %v inside CreateMachinesetByInstanceType", instanceType)
		newMachinesetYaml = replaceYAMLLineValue(newMachinesetYaml, "instanceType", instanceType)
	case "gcp":
		Logf("machineType is %v inside CreateMachinesetByInstanceType", instanceType)
		newMachinesetYaml = replaceYAMLLineValue(newMachinesetYaml, "machineType", instanceType)
	case "azure":
		Logf("vmSize is %v inside CreateMachinesetByInstanceType", instanceType)
		newMachinesetYaml = replaceYAMLLineValue(newMachinesetYaml, "vmSize", instanceType)
	case "ibmcloud":
		Logf("profile is %v inside CreateMachinesetByInstanceType", instanceType)
		newMachinesetYaml = replaceYAMLLineValue(newMachinesetYaml, "profile", instanceType)
	default:
		Logf("unsupported instance: %v", instanceType)
	}

	newMachinesetYaml = replaceYAMLLineValue(newMachinesetYaml, "replicas", "1")

	return oc.AsAdmin().WithoutNamespace().Run("create").Args("-f", "-", "-n", machineAPINamespace).InputString(newMachinesetYaml).Execute()
}

func getMachineSetReplicas(oc *CLI, machineSetName string) (int, error) {
	replicasVal, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(mapiMachineset, machineSetName, "-o=jsonpath={.spec.replicas}", "-n", machineAPINamespace).Output()
	if err != nil {
		return 0, fmt.Errorf("failed to get replicas for machineset %s: %w", machineSetName, err)
	}
	replicas, err := strconv.Atoi(replicasVal)
	if err != nil {
		return 0, fmt.Errorf("failed to parse replicas %q for machineset %s: %w", replicasVal, machineSetName, err)
	}
	return replicas, nil
}

func getNodeNamesFromMachineSet(oc *CLI, machineSetName string) ([]string, error) {
	nodeNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(mapiMachine, "-o=jsonpath={.items[*].status.nodeRef.name}", "-l", "machine.openshift.io/cluster-api-machineset="+machineSetName, "-n", machineAPINamespace).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes for machineset %s: %w", machineSetName, err)
	}
	if nodeNames == "" {
		return nil, nil
	}
	return strings.Fields(nodeNames), nil
}

func waitForNodesReady(ctx context.Context, oc *CLI, machineSetName string) error {
	machineNumber, err := getMachineSetReplicas(oc, machineSetName)
	if err != nil {
		return err
	}
	if machineNumber < 1 {
		return nil
	}

	Logf("wait nodes ready then check nodes haven't uninitialized taints")
	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 180*time.Second, false, func(ctx context.Context) (bool, error) {
		nodeNames, nodeErr := getNodeNamesFromMachineSet(oc, machineSetName)
		if nodeErr != nil {
			Logf("error getting node names for machineset %s: %v, retrying", machineSetName, nodeErr)
			return false, nil
		}
		if len(nodeNames) == 0 {
			Logf("no nodes bound yet for machineset %s (expected %d), retrying", machineSetName, machineNumber)
			return false, nil
		}
		for _, nodeName := range nodeNames {
			readyStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", nodeName, "-o=jsonpath={.status.conditions[?(@.type==\"Ready\")].status}").Output()
			if err != nil {
				if strings.Contains(err.Error(), "NotFound") {
					Logf("node %s does not exist, skipping", nodeName)
					continue
				}
				Logf("error getting ready status for node %s: %v, retrying", nodeName, err)
				return false, nil
			}
			Logf("node %s readyStatus: %s", nodeName, readyStatus)
			if readyStatus != "True" {
				return false, nil
			}
			taints, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", nodeName, "-o=jsonpath={.spec.taints}").Output()
			if err != nil {
				Logf("error getting taints for node %s: %v, retrying", nodeName, err)
				return false, nil
			}
			if strings.Contains(taints, "uninitialized") {
				Logf("node %s has uninitialized taint %s, retrying", nodeName, taints)
				return false, nil
			}
		}
		Logf("all nodes are ready and haven't uninitialized taints")
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("some nodes are not ready in 3 minutes for machineset %s: %w", machineSetName, err)
	}
	return nil
}

// WaitForMachinesRunning waits for machines in a machineset to be running.
func WaitForMachinesRunning(ctx context.Context, oc *CLI, machineNumber int, machineSetName string) error {
	Logf("waiting for the machines Running")

	pollErr := wait.PollUntilContextTimeout(ctx, 60*time.Second, 1200*time.Second, false, func(_ context.Context) (bool, error) {
		msg, getErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(mapiMachineset, machineSetName, "-o=jsonpath={.status.readyReplicas}", "-n", machineAPINamespace).Output()
		if getErr != nil {
			Logf("error getting readyReplicas for machineset %s: %v, retrying", machineSetName, getErr)
			return false, nil
		}
		if strings.TrimSpace(msg) == "" {
			Logf("readyReplicas for machineset %s is not set yet, retrying", machineSetName)
			return false, nil
		}
		machinesRunning, convErr := strconv.Atoi(msg)
		if convErr != nil {
			Logf("failed to parse readyReplicas %q for machineset %s: %v, retrying", msg, machineSetName, convErr)
			return false, nil
		}
		if machinesRunning != machineNumber {
			phase, getPhaseErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(mapiMachine, "-n", machineAPINamespace, "-l", "machine.openshift.io/cluster-api-machineset="+machineSetName, "-o=jsonpath={.items[*].status.phase}").Output()
			if getPhaseErr != nil {
				Logf("error getting machine phases for machineset %s: %v, retrying", machineSetName, getPhaseErr)
				return false, nil
			}
			if strings.Contains(phase, "Failed") {
				output, dumpErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(mapiMachine, "-n", machineAPINamespace, "-l", "machine.openshift.io/cluster-api-machineset="+machineSetName, "-o=yaml").Output()
				if dumpErr != nil {
					Logf("error dumping machine YAML for machineset %s: %v", machineSetName, dumpErr)
				}
				Logf("%v", output)
				if strings.Contains(output, "error launching instance: Instances in the pgcluster Placement Group") {
					return false, ErrPGClusterPlacementGroupNotSupported
				}
				return false, fmt.Errorf("some machine go into Failed phase")
			}
			if strings.Contains(phase, "Provisioning") {
				output, dumpErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(mapiMachine, "-n", machineAPINamespace, "-l", "machine.openshift.io/cluster-api-machineset="+machineSetName, "-o=yaml").Output()
				if dumpErr != nil {
					Logf("error dumping machine YAML for machineset %s: %v", machineSetName, dumpErr)
				}
				if strings.Contains(output, "InsufficientInstanceCapacity") || strings.Contains(output, "InsufficientCapacityOnOutpost") {
					Logf("%v", output)
					return false, ErrInsufficientInstanceCapacity
				}
				if strings.Contains(output, "InsufficientResources") {
					Logf("%v", output)
					return false, ErrInsufficientResources
				}
			}
			Logf("expected %v machine are not Running yet and waiting up to 1 minutes", machineNumber)
			return false, nil
		}
		Logf("expected %v machines are Running", machineNumber)
		return true, nil
	})

	if pollErr != nil {
		switch {
		case errors.Is(pollErr, ErrInsufficientInstanceCapacity):
			return ErrInsufficientInstanceCapacity
		case errors.Is(pollErr, ErrInsufficientResources):
			return ErrInsufficientResources
		case errors.Is(pollErr, ErrPGClusterPlacementGroupNotSupported):
			return ErrPGClusterPlacementGroupNotSupported
		default:
			output, dumpErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(mapiMachine, "-n", machineAPINamespace, "-l", "machine.openshift.io/cluster-api-machineset="+machineSetName, "-o=yaml").Output()
			if dumpErr != nil {
				Logf("error dumping machine YAML for machineset %s: %v", machineSetName, dumpErr)
			}
			Logf("%v", output)
			return fmt.Errorf("expected %d machines are not Running after waiting up to 20 minutes: %w", machineNumber, pollErr)
		}
	}

	Logf("all machines are Running")
	if machineNumber >= 1 {
		if err := waitForNodesReady(ctx, oc, machineSetName); err != nil {
			return err
		}
	}
	return nil
}

// GetNodeNameByMachineset returns the node name for the first machine in a machineset.
func GetNodeNameByMachineset(oc *CLI, machinesetName string) (string, error) {
	machinesetLabels, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(mapiMachineset, machinesetName, "-n", machineAPINamespace, "-ojsonpath={.spec.selector.matchLabels.machine\\.openshift\\.io/cluster-api-machineset}").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get machineset labels for %s: %w", machinesetName, err)
	}
	if machinesetLabels == "" {
		return "", fmt.Errorf("machineset labels are empty for %s", machinesetName)
	}

	machineNameStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(mapiMachine, "-l", "machine.openshift.io/cluster-api-machineset="+machinesetLabels, "-n", machineAPINamespace, "-oname").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get machines for machineset %s: %w", machinesetName, err)
	}
	if machineNameStr == "" {
		return "", fmt.Errorf("no machines found for machineset %s", machinesetName)
	}

	machineNames := strings.Fields(machineNameStr)
	if len(machineNames) == 0 {
		return "", fmt.Errorf("no machine names parsed from output for machineset %s", machinesetName)
	}
	machineName := machineNames[0]
	Logf("machineName is %v in GetNodeNameByMachineset", machineName)

	nodeName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(machineName, "-n", machineAPINamespace, "-ojsonpath={.status.nodeRef.name}").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get node name for machine %s: %w", machineName, err)
	}
	if nodeName == "" {
		return "", fmt.Errorf("node name is empty for machine %s", machineName)
	}
	return nodeName, nil
}

// CountLinuxWorkerNodes counts Linux worker nodes
func CountLinuxWorkerNodes(oc *CLI) (int, error) {
	nodeList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=,kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return 0, fmt.Errorf("failed to list Linux worker nodes: %w", err)
	}
	nodes := strings.Fields(nodeList)
	return len(nodes), nil
}

// ShowSystemctlPropertyValueOfServiceUnitByName returns a systemctl show property value for a service unit.
func ShowSystemctlPropertyValueOfServiceUnitByName(oc *CLI, tunedNodeName string, ntoNamespace string, serviceUnit string, propertyName string) (string, error) {
	debugOptions := []string{"-q", "--to-namespace=" + ntoNamespace}
	allProperties, err := DebugNodeWithOptionsAndChroot(oc, tunedNodeName, debugOptions, "systemctl", "show", serviceUnit)
	if err != nil {
		return "", fmt.Errorf("failed to get properties for service %s on node %s: %w", serviceUnit, tunedNodeName, err)
	}

	var propertyValue string
	if strings.Contains(allProperties, propertyName) {
		propertyValue, err = DebugNodeWithOptionsAndChroot(oc, tunedNodeName, debugOptions, "systemctl", "show", "-p", propertyName, serviceUnit)
		if err != nil {
			return "", fmt.Errorf("failed to get property %s for service %s on node %s: %w", propertyName, serviceUnit, tunedNodeName, err)
		}
	} else {
		return "", fmt.Errorf("property %s not found in output for service %s on node %s", propertyName, serviceUnit, tunedNodeName)
	}
	return strings.TrimSpace(propertyValue), nil
}

// GetSystemctlServiceUnitTimestampByPropertyNameWithMonotonic extracts a monotonic timestamp from a systemctl property value.
func GetSystemctlServiceUnitTimestampByPropertyNameWithMonotonic(propertyValue string) (int, error) {
	if !strings.Contains(propertyValue, "=") {
		return 0, fmt.Errorf("property value %q does not contain '=' separator", propertyValue)
	}
	if !strings.Contains(propertyValue, "Monotonic") {
		return 0, fmt.Errorf("property value %q does not contain 'Monotonic'", propertyValue)
	}
	serviceUnitTimestampArr := strings.Split(propertyValue, "=")
	if len(serviceUnitTimestampArr) < 2 {
		return 0, fmt.Errorf("property value %q has no value after '=' separator", propertyValue)
	}
	serviceUnitTimestamp, err := strconv.Atoi(serviceUnitTimestampArr[1])
	if err != nil {
		return 0, fmt.Errorf("failed to parse timestamp from %q: %w", propertyValue, err)
	}
	if serviceUnitTimestamp == 0 {
		return 0, fmt.Errorf("timestamp is 0 for property value %q — service may have never started", propertyValue)
	}
	Logf("the serviceUnitTimestamp is [ %v ]", serviceUnitTimestamp)
	return serviceUnitTimestamp, nil
}
