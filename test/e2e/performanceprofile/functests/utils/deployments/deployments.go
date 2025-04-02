package deployments

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
)

func Make(name, namespace string, opts ...func(dp *appsv1.Deployment)) *appsv1.Deployment {
	dp := GetTestDeployment(name, namespace)
	for _, opt := range opts {
		opt(dp)
	}
	return dp
}

func GetTestDeployment(name, namespace string) *appsv1.Deployment {
	replicas := int32(1)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"test": "",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"test": "",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "test",
							Image:   images.Test(),
							Command: []string{"sleep", "10h"},
						},
					},
				},
			},
		},
	}
}

func WithNodeSelector(nodeSelector map[string]string) func(dp *appsv1.Deployment) {
	return func(dp *appsv1.Deployment) {
		dp.Spec.Template.Spec.NodeSelector = nodeSelector
	}
}

func WithReplicas(replicas int32) func(dp *appsv1.Deployment) {
	return func(dp *appsv1.Deployment) {
		dp.Spec.Replicas = &replicas
	}
}

func WithPodTemplate(podTemplate *corev1.Pod) func(dp *appsv1.Deployment) {
	return func(dp *appsv1.Deployment) {
		dp.Spec.Template.Spec = podTemplate.Spec
		dp.Spec.Template.ObjectMeta.Labels = podTemplate.ObjectMeta.Labels
		dp.Spec.Selector.MatchLabels = podTemplate.ObjectMeta.Labels
	}
}

func IsReady(ctx context.Context, cli client.Client, listOptions *client.ListOptions, podList *corev1.PodList, dp *appsv1.Deployment) (bool, error) {
	if err := cli.List(ctx, podList, listOptions); err != nil || len(podList.Items) == 0 {
		return false, err
	}

	for _, pod := range podList.Items {
		_, err := pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(&pod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
		if err != nil {
			return false, err
		}
	}

	return true, nil
}

// WaitForDesiredDeploymentStatus Wait for deployment comes to the desired status
// This is useful when we reboot the nodes, we do not exactly know when the deployment will
// be running, so in the test we check if the deployment has reached the desiredStatus before
// quering its pods
func WaitForDesiredDeploymentStatus(ctx context.Context, deployment *appsv1.Deployment, cli client.Client, namespace, name string, desiredStatus appsv1.DeploymentStatus) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		// get the latest deployment
		deployment := &appsv1.Deployment{}
		err := cli.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, deployment)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, fmt.Errorf("deployment not found")
			}
			return false, err
		}
		if deployment.Status.Replicas == desiredStatus.Replicas && deployment.Status.AvailableReplicas == desiredStatus.AvailableReplicas {
			return true, nil
		}
		return false, nil
	})
}
