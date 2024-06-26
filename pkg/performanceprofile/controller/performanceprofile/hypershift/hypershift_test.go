package hypershift

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
)

func TestControlPlaneClientImpl_Get(t *testing.T) {
	machineConfig1 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: config-1
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello World\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/file1.sh
`
	coreMachineConfig1 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: core-config-1
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello Core\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/core.sh
`

	kubeletConfig1 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: set-max-pods
spec:
  kubeletConfig:
    maxPods: 100
`
	perfprofOne := `apiVersion: performance.openshift.io/v2
kind: PerformanceProfile
metadata:
    name: perfprofOne
spec:
    cpu:
        isolated: 1,3-39,41,43-79
        reserved: 0,2,40,42
    machineConfigPoolSelector:
        machineconfiguration.openshift.io/role: worker-cnf
    nodeSelector:
        node-role.kubernetes.io/worker-cnf: ""
    numa:
        topologyPolicy: restricted
    realTimeKernel:
        enabled: true
    workloadHints:
        highPowerConsumption: false
        realTime: true
`
	if err := performancev2.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}
	if err := machineconfigv1.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}
	namespace := "test"
	testsCases := []struct {
		name                  string
		encapsulatedObjsToGet []client.Object
		configMaps            []runtime.Object
		expectedInNotFoundErr bool
	}{
		{
			name: "encapsulated object name equal to configmap name",
			encapsulatedObjsToGet: []client.Object{
				&machineconfigv1.MachineConfig{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name: "config-1",
					},
				},
				&machineconfigv1.MachineConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name: "core-config-1",
					},
				},
			},
			configMaps: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "config-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						ConfigKey: machineConfig1,
					},
					BinaryData: nil,
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-config-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						ConfigKey: coreMachineConfig1,
					},
				},
			},
		},
		{
			name: "encapsulated performanceprofile name not equal to configmap name",
			encapsulatedObjsToGet: []client.Object{
				&performancev2.PerformanceProfile{
					ObjectMeta: metav1.ObjectMeta{
						Name: "perfprofOne",
					},
				},
			},
			configMaps: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "configmap-performance-profile-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TuningKey: perfprofOne,
					},
				},
			},
		},
		{
			name: "encapsulated object name not equal to configmap name",
			encapsulatedObjsToGet: []client.Object{
				&machineconfigv1.MachineConfig{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name: "config-1",
					},
				},
				&machineconfigv1.MachineConfig{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name: "core-config-1",
					},
				},
				&machineconfigv1.KubeletConfig{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name: "set-max-pods",
					},
				},
			},
			configMaps: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "config-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						ConfigKey: machineConfig1,
					},
					BinaryData: nil,
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-config-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						ConfigKey: coreMachineConfig1,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubelet-config-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						ConfigKey: kubeletConfig1,
					},
				},
			},
		},
		{
			name: "provided wrong hosted control plane namespace name",
			encapsulatedObjsToGet: []client.Object{
				&machineconfigv1.MachineConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name: "config-1",
					},
				},
			},
			configMaps: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "config-1",
						Namespace: "wrong-namespace",
					},
					Data: map[string]string{
						ConfigKey: machineConfig1,
					},
					BinaryData: nil,
				},
			},
			expectedInNotFoundErr: true,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tc.configMaps...).Build()
			c := NewControlPlaneClient(fakeClient, namespace)
			for _, objectToGet := range tc.encapsulatedObjsToGet {
				err := c.Get(context.TODO(), client.ObjectKeyFromObject(objectToGet), objectToGet)
				if !tc.expectedInNotFoundErr && err != nil {
					t.Errorf("failed to get object %v; err: %v", objectToGet, err)
				}
				if tc.expectedInNotFoundErr && !apierrors.IsNotFound(err) {
					t.Errorf("expected IsNotFound error, actual error %v ", err)
				}
			}
		})
	}
}
