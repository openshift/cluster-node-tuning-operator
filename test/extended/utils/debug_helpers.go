// debug_helpers.go provides debug node operations

package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

// stringsSliceElementsHasPrefix checks if any element in the slice has the given prefix
func stringsSliceElementsHasPrefix(slice []string, prefix string, caseSensitive bool) (bool, int) {
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

// resourceMetadata is the subset of an object's metadata needed for label/annotation lookups.
type resourceMetadata struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

// getResourceMetadata fetches a single resource and decodes its labels/annotations from JSON,
// avoiding the fragile `%v` map serialization produced by `-o=jsonpath={.metadata.labels}`.
func getResourceMetadata(oc *CLI, resource, namespace, name string) (resourceMetadata, error) {
	args := []string{resource}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	if name != "" {
		args = append(args, name)
	}
	args = append(args, "-o=json")
	out, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(args...).Output()
	if err != nil {
		return resourceMetadata{}, err
	}
	var obj struct {
		Metadata resourceMetadata `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		return resourceMetadata{}, err
	}
	return obj.Metadata, nil
}

// isNamespacePrivileged checks if a namespace has privileged security context constraints
func isNamespacePrivileged(oc *CLI, namespace string) (bool, error) {
	meta, err := getResourceMetadata(oc, "namespace", "", namespace)
	if err != nil {
		return false, err
	}
	return meta.Labels["pod-security.kubernetes.io/enforce"] == "privileged" ||
		meta.Labels["security.openshift.io/scc.podSecurityLabelSync"] == "false", nil
}

// setNamespacePrivileged sets privileged labels on a namespace
func setNamespacePrivileged(oc *CLI, namespace string) error {
	err := oc.AsAdmin().WithoutNamespace().Run("label").Args("namespace", namespace, "pod-security.kubernetes.io/enforce=privileged", "pod-security.kubernetes.io/audit=privileged", "pod-security.kubernetes.io/warn=privileged", "security.openshift.io/scc.podSecurityLabelSync=false", "--overwrite").Execute()
	return err
}

// recoverNamespaceRestricted recovers namespace to restricted labels
func recoverNamespaceRestricted(oc *CLI, namespace string) {
	err := oc.AsAdmin().WithoutNamespace().Run("label").Args("namespace", namespace, "pod-security.kubernetes.io/enforce-", "pod-security.kubernetes.io/audit-", "pod-security.kubernetes.io/warn-", "security.openshift.io/scc.podSecurityLabelSync-").Execute()
	if err != nil {
		Logf("recoverNamespaceRestricted: failed to remove labels from namespace %s: %v", namespace, err)
	}
}

// isDefaultNodeSelectorEnabled checks if default node selector is enabled
func isDefaultNodeSelectorEnabled(oc *CLI) bool {
	selector, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("scheduler", "cluster", "-o=jsonpath={.spec.defaultNodeSelector}").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(selector) != ""
}

// isWorkerNode checks if a node has the worker role
func isWorkerNode(oc *CLI, nodeName string) bool {
	meta, err := getResourceMetadata(oc, "node", "", nodeName)
	if err != nil {
		return false
	}
	_, ok := meta.Labels["node-role.kubernetes.io/worker"]
	return ok
}

// isSpecifiedAnnotationKeyExist checks if a specific annotation key exists on a resource
func isSpecifiedAnnotationKeyExist(oc *CLI, resource, namespace, annotationKey string) bool {
	meta, err := getResourceMetadata(oc, resource, namespace, "")
	if err != nil {
		return false
	}
	_, ok := meta.Annotations[annotationKey]
	return ok
}

// addAnnotationsToSpecificResource adds annotations to a specific resource
func addAnnotationsToSpecificResource(oc *CLI, resource, namespace, annotation string) error {
	args := []string{resource}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, annotation, "--overwrite")
	return oc.AsAdmin().WithoutNamespace().Run("annotate").Args(args...).Execute()
}

// removeAnnotationFromSpecificResource removes an annotation from a specific resource
func removeAnnotationFromSpecificResource(oc *CLI, resource, namespace, annotationKey string) error {
	args := []string{resource}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, annotationKey+"-")
	return oc.AsAdmin().WithoutNamespace().Run("annotate").Args(args...).Execute()
}

// ensureNamespacePrivilegedForDebug checks if the namespace needs privileged labels for debug node
// and applies them if necessary. Returns a cleanup function that, when called, restores the
// namespace to its previous state (only if labels were modified).
func ensureNamespacePrivilegedForDebug(oc *CLI, namespace string, recoverLabels bool) (cleanup func(), err error) {
	cleanup = func() {} // no-op default

	if strings.HasPrefix(namespace, "openshift-") {
		return cleanup, nil
	}

	isPrivileged, checkErr := isNamespacePrivileged(oc, namespace)
	if checkErr != nil {
		return cleanup, checkErr
	}
	if isPrivileged {
		return cleanup, nil
	}

	if recoverLabels {
		cleanup = func() { recoverNamespaceRestricted(oc, namespace) }
	}

	if err := setNamespacePrivileged(oc, namespace); err != nil {
		return cleanup, fmt.Errorf("failed to set privileged labels on namespace %s: %w", namespace, err)
	}

	return cleanup, nil
}

// ensureAnnotationForDefaultNodeSelector adds the openshift.io/node-selector annotation to the
// namespace if default nodeSelector is enabled and the target is not a worker node.
// Returns a cleanup function that removes the annotation.
func ensureAnnotationForDefaultNodeSelector(oc *CLI, namespace string, nodeName string) func() {
	if !isDefaultNodeSelectorEnabled(oc) || isWorkerNode(oc, nodeName) || isSpecifiedAnnotationKeyExist(oc, "ns/"+namespace, "", `openshift.io/node-selector`) {
		return func() {}
	}

	if err := addAnnotationsToSpecificResource(oc, "ns/"+namespace, "", `openshift.io/node-selector=`); err != nil {
		Logf("warning: failed to add annotation: %v", err)
	}

	return func() {
		if err := removeAnnotationFromSpecificResource(oc, "ns/"+namespace, "", `openshift.io/node-selector`); err != nil {
			Logf("warning: failed to remove annotation: %v", err)
		}
	}
}

// debugNode is the core function for launching debug containers
func debugNode(oc *CLI, nodeName string, cmdOptions []string, needChroot bool, recoverNsLabels bool, cmd ...string) (string, string, error) {
	cargs := []string{"node/" + nodeName}

	// Enhance for debug node namespace used logic
	// if "--to-namespace=" option is used, then uses the input options' namespace, otherwise use oc.Namespace()
	// if oc.Namespace() is empty, uses "default" namespace instead
	hasToNamespaceInCmdOptions, index := stringsSliceElementsHasPrefix(cmdOptions, "--to-namespace=", false)
	debugNodeNamespace := "default"
	if hasToNamespaceInCmdOptions {
		debugNodeNamespace = strings.TrimPrefix(cmdOptions[index], "--to-namespace=")
	} else if ns := oc.Namespace(); ns != "" {
		debugNodeNamespace = ns
	}

	// Ensure namespace has privileged labels for debug node on 4.12+ clusters.
	// Register cleanup BEFORE the side-effect so it runs even if subsequent steps fail.
	nsCleanup, err := ensureNamespacePrivilegedForDebug(oc, debugNodeNamespace, recoverNsLabels)
	if err != nil {
		return "", "", err
	}
	defer nsCleanup()

	// Ensure annotation to prevent scheduler from overwriting the debug pod's nodeSelector.
	annotationCleanup := ensureAnnotationForDefaultNodeSelector(oc, debugNodeNamespace, nodeName)
	defer annotationCleanup()

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

// DebugNodeWithOptionsAndChrootWithStdErr executes a command on a node with options and chroot,
// returning both stdout and stderr separately.
func DebugNodeWithOptionsAndChrootWithStdErr(oc *CLI, nodeName string, options []string, command ...string) (string, string, error) {
	stdout, stderr, err := debugNode(oc, nodeName, options, true, true, command...)
	return stdout, stderr, err
}

// DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel launches debug container using chroot and with options e.g. --image
// WithoutRecoverNsLabel which will not recover the labels that added for debug node container adapt the podSecurity changed on 4.12+ test clusters
// "security.openshift.io/scc.podSecurityLabelSync=false" And "pod-security.kubernetes.io/enforce=privileged"
func DebugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc *CLI, nodeName string, options []string, cmd ...string) (stdOut string, stdErr string, err error) {
	return debugNode(oc, nodeName, options, true, false, cmd...)
}

// DebugNodeWithChroot executes a command on a node with chroot
func DebugNodeWithChroot(oc *CLI, nodeName string, cmd ...string) (string, error) {
	stdout, _, err := debugNode(oc, nodeName, nil, true, true, cmd...)
	return stdout, err
}

// DebugNodeWithOptionsAndChroot executes a command on a node with options and chroot
func DebugNodeWithOptionsAndChroot(oc *CLI, nodeName string, options []string, cmd ...string) (string, error) {
	stdout, _, err := debugNode(oc, nodeName, options, true, true, cmd...)
	return stdout, err
}

// DebugNodeRetryWithOptionsAndChroot executes a command on a node with retry, options and chroot
func DebugNodeRetryWithOptionsAndChroot(ctx context.Context, oc *CLI, nodeName string, options []string, timeout time.Duration, cmd ...string) (string, error) {
	var output string
	var err error

	pollErr := wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, false, func(_ context.Context) (bool, error) {
		output, err = DebugNodeWithOptionsAndChroot(oc, nodeName, options, cmd...)
		return err == nil, nil
	})

	if pollErr != nil {
		if err != nil {
			return "", fmt.Errorf("DebugNodeRetryWithOptionsAndChroot timed out after %s: %w (last error: %v)", timeout, pollErr, err)
		}
		return "", fmt.Errorf("DebugNodeRetryWithOptionsAndChroot timed out after %s: %w", timeout, pollErr)
	}
	return output, nil
}
