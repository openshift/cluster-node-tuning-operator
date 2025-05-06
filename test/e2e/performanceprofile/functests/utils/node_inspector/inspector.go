package node_inspector

import (
	"context"
	"fmt"
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
	serviceAccountName := fmt.Sprintf("%s-%s", testutils.NodeInspectorName, serviceAccountSuffix)
	sa := createServiceAccount(serviceAccountName, testutils.NodeInspectorNamespace)
	if err := testclient.DataPlaneClient.Create(ctx, sa); err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
		testlog.Warningf("Node Inspector ServiceAccount %s already exists, this is not expected.", serviceAccountName)
	}
	clusterRoleName := fmt.Sprintf("%s-%s", testutils.NodeInspectorName, clusterRoleSuffix)
	cr := createClusterRole(clusterRoleName)
	if err := testclient.DataPlaneClient.Create(ctx, cr); err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
		testlog.Warningf("Node Inspector ClusterRole %s already exists, this is not expected.", clusterRoleName)
	}
	clusterRoleBindingName := fmt.Sprintf("%s-%s", testutils.NodeInspectorName, clusterRoleBindingSuffix)
	rb := createClusterRoleBinding(clusterRoleBindingName, testutils.NodeInspectorNamespace, serviceAccountName, clusterRoleName)
	if err := testclient.DataPlaneClient.Create(ctx, rb); err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
		testlog.Warningf("Node Inspector ClusterRoleBinding %s already exists, this is not expected.", clusterRoleBindingName)
	}
	ds := createDaemonSet(testutils.NodeInspectorName, testutils.NodeInspectorNamespace, serviceAccountName, images.Test())
	if err := testclient.DataPlaneClient.Create(ctx, ds); err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
		testlog.Warningf("Node Inspector Daemonset %s already exists, this is not expected.", testutils.NodeInspectorName)
	}
	if err := daemonset.WaitToBeRunning(ctx, testclient.DataPlaneClient, testutils.NodeInspectorNamespace, testutils.NodeInspectorName); err != nil {
		return err
	}
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
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("node inspector is not running")
	}
	pod, err := getDaemonPodByNode(ctx, node)
	if err != nil {
		return nil, err
	}
	testlog.Infof("found daemon pod %s for node %s", pod.Name, node.Name)

	return testpods.WaitForPodOutput(ctx, testclient.K8sClient, pod, command)
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
