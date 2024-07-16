package hypershift

import (
	"context"
	"fmt"
	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v2"
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
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	hypershiftconsts "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/consts"
)

const machineConfig1 = `
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
const coreMachineConfig1 = `
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

const kubeletConfig1 = `
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: set-max-pods
spec:
  kubeletConfig:
    maxPods: 100
`
const perfprofOne = `apiVersion: performance.openshift.io/v2
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
const perfprofOneStatus = `
conditions:
  - type: Available
    status: "True"
    reason: ""
    message: ""
`

const tuned1 = `
apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  name: tuned-1
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - data: |
      [main]
      summary=Custom OpenShift profile
      include=openshift-node

      [sysctl]
      vm.dirty_ratio="55"
    name: tuned-1-profile
  recommend:
  - match:
    - label: tuned-1-node-label
    priority: 20
    profile: tuned-1-profile
`

func TestControlPlaneClientImpl_Get(t *testing.T) {
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
						hypershiftconsts.ConfigKey: machineConfig1,
					},
					BinaryData: nil,
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-config-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						hypershiftconsts.ConfigKey: coreMachineConfig1,
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
						hypershiftconsts.TuningKey: perfprofOne,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      GetStatusConfigMapName("configmap-performance-profile-1"),
						Namespace: namespace,
					},
					Data: map[string]string{
						hypershiftconsts.PerformanceProfileStatusKey: perfprofOneStatus,
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
						hypershiftconsts.ConfigKey: machineConfig1,
					},
					BinaryData: nil,
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-config-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						hypershiftconsts.ConfigKey: coreMachineConfig1,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubelet-config-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						hypershiftconsts.ConfigKey: kubeletConfig1,
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
						hypershiftconsts.ConfigKey: machineConfig1,
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

func TestControlPlaneClientImpl_List(t *testing.T) {
	if err := performancev2.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}
	if err := machineconfigv1.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}

	namespace := "test"
	testsCases := []struct {
		name           string
		ObjectList     client.ObjectList
		configMaps     []runtime.Object
		getterFunction func(client.ObjectList) ([]client.Object, []client.Object, error)
	}{
		{
			name:       "list performanceprofile objects",
			ObjectList: &performancev2.PerformanceProfileList{},
			configMaps: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "config-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						hypershiftconsts.TuningKey: perfprofOne,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      GetStatusConfigMapName("config-1"),
						Namespace: namespace,
					},
					Data: map[string]string{
						hypershiftconsts.PerformanceProfileStatusKey: perfprofOneStatus,
					},
				},
			},
			getterFunction: func(list client.ObjectList) ([]client.Object, []client.Object, error) {
				var got, want []client.Object
				toDecode := []string{perfprofOne}
				ppList, ok := list.(*performancev2.PerformanceProfileList)
				if !ok {
					return got, want, fmt.Errorf("expected *performancev2.PerformanceProfileList, got: %T", list)
				}
				for i := range ppList.Items {
					got = append(got, &ppList.Items[i])
				}
				for _, s := range toDecode {
					ppWant := &performancev2.PerformanceProfile{}
					_, err := DecodeManifest([]byte(s), scheme.Scheme, ppWant)
					if err != nil {
						return got, want, err
					}

					ppStatus := &performancev2.PerformanceProfileStatus{}
					if err := yaml.Unmarshal([]byte(perfprofOneStatus), ppStatus); err != nil {
						return got, want, err
					}
					ppWant.Status = *ppStatus
					want = append(want, ppWant)
				}
				return got, want, nil
			},
		},
		{
			name:       "list multiple machineconfig",
			ObjectList: &machineconfigv1.MachineConfigList{},
			configMaps: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "config-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						hypershiftconsts.ConfigKey: machineConfig1,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "config-2",
						Namespace: namespace,
					},
					Data: map[string]string{
						hypershiftconsts.ConfigKey: coreMachineConfig1,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "config-3",
						Namespace: namespace,
					},
					Data: map[string]string{
						hypershiftconsts.ConfigKey: kubeletConfig1,
					},
				},
			},
			getterFunction: func(list client.ObjectList) ([]client.Object, []client.Object, error) {
				var got, want []client.Object
				toDecode := []string{machineConfig1, coreMachineConfig1}
				mcList, ok := list.(*machineconfigv1.MachineConfigList)
				if !ok {
					return got, want, fmt.Errorf("expected *machineconfigv1.MachineConfigList, got: %T", list)
				}
				for i := range mcList.Items {
					got = append(got, &mcList.Items[i])
				}
				for _, s := range toDecode {
					mcWant := &machineconfigv1.MachineConfig{}
					_, err := DecodeManifest([]byte(s), scheme.Scheme, mcWant)
					if err != nil {
						return got, want, err
					}
					want = append(want, mcWant)
				}
				return got, want, nil
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tc.configMaps...).Build()
			c := NewControlPlaneClient(fakeClient, namespace)
			err := c.List(context.TODO(), tc.ObjectList)
			if err != nil {
				t.Fatal(err)
			}
			got, want, err := tc.getterFunction(tc.ObjectList)
			if err != nil {
				t.Errorf("getterFunction() error = %v, wantErr %v", err, tc.ObjectList)
			}
			if diff := cmp.Diff(got, want); diff != "" {
				t.Errorf("actual object differs from expected: %s", diff)
			}
		})
	}
}

func TestControlPlaneClientImpl_Delete(t *testing.T) {
	if err := performancev2.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}
	if err := machineconfigv1.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}
	if err := tunedv1.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}
	namespace := "test"
	testsCases := []struct {
		name                     string
		encapsulatedObjsToDelete []client.Object
		configMaps               []runtime.Object
	}{
		{
			name: "delete config map that holds machineconfig object",
			encapsulatedObjsToDelete: []client.Object{
				&machineconfigv1.MachineConfig{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name: "config-1",
					},
				},
			},
			configMaps: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "config-1-configmap",
						Namespace: HostedClustersNamespaceName,
					},
					Data: map[string]string{
						hypershiftconsts.ConfigKey: machineConfig1,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-config-1-configmap",
						Namespace: HostedClustersNamespaceName,
					},
					Data: map[string]string{
						hypershiftconsts.ConfigKey: coreMachineConfig1,
					},
				},
			},
		},
		{
			name: "delete config map that holds performanceProfile object",
			encapsulatedObjsToDelete: []client.Object{
				&performancev2.PerformanceProfile{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name: "perfprofOne",
					},
				},
			},
			configMaps: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "perfprofOne-configmap",
						Namespace: HostedClustersNamespaceName,
					},
					Data: map[string]string{
						hypershiftconsts.TuningKey: perfprofOne,
					},
				},
			},
		},
		{
			name: "delete config map that holds tuned object even when ns name is wrong",
			encapsulatedObjsToDelete: []client.Object{
				&performancev2.PerformanceProfile{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tuned-1",
						Namespace: namespace,
					},
				},
			},
			configMaps: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tuned-1-configmap",
						Namespace: HostedClustersNamespaceName,
					},
					Data: map[string]string{
						hypershiftconsts.TuningKey: tuned1,
					},
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tc.configMaps...).Build()
			c := NewControlPlaneClient(fakeClient, namespace)
			for _, obj := range tc.encapsulatedObjsToDelete {
				err := c.Delete(context.TODO(), obj)
				if err != nil {
					t.Errorf("failed to delete encapsulated object %s: %v", obj.GetName(), err)
				}
			}
		})
	}
}

func TestDecodeManifest(t *testing.T) {
	testCases := []struct {
		name     string
		manifest []byte
		into     runtime.Object
		isLoaded bool
	}{
		{
			name:     "decode kubelet config into machine config",
			manifest: []byte(kubeletConfig1),
			into:     &machineconfigv1.MachineConfig{},
			isLoaded: false,
		},
		{
			name:     "decode machine config into tuned config",
			manifest: []byte(machineConfig1),
			into:     &machineconfigv1.MachineConfig{},
			isLoaded: true,
		},
		{
			name:     "decode performance profile into performance profile",
			manifest: []byte(perfprofOne),
			into:     &performancev2.PerformanceProfile{},
			isLoaded: true,
		},
		{
			name:     "decode performance profile into tuned",
			manifest: []byte(perfprofOne),
			into:     &tunedv1.Tuned{},
			isLoaded: false,
		},
	}

	if err := performancev2.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}
	if err := machineconfigv1.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}
	if err := tunedv1.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := DecodeManifest(tc.manifest, scheme.Scheme, tc.into)
			if err != nil {
				t.Errorf("failed to decode manifest into %T: %v", tc.into, err)
			}
			if !ok && tc.isLoaded {
				t.Errorf("expected into of type %T to be loaded", tc.into)
			}
		})
	}
}
