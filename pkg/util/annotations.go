package util

import (
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
)

const (
	AnnotationValueAny = "*" // "" is treated as the same
)

func HasAnnotation(anns map[string]string, key string, values ...string) bool {
	if anns == nil {
		return false
	}
	annVals, ok := anns[key]
	if !ok {
		return false
	}
	if len(annVals) == 0 || len(values) == 0 {
		// annotation value == "", treated as wildcard like AnnotationValueAny. But faster.
		// if nothing to check, we only need to check for the ann presence, and if
		// we got this far, we have it.
		return true
	}
	annSet := sets.New[string](parseValuesAnnotation(annVals)...)
	return annSet.Has(AnnotationValueAny) || annSet.HasAll(values...)
}

func AddAnnotation(anns map[string]string, key, val string) map[string]string {
	ret := cloneMapStringString(anns)
	ret[key] = val
	return ret
}

func DelAnnotation(anns map[string]string, key string) map[string]string {
	ret := cloneMapStringString(anns)
	delete(ret, key)
	return ret
}

func ToggleDeferredUpdateAnnotation(anns map[string]string, toggle bool) map[string]string {
	if toggle {
		return AddAnnotation(anns, tunedv1.TunedDeferredUpdate, "")
	}
	return DelAnnotation(anns, tunedv1.TunedDeferredUpdate)
}

func HasDeferredUpdateAnnotation(anns map[string]string, values ...string) bool {
	return HasAnnotation(anns, tunedv1.TunedDeferredUpdate, values...)
}

func parseValuesAnnotation(s string) []string {
	items := strings.Split(s, ",")
	ret := make([]string, 0, len(items))
	for _, item := range items {
		ret = append(ret, strings.TrimSpace(item))
	}
	return ret
}

func cloneMapStringString(obj map[string]string) map[string]string {
	ret := make(map[string]string, len(obj))
	for key, val := range obj {
		ret[key] = val
	}
	return ret
}
