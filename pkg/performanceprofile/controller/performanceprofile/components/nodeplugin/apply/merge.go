package apply

import (
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// MergeMetadataForUpdate merges the read-only fields of metadata.
// This is to be able to do a meaningful comparison in apply,
// since objects created on runtime do not have these fields populated.
func MergeMetadataForUpdate(current, updated *uns.Unstructured) error {
	mergeAnnotations(current, updated)
	mergeLabels(current, updated)
	updated.SetResourceVersion(current.GetResourceVersion())

	return nil
}

// MergeObjectForUpdate prepares a "desired" object to be updated.
// Some objects, such as ServiceAccount require
// some semantic-aware updates
func MergeObjectForUpdate(current, updated *uns.Unstructured) error {
	if err := MergeDaemonSetForUpdate(current, updated); err != nil {
		return err
	}

	if err := MergeServiceAccountForUpdate(current, updated); err != nil {
		return err
	}

	// For all object types, merge metadata.
	// Run this last, in case any of the more specific merge logic has
	// changed "updated"
	if err := MergeMetadataForUpdate(current, updated); err != nil {
		return err
	}

	return nil
}

// MergeServiceAccountForUpdate copies secrets from current to updated.
// This is intended to preserve the auto-generated token.
// Right now, we just copy current to updated and don't support supplying
// any secrets ourselves.
func MergeServiceAccountForUpdate(current, updated *uns.Unstructured) error {
	gvk := updated.GroupVersionKind()
	if gvk.Group == "" && gvk.Kind == "ServiceAccount" {
		curSecrets, found, err := uns.NestedSlice(current.Object, "secrets")
		if err != nil {
			return err
		}

		if found {
			if err := uns.SetNestedField(updated.Object, curSecrets, "secrets"); err != nil {
				return err
			}
		}

		curImagePullSecrets, found, err := uns.NestedSlice(current.Object, "imagePullSecrets")
		if err != nil {
			return err
		}
		if found {
			if err := uns.SetNestedField(updated.Object, curImagePullSecrets, "imagePullSecrets"); err != nil {
				return err
			}
		}
	}
	return nil
}

// MergeDaemonSetForUpdate MergeDaemonSet copies status from current to updated.
// The API server should fill the status field,
// so it's semantically wrong to compare it.
func MergeDaemonSetForUpdate(current, updated *uns.Unstructured) error {
	gvk := updated.GetObjectKind().GroupVersionKind()
	if gvk.Group == "apps" && gvk.Kind == "DaemonSet" {
		status, found, err := uns.NestedMap(current.Object, "status")
		if err != nil {
			return err
		}
		if found {
			if err := uns.SetNestedMap(updated.Object, status, "status"); err != nil {
				return err
			}
		}
	}
	return nil
}

// mergeAnnotations copies over any annotations from current to updated,
// with updated winning if there's a conflict
func mergeAnnotations(current, updated *uns.Unstructured) {
	updatedAnnotations := updated.GetAnnotations()
	curAnnotations := current.GetAnnotations()

	if curAnnotations == nil {
		curAnnotations = map[string]string{}
	}

	for k, v := range updatedAnnotations {
		curAnnotations[k] = v
	}
	if len(curAnnotations) > 0 {
		updated.SetAnnotations(curAnnotations)
	}
}

// mergeLabels copies over any labels from current to updated,
// with updated winning if there's a conflict
func mergeLabels(current, updated *uns.Unstructured) {
	updatedLabels := updated.GetLabels()
	curLabels := current.GetLabels()

	if curLabels == nil {
		curLabels = map[string]string{}
	}

	for k, v := range updatedLabels {
		curLabels[k] = v
	}
	if len(curLabels) > 0 {
		updated.SetLabels(curLabels)
	}
}
