package util

import (
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
)

// DeferMode controls the deferred feature. Default is "never", which is the value
// assumed when no annotation is set, and which cause all objects to be processed
// immediately (never deferred)
type DeferMode string

const (
	// DeferNever means deferred mode is disabled; treat as immediate update, like the annotation is not present.
	DeferNever DeferMode = "never"
	// DeferAlways means the changes carried will be deferred until the next node restart: both for the first application and for profile changes.
	DeferAlways DeferMode = "always"
	// DeferUpdate means in-place updates/changes to only the contents of the currently used profile. Switches to different TuneD profiles are processed immediately without the need to reboot.
	DeferUpdate DeferMode = "update"
)

func (dm DeferMode) String() string {
	return string(dm)
}

func IsImmediateUpdate(value DeferMode) bool {
	return value == DeferNever
}

func IsDeferredUpdate(value DeferMode) bool {
	return value == DeferAlways || value == DeferUpdate
}

func GetDeferredUpdateAnnotation(anns map[string]string) DeferMode {
	if anns == nil {
		return DeferNever
	}
	val, ok := anns[tunedv1.TunedDeferredUpdate]
	if !ok {
		return DeferNever
	}
	value := DeferMode(val)
	if !IsDeferredUpdate(value) {
		return DeferNever
	}
	return value
}

func SetDeferredUpdateAnnotation(anns map[string]string, value DeferMode) map[string]string {
	ret := cloneMapStringString(anns)
	if value == DeferNever {
		return ret
	}
	ret[tunedv1.TunedDeferredUpdate] = string(value)
	return ret
}

func DeleteDeferredUpdateAnnotation(anns map[string]string) map[string]string {
	ret := cloneMapStringString(anns)
	delete(ret, tunedv1.TunedDeferredUpdate)
	return ret
}

func cloneMapStringString(obj map[string]string) map[string]string {
	ret := make(map[string]string, len(obj))
	for key, val := range obj {
		ret[key] = val
	}
	return ret
}
