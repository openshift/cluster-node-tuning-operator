package util

import (
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
)

func HasDeferredUpdateAnnotation(anns map[string]string) bool {
	if anns == nil {
		return false
	}
	_, ok := anns[tunedv1.TunedDeferredUpdate]
	return ok
}

func SetDeferredUpdateAnnotation(anns map[string]string, tuned *tunedv1.Tuned) map[string]string {
	if anns == nil {
		anns = make(map[string]string)
	}
	return ToggleDeferredUpdateAnnotation(anns, HasDeferredUpdateAnnotation(tuned.Annotations))
}

func ToggleDeferredUpdateAnnotation(anns map[string]string, toggle bool) map[string]string {
	ret := cloneMapStringString(anns)
	if toggle {
		ret[tunedv1.TunedDeferredUpdate] = ""
	} else {
		delete(ret, tunedv1.TunedDeferredUpdate)
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
