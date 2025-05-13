package serialize

import (
	"encoding/json"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/profilecreator/toleration"
)

const (
	hardwareTuningMessage = `# HardwareTuning is an advanced feature, and only intended to be used if
# user is aware of the vendor recommendation on maximum cpu frequency.
# The structure must follow
#
# hardwareTuning:
#   isolatedCpuFreq: <Maximum frequency for applications running on isolated cpus>
#   reservedCpuFreq: <Maximum frequency for platform software running on reserved cpus>
`

	differentCoreIDsMessage = `# PPC tolerates having different core IDs for the same logical processors on
# the same NUMA cell compared with other nodes that belong to the stated pool.
# While core IDs numbering may differ between two systems, it still can be considered
# that NUMA and HW topologies are similar; However this depends on the combination
# setting of the hardware, software and firmware as that may affect the mapping pattern.
# While the performance profile controller depends on the logical processors per NUMA,
# having different IDs may affect your system's performance optimization where the cores
# location matters, thus use the generated profile with caution.
`
)

func Profile(obj runtime.Object, tols toleration.Set) (string, error) {
	// write CSV to out dir
	writer := strings.Builder{}
	if err := marshallObject(obj, &writer); err != nil {
		return "", err
	}

	if tols[toleration.EnableHardwareTuning] {
		if _, err := writer.Write([]byte(hardwareTuningMessage)); err != nil {
			return "", err
		}
	}

	if tols[toleration.DifferentCoreIDs] {
		if _, err := writer.Write([]byte(differentCoreIDsMessage)); err != nil {
			return "", err
		}
	}

	return writer.String(), nil
}

// MarshallObject marshals an object, usually a CSV into YAML
func marshallObject(obj interface{}, writer io.Writer) error {
	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		return err
	}

	var r unstructured.Unstructured
	if err := json.Unmarshal(jsonBytes, &r.Object); err != nil {
		return err
	}

	// remove status and metadata.creationTimestamp
	unstructured.RemoveNestedField(r.Object, "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(r.Object, "template", "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(r.Object, "spec", "template", "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(r.Object, "status")

	deployments, exists, err := unstructured.NestedSlice(r.Object, "spec", "install", "spec", "deployments")
	if err != nil {
		return err
	}
	if exists {
		for _, obj := range deployments {
			deployment := obj.(map[string]interface{})
			unstructured.RemoveNestedField(deployment, "metadata", "creationTimestamp")
			unstructured.RemoveNestedField(deployment, "spec", "template", "metadata", "creationTimestamp")
			unstructured.RemoveNestedField(deployment, "status")
		}
		if err := unstructured.SetNestedSlice(r.Object, deployments, "spec", "install", "spec", "deployments"); err != nil {
			return err
		}
	}

	jsonBytes, err = json.Marshal(r.Object)
	if err != nil {
		return err
	}

	yamlBytes, err := yaml.JSONToYAML(jsonBytes)
	if err != nil {
		return err
	}

	// fix double quoted strings by removing unneeded single quotes...
	s := string(yamlBytes)
	s = strings.Replace(s, " '\"", " \"", -1)
	s = strings.Replace(s, "\"'\n", "\"\n", -1)

	yamlBytes = []byte(s)

	_, err = writer.Write([]byte("---\n"))
	if err != nil {
		return err
	}

	_, err = writer.Write(yamlBytes)
	if err != nil {
		return err
	}

	return nil
}
