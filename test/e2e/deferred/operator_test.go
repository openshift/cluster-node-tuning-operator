package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/manifests"
	ntoutil "github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

var (
	cs = framework.NewClientSet()
)

func TestNodeTuningOperatorDeferred(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Node Tuning Operator e2e tests: deferred")
}

type verification struct {
	command []string
	output  string
}

func extractVerifications(tuneds ...*tunedv1.Tuned) map[string]verification {
	ret := make(map[string]verification)
	for _, tuned := range tuneds {
		verificationOutput, ok := tuned.Annotations[verifyOutputAnnotation]
		if !ok {
			util.Logf("tuned %q has no verification output annotation", tuned.Name)
			continue
		}

		verificationCommand, ok := tuned.Annotations[verifyCommandAnnotation]
		if !ok {
			util.Logf("tuned %q has no verification command annotation", tuned.Name)
			continue
		}

		verificationCommandArgs := []string{}
		err := json.Unmarshal([]byte(verificationCommand), &verificationCommandArgs)
		if err != nil {
			util.Logf("cannot unmarshal verification command for tuned %q", tuned.Name)
			continue
		}
		util.Logf("tuned %q verification command: %v", tuned.Name, verificationCommandArgs)

		ret[tuned.Name] = verification{
			command: verificationCommandArgs,
			output:  verificationOutput,
		}
	}
	return ret
}

func verify(pod *corev1.Pod, verifications map[string]verification) error {
	for _, verif := range verifications {
		out, err := util.ExecCmdInPod(pod, verif.command...)
		if err != nil {
			return err
		}
		out = strings.TrimSpace(out)
		if out != verif.output {
			return fmt.Errorf("got: %s; expected: %s", out, verif.output)
		}
	}
	return nil
}

func popleft(strs []string) (string, []string, bool) {
	if len(strs) < 1 {
		return "", strs, false
	}
	return strs[0], strs[1:], true
}

func prepend(strs []string, s string) []string {
	return append([]string{s}, strs...)
}

func setDeferred(obj *tunedv1.Tuned) *tunedv1.Tuned {
	if obj == nil {
		return obj
	}
	if obj.Annotations == nil {
		obj.Annotations = make(map[string]string)
	}
	obj.Annotations = ntoutil.ToggleDeferredUpdateAnnotation(obj.Annotations, true)
	return obj
}

func loadTuned(path string) (*tunedv1.Tuned, error) {
	src, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return manifests.NewTuned(src)
}

func getCurrentDirPath() (string, error) {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot retrieve tests directory")
	}
	return filepath.Dir(file), nil
}

func findCondition(conditions []tunedv1.ProfileStatusCondition, conditionType tunedv1.ProfileConditionType) *tunedv1.ProfileStatusCondition {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return &condition
		}
	}
	return nil
}

func checkAppliedConditionDeferred(cond *tunedv1.ProfileStatusCondition, expectedProfile string) error {
	if cond.Status != corev1.ConditionFalse {
		return fmt.Errorf("applied is true")
	}
	if !strings.Contains(cond.Message, "waiting for the next daemon restart: "+expectedProfile) {
		return fmt.Errorf("unexpected message %q", cond.Message)
	}
	return nil
}

func checkAppliedConditionOK(cond *tunedv1.ProfileStatusCondition) error {
	if cond.Status != corev1.ConditionTrue {
		return fmt.Errorf("applied is false")
	}
	if !strings.Contains(cond.Reason, "AsExpected") {
		return fmt.Errorf("unexpected reason %q", cond.Reason)
	}
	if !strings.Contains(cond.Message, "TuneD profile applied.") {
		return fmt.Errorf("unexpected message %q", cond.Message)
	}
	return nil
}
