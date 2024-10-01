package components

import (
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	hypershiftconsts "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/hypershift/consts"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

func TestEncapsulateObjInConfigMap(t *testing.T) {
	tests := []struct {
		name            string
		profileName     string
		ppConfigMap     *corev1.ConfigMap
		encapsulatedObj client.Object
		dataKey         string
		objectLabels    map[string]string
	}{
		{
			name:        "MachineConfigObject",
			profileName: "test-1",
			ppConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pp-test-1",
					Namespace: "test-ns",
					Annotations: map[string]string{
						hypershiftconsts.NodePoolNameLabel: "np-test-1",
					},
				},
			},
			encapsulatedObj: &machineconfigv1.MachineConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mc-test-1",
					Namespace: "test-ns",
					Annotations: map[string]string{
						hypershiftconsts.NodePoolNameLabel: "np-test-1",
					},
				},
			},
			dataKey: hypershiftconsts.ConfigKey,
			objectLabels: map[string]string{
				hypershiftconsts.NTOGeneratedMachineConfigLabel: "true",
			},
		},
		{
			name:        "KubeletConfigObject",
			profileName: "test-1",
			ppConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pp-test-1",
					Namespace: "test-ns",
					Annotations: map[string]string{
						hypershiftconsts.NodePoolNameLabel: "np-test-1",
					},
				},
			},
			encapsulatedObj: &machineconfigv1.KubeletConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kc-test-1",
					Namespace: "test-ns",
				},
			},
			dataKey: hypershiftconsts.ConfigKey,
			objectLabels: map[string]string{
				hypershiftconsts.NTOGeneratedMachineConfigLabel: "true",
				hypershiftconsts.KubeletConfigConfigMapLabel:    "",
			},
		},
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	for _, tt := range tests {
		t.Run(fmt.Sprintf("Encapsulation of %s", tt.name), func(t *testing.T) {
			configMap, err := EncapsulateObjInConfigMap(scheme, tt.ppConfigMap, tt.encapsulatedObj, tt.profileName, tt.dataKey, tt.objectLabels)
			if err != nil {
				t.Errorf("EncapsulateObjInConfigMap() error = %v", err)
			}
			for k := range tt.objectLabels {
				if _, ok := configMap.Labels[k]; !ok {
					t.Errorf("EncapsulateObjInConfigMap() missing label %s", k)
				}
			}
		})
	}
}
