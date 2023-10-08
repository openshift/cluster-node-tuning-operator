package util

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ObjectInfo(o interface{}) string {
	if o == nil {
		return "nil"
	}

	object := o.(metav1.Object)
	s := fmt.Sprintf("%T, ", o)
	if namespace := object.GetNamespace(); namespace != "" {
		s += fmt.Sprintf("Namespace=%s, ", namespace)
	}
	s += fmt.Sprintf("Name=%s", object.GetName())
	return s
}
