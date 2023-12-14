package node

import (
	apiconfigv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NewNodeConfig(mode apiconfigv1.CgroupMode) *apiconfigv1.Node {
	return &apiconfigv1.Node{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apiconfigv1.SchemeGroupVersion.String(),
			Kind:       "Node",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: apiconfigv1.NodeSpec{
			CgroupMode: mode,
		},
	}
}
