package operator

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func utilObjectInfo(o interface{}) string {
	object := o.(metav1.Object)
	s := fmt.Sprintf("%T, ", o)
	if namespace := object.GetNamespace(); namespace != "" {
		s += fmt.Sprintf("Namespace=%s, ", namespace)
	}
	s += fmt.Sprintf("Name=%s", object.GetName())
	return s
}
