package controller

import (
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (h *Handler) generateDaemonSet(serviceAccount *corev1.ServiceAccount) *appsv1.DaemonSet {
	bTrue := true
	typeDir := corev1.HostPathDirectory
	imageTuned := os.Getenv("CLUSTER_NODE_TUNED_IMAGE")

	ds := &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "DaemonSet"},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-cluster-node-tuning",
			Name:      "tuned",
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"openshift-app": "tuned",
				},
			},
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
				Type: appsv1.RollingUpdateDaemonSetStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"openshift-app": "tuned",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "tuned",
					Containers: []corev1.Container{
						{
							Name:  "cluster-node-tuned",
							Image: imageTuned,
							SecurityContext: &corev1.SecurityContext{
								Privileged: &bTrue,
							},
							Env: []corev1.EnvVar{
								{
									Name: "OCP_NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "spec.nodeName",
										},
									},
								},
								{
									Name:  "RESYNC_PERIOD",
									Value: "60",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: "/etc/tuned/recommend.d",
									Name:      "etc-tuned-recommend",
								},
								{
									MountPath: "/var/lib/tuned/profiles-data",
									Name:      "var-lib-tuned-profiles-data",
								},
								{
									MountPath: "/sys",
									Name:      "sys",
								},
								{
									MountPath: "/var/run/dbus",
									Name:      "var-run-dbus",
									ReadOnly:  bTrue,
								},
								{
									MountPath: "/run/systemd/system",
									Name:      "run-systemd-system",
									ReadOnly:  bTrue,
								},
							},
						},
					},
					HostNetwork: bTrue,
					Tolerations: []corev1.Toleration{
						{
							Operator: "Exists",
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "etc-tuned-recommend",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "tuned-recommend",
									},
									Items: []corev1.KeyToPath{
										{
											Key:  "tuned-ocp-recommend",
											Path: "50-openshift.conf",
										},
									},
								},
							},
						},
						{
							Name: "var-lib-tuned-profiles-data",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "tuned-profiles",
									},
									Items: []corev1.KeyToPath{
										{
											Key:  "tuned-profiles-data",
											Path: "tuned-profiles.yaml",
										},
									},
								},
							},
						},
						{
							Name: "sys",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/sys",
									Type: &typeDir,
								},
							},
						},
						{
							Name: "var-run-dbus",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/run/dbus",
									Type: &typeDir,
								},
							},
						},
						{
							Name: "run-systemd-system",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/run/systemd/system",
									Type: &typeDir,
								},
							},
						},
					},
				},
			},
		},
	}

	return ds
}
