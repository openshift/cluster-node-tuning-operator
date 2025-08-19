package node_inspector

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/daemonset"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/namespaces"
	testpods "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
)

const serviceAccountSuffix = "sa"
const clusterRoleSuffix = "cr"
const clusterRoleBindingSuffix = "crb"

var initialized bool

// initialize would be used to lazy initialize the node inspector
func initialize(ctx context.Context) error {
	if initialized {
		return nil
	}
	testlog.Info("initializing node inspector")
	// NodeInspectorNamespace is the namespace
	// used for deploying a DaemonSet that will be used to executing commands on nodes.
	nodeInspectorNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testutils.NodeInspectorNamespace,
		},
	}
	err := testclient.DataPlaneClient.Create(ctx, nodeInspectorNamespace)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace: %v", err)
	}

	// Create Node Inspector resources
	err = create(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Node Inspector resources: %v", err)
	}
	return nil
}

func create(ctx context.Context) error {
	testlog.Info("Creating node inspector resources...")

	serviceAccountName := fmt.Sprintf("%s-%s", testutils.NodeInspectorName, serviceAccountSuffix)
	testlog.Infof("Creating ServiceAccount: %s", serviceAccountName)
	sa := createServiceAccount(serviceAccountName, testutils.NodeInspectorNamespace)
	if err := testclient.DataPlaneClient.Create(ctx, sa); err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create ServiceAccount %s: %v", serviceAccountName, err)
		}
		testlog.Warningf("Node Inspector ServiceAccount %s already exists, this is not expected.", serviceAccountName)
	} else {
		testlog.Infof("✅ ServiceAccount %s created successfully", serviceAccountName)
	}

	clusterRoleName := fmt.Sprintf("%s-%s", testutils.NodeInspectorName, clusterRoleSuffix)
	testlog.Infof("Creating ClusterRole: %s", clusterRoleName)
	cr := createClusterRole(clusterRoleName)
	if err := testclient.DataPlaneClient.Create(ctx, cr); err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create ClusterRole %s: %v", clusterRoleName, err)
		}
		testlog.Warningf("Node Inspector ClusterRole %s already exists, this is not expected.", clusterRoleName)
	} else {
		testlog.Infof("✅ ClusterRole %s created successfully", clusterRoleName)
	}

	clusterRoleBindingName := fmt.Sprintf("%s-%s", testutils.NodeInspectorName, clusterRoleBindingSuffix)
	testlog.Infof("Creating ClusterRoleBinding: %s", clusterRoleBindingName)
	rb := createClusterRoleBinding(clusterRoleBindingName, testutils.NodeInspectorNamespace, serviceAccountName, clusterRoleName)
	if err := testclient.DataPlaneClient.Create(ctx, rb); err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create ClusterRoleBinding %s: %v", clusterRoleBindingName, err)
		}
		testlog.Warningf("Node Inspector ClusterRoleBinding %s already exists, this is not expected.", clusterRoleBindingName)
	} else {
		testlog.Infof("✅ ClusterRoleBinding %s created successfully", clusterRoleBindingName)
	}

	testlog.Infof("Creating DaemonSet: %s (image: %s)", testutils.NodeInspectorName, images.Test())
	ds := createDaemonSet(testutils.NodeInspectorName, testutils.NodeInspectorNamespace, serviceAccountName, images.Test())
	if err := testclient.DataPlaneClient.Create(ctx, ds); err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create DaemonSet %s: %v", testutils.NodeInspectorName, err)
		}
		testlog.Warningf("Node Inspector Daemonset %s already exists, this is not expected.", testutils.NodeInspectorName)
	} else {
		testlog.Infof("✅ DaemonSet %s created successfully", testutils.NodeInspectorName)
	}

	testlog.Info("Waiting for node inspector DaemonSet to be running...")
	if err := daemonset.WaitToBeRunning(ctx, testclient.DataPlaneClient, testutils.NodeInspectorNamespace, testutils.NodeInspectorName); err != nil {
		return fmt.Errorf("failed to wait for DaemonSet %s to be running: %v", testutils.NodeInspectorName, err)
	}

	testlog.Info("✅ Node inspector initialized successfully")
	initialized = true

	return nil
}

func Delete(ctx context.Context) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testutils.NodeInspectorNamespace}}
	if err := testclient.DataPlaneClient.Delete(ctx, ns); err != nil {
		if errors.IsNotFound(err) {
			testlog.Warningf("Namespace %s not found, nothing to delete", testutils.NodeInspectorNamespace)
		} else {
			return fmt.Errorf("failed to delete namespace: %v", err)
		}
	}
	if err := namespaces.WaitForDeletion(testutils.NodeInspectorNamespace, 5*time.Minute); err != nil {
		return fmt.Errorf("timed out waiting for deletion of namespace %s: %v", testutils.NodeInspectorNamespace, err)
	}

	cr := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-%s", testutils.NodeInspectorName, clusterRoleSuffix)}}
	if err := testclient.DataPlaneClient.Delete(ctx, cr); err != nil {
		if errors.IsNotFound(err) {
			testlog.Warningf("ClusterRole %s not found, nothing to delete", cr.Name)
		} else {
			return fmt.Errorf("failed to delete ClusterRole: %v", err)
		}
	}
	return nil
}

func isRunning(ctx context.Context) (bool, error) {
	if !initialized {
		if err := initialize(ctx); err != nil {
			return false, err
		}
		return true, nil
	}
	return daemonset.IsRunning(ctx, testclient.DataPlaneClient, testutils.NodeInspectorNamespace, testutils.NodeInspectorName)
}

// getDaemonPodByNode returns the daemon pod that runs on the specified node
func getDaemonPodByNode(ctx context.Context, node *corev1.Node) (*corev1.Pod, error) {
	listOptions := &client.ListOptions{
		Namespace:     testutils.NodeInspectorNamespace,
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}),
		LabelSelector: labels.SelectorFromSet(labels.Set{"name": testutils.NodeInspectorName}),
	}

	pods := &corev1.PodList{}
	if err := testclient.DataPlaneClient.List(ctx, pods, listOptions); err != nil {
		return nil, err
	}
	if len(pods.Items) < 1 {
		return nil, fmt.Errorf("failed to get daemon pod for the node %q", node.Name)
	}
	return &pods.Items[0], nil
}

// ExecCommand executing the command on a daemon pod of the given node
func ExecCommand(ctx context.Context, node *corev1.Node, command []string) ([]byte, error) {
	// Ensure the node inspector is running
	ok, err := isRunning(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check if node inspector is running: %v", err)
	}
	if !ok {
		// Provide detailed debugging information when node inspector is not running
		debugInfo, debugErr := getDetailedDebugInfo(ctx, node)
		if debugErr != nil {
			testlog.Errorf("Failed to gather debug information: %v", debugErr)
			return nil, fmt.Errorf("node inspector is not running (failed to gather debug info: %v)", debugErr)
		}
		testlog.Errorf("Node inspector is not running. Debug information:\n%s", debugInfo)
		return nil, fmt.Errorf("node inspector is not running. Check logs above for detailed debug information")
	}
	pod, err := getDaemonPodByNode(ctx, node)
	if err != nil {
		return nil, err
	}
	testlog.Infof("found daemon pod %s for node %s", pod.Name, node.Name)

	return testpods.WaitForPodOutput(ctx, testclient.K8sClient, pod, command)
}

// getDetailedDebugInfo gathers comprehensive debugging information about the node inspector state
func getDetailedDebugInfo(ctx context.Context, targetNode *corev1.Node) (string, error) {
	var debugInfo strings.Builder

	debugInfo.WriteString("=== NODE INSPECTOR DEBUG INFORMATION ===\n")

	// Check if namespace exists
	ns := &corev1.Namespace{}
	nsKey := client.ObjectKey{Name: testutils.NodeInspectorNamespace}
	if err := testclient.DataPlaneClient.Get(ctx, nsKey, ns); err != nil {
		debugInfo.WriteString(fmt.Sprintf("❌ Namespace %s: %v\n", testutils.NodeInspectorNamespace, err))
	} else {
		debugInfo.WriteString(fmt.Sprintf("✅ Namespace %s: exists (phase: %s)\n", testutils.NodeInspectorNamespace, ns.Status.Phase))
	}

	// Check daemonset status
	ds, err := daemonset.GetByName(ctx, testclient.DataPlaneClient, testutils.NodeInspectorNamespace, testutils.NodeInspectorName)
	if err != nil {
		debugInfo.WriteString(fmt.Sprintf("❌ DaemonSet %s/%s: %v\n", testutils.NodeInspectorNamespace, testutils.NodeInspectorName, err))
	} else {
		debugInfo.WriteString(fmt.Sprintf("✅ DaemonSet %s/%s: exists\n", testutils.NodeInspectorNamespace, testutils.NodeInspectorName))
		debugInfo.WriteString(fmt.Sprintf("   Desired: %d, Current: %d, Ready: %d, Available: %d\n",
			ds.Status.DesiredNumberScheduled, ds.Status.CurrentNumberScheduled,
			ds.Status.NumberReady, ds.Status.NumberAvailable))
		debugInfo.WriteString(fmt.Sprintf("   Updated: %d, Misscheduled: %d\n",
			ds.Status.UpdatedNumberScheduled, ds.Status.NumberMisscheduled))

		// Check if any nodes are schedulable
		if ds.Status.DesiredNumberScheduled == 0 {
			debugInfo.WriteString("⚠️  No nodes are eligible for node inspector scheduling\n")
			debugInfo.WriteString("   Node selector: ")
			for k, v := range ds.Spec.Template.Spec.NodeSelector {
				debugInfo.WriteString(fmt.Sprintf("%s=%s ", k, v))
			}
			debugInfo.WriteString("\n")
		}
	}

	// List and analyze pods
	listOptions := &client.ListOptions{
		Namespace:     testutils.NodeInspectorNamespace,
		LabelSelector: labels.SelectorFromSet(labels.Set{"name": testutils.NodeInspectorName}),
	}

	pods := &corev1.PodList{}
	if err := testclient.DataPlaneClient.List(ctx, pods, listOptions); err != nil {
		debugInfo.WriteString(fmt.Sprintf("❌ Failed to list pods: %v\n", err))
	} else {
		debugInfo.WriteString(fmt.Sprintf("📋 Found %d node inspector pods:\n", len(pods.Items)))

		for i, pod := range pods.Items {
			debugInfo.WriteString(fmt.Sprintf("   Pod %d: %s (node: %s)\n", i+1, pod.Name, pod.Spec.NodeName))
			debugInfo.WriteString(fmt.Sprintf("     Phase: %s, Ready: %v\n", pod.Status.Phase, isPodReady(&pod)))

			if pod.Spec.NodeName == targetNode.Name {
				debugInfo.WriteString("     ⭐ This is the pod for the target node\n")
			}

			// Check container statuses
			for j, containerStatus := range pod.Status.ContainerStatuses {
				debugInfo.WriteString(fmt.Sprintf("     Container %d (%s): Ready=%v, Restarts=%d\n",
					j+1, containerStatus.Name, containerStatus.Ready, containerStatus.RestartCount))

				if containerStatus.State.Waiting != nil {
					debugInfo.WriteString(fmt.Sprintf("       State: Waiting (%s: %s)\n",
						containerStatus.State.Waiting.Reason, containerStatus.State.Waiting.Message))
				} else if containerStatus.State.Running != nil {
					debugInfo.WriteString(fmt.Sprintf("       State: Running (started: %v)\n",
						containerStatus.State.Running.StartedAt))
				} else if containerStatus.State.Terminated != nil {
					debugInfo.WriteString(fmt.Sprintf("       State: Terminated (%s: %s, exit code: %d)\n",
						containerStatus.State.Terminated.Reason, containerStatus.State.Terminated.Message,
						containerStatus.State.Terminated.ExitCode))
				}
			}

			// Check conditions
			for _, condition := range pod.Status.Conditions {
				if condition.Status != corev1.ConditionTrue {
					debugInfo.WriteString(fmt.Sprintf("     Condition %s: %s (%s)\n",
						condition.Type, condition.Status, condition.Message))
				}
			}
		}
	}

	// Check for recent events
	debugInfo.WriteString("\n📅 Recent events:\n")
	if eventErr := logRecentEvents(ctx, &debugInfo); eventErr != nil {
		debugInfo.WriteString(fmt.Sprintf("❌ Failed to get events: %v\n", eventErr))
	}

	// Check node conditions for target node
	debugInfo.WriteString(fmt.Sprintf("\n🖥️  Target node %s conditions:\n", targetNode.Name))
	for _, condition := range targetNode.Status.Conditions {
		debugInfo.WriteString(fmt.Sprintf("   %s: %s (%s)\n",
			condition.Type, condition.Status, condition.Message))
	}

	return debugInfo.String(), nil
}

// isPodReady checks if a pod is ready
func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// logRecentEvents logs recent events related to the node inspector
func logRecentEvents(ctx context.Context, debugInfo *strings.Builder) error {
	// Get all events in the node inspector namespace
	eventsList := &corev1.EventList{}
	err := testclient.DataPlaneClient.List(ctx, eventsList, &client.ListOptions{
		Namespace: testutils.NodeInspectorNamespace,
	})
	if err != nil {
		return err
	}

	// Show only last 10 events
	events := eventsList.Items
	if len(events) > 10 {
		events = events[len(events)-10:]
	}

	for _, event := range events {
		debugInfo.WriteString(fmt.Sprintf("   %s: %s %s %s (%s)\n",
			event.LastTimestamp.Format("15:04:05"), event.Type, event.Reason,
			event.Message, event.InvolvedObject.Name))
	}

	if len(events) == 0 {
		debugInfo.WriteString("   No recent events found\n")
	}

	return nil
}

func createDaemonSet(name, namespace, serviceAccountName, image string) *appsv1.DaemonSet {
	MountPropagationHostToContainer := corev1.MountPropagationHostToContainer
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"name": name,
			},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"target.workload.openshift.io/management": `{"effect": "PreferredDuringScheduling"}`,
					},
					Labels: map[string]string{
						"name": name,
					},
				},
				Spec: corev1.PodSpec{
					HostPID:                       true,
					HostNetwork:                   true,
					ServiceAccountName:            serviceAccountName,
					TerminationGracePeriodSeconds: ptr.To(int64(0)),
					NodeSelector:                  map[string]string{"kubernetes.io/os": "linux"},
					Containers: []corev1.Container{
						{
							Name:            "node-daemon",
							Image:           image,
							Command:         []string{"/bin/bash", "-c", "sleep INF"},
							ImagePullPolicy: corev1.PullAlways,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"cpu":    resource.MustParse("20m"),
									"memory": resource.MustParse("50Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged:             ptr.To(true),
								ReadOnlyRootFilesystem: ptr.To(true),
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath:        "/rootfs",
									Name:             "rootfs",
									MountPropagation: &MountPropagationHostToContainer,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "rootfs",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/",
								},
							},
						},
					},
					Tolerations: []corev1.Toleration{
						{
							Operator: corev1.TolerationOpExists,
						},
					},
				},
			},
		},
	}
}

func createServiceAccount(name, namespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func createClusterRole(name string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups:     []string{"security.openshift.io"},
				ResourceNames: []string{"privileged"},
				Resources:     []string{"securitycontextconstraints"},
				Verbs:         []string{"use"},
			},
		},
	}
}

func createClusterRoleBinding(name, namespace, serviceAccountName, clusterRoleName string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccountName,
				Namespace: namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "ClusterRole",
			Name:     clusterRoleName,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}
}

// CheckStatus provides a detailed status check of the node inspector without executing commands
// This function is useful for debugging when the node inspector is not working
func CheckStatus(ctx context.Context) error {
	testlog.Info("=== NODE INSPECTOR STATUS CHECK ===")

	// Check if we think it's initialized
	testlog.Infof("Initialized flag: %v", initialized)

	// Check if namespace exists
	ns := &corev1.Namespace{}
	nsKey := client.ObjectKey{Name: testutils.NodeInspectorNamespace}
	if err := testclient.DataPlaneClient.Get(ctx, nsKey, ns); err != nil {
		testlog.Errorf("❌ Namespace %s: %v", testutils.NodeInspectorNamespace, err)
		return fmt.Errorf("namespace check failed: %v", err)
	} else {
		testlog.Infof("✅ Namespace %s: exists (phase: %s)", testutils.NodeInspectorNamespace, ns.Status.Phase)
	}

	// Check if running
	running, err := isRunning(ctx)
	if err != nil {
		testlog.Errorf("❌ Failed to check if running: %v", err)
		return fmt.Errorf("running check failed: %v", err)
	}

	if running {
		testlog.Info("✅ Node inspector is running")
	} else {
		testlog.Warning("❌ Node inspector is NOT running")
	}

	// Always show detailed info
	debugInfo, debugErr := getDetailedDebugInfo(ctx, nil)
	if debugErr != nil {
		testlog.Errorf("Failed to gather debug information: %v", debugErr)
	} else {
		testlog.Info(debugInfo)
	}

	return nil
}

// ForceReinitialize deletes and recreates the node inspector (for debugging)
func ForceReinitialize(ctx context.Context) error {
	testlog.Info("Force reinitializing node inspector...")

	// Delete existing resources
	if err := Delete(ctx); err != nil {
		testlog.Warningf("Failed to delete existing node inspector: %v", err)
	}

	// Reset initialized flag
	initialized = false

	// Reinitialize
	return initialize(ctx)
}
