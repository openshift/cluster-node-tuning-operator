package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	ntoutil "github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

const (
	defaultWorkerProfile = "openshift-node"
)

const (
	verifyCommandAnnotation = "verificationCommand"
	verifyOutputAnnotation  = "verificationOutput"

	pollInterval = 5 * time.Second
	waitDuration = 5 * time.Minute
	// The number of Profile status conditions.  Adjust when adding new conditions in the API.
	ProfileStatusConditions = 2

	tunedSHMMNI    = "../testing_manifests/deferred/tuned-basic-00.yaml"
	tunedCPUEnergy = "../testing_manifests/deferred/tuned-basic-10.yaml"
	tunedVMLatency = "../testing_manifests/deferred/tuned-basic-20.yaml"

	tunedVMLatInplaceBootstrap = "../testing_manifests/deferred/tuned-basic-updates-00.yaml"
	tunedVMLatInplaceUpdate    = "../testing_manifests/deferred/tuned-basic-updates-10.yaml"

	tunedMatchLabelLater = "match-later"
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

func getRecommendedProfile(pod *corev1.Pod) (string, error) {
	out, err := util.ExecCmdInPod(pod, "/bin/cat", "/etc/tuned/recommend.d/50-openshift.conf")
	if err != nil {
		return "", err
	}
	recommended := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(out), "["), "]")
	util.Logf("getRecommendedProfile(): read %q from pod %s/%s on %q", recommended, pod.Namespace, pod.Name, pod.Spec.NodeName)
	return recommended, nil
}

func verify(pod *corev1.Pod, verifications map[string]verification) error {
	for _, verif := range verifications {
		out, err := util.ExecCmdInPod(pod, verif.command...)
		if err != nil {
			// not available, which is actually a valid state. Let's record it.
			out = err.Error()
		} else {
			out = strings.TrimSpace(out)
		}
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

func setDeferred(obj *tunedv1.Tuned, mode ntoutil.DeferMode) *tunedv1.Tuned {
	if obj == nil {
		return obj
	}
	if obj.Annotations == nil {
		obj.Annotations = make(map[string]string)
	}
	obj.Annotations = ntoutil.SetDeferredUpdateAnnotation(obj.Annotations, mode)
	return obj
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
	klog.Infof("expected profile: %q", expectedProfile)
	if cond.Status != corev1.ConditionFalse {
		return fmt.Errorf("applied is true")
	}
	if !strings.Contains(cond.Message, "waiting for the next node restart") {
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

func checkAppliedConditionStaysOKForNode(ctx context.Context, nodeName, expectedProfile string) {
	ginkgo.GinkgoHelper()

	gomega.Consistently(func() error {
		curProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		ginkgo.By(fmt.Sprintf("checking conditions for reference profile %q: %#v", curProf.Name, curProf.Status.Conditions))
		if len(curProf.Status.Conditions) == 0 {
			return fmt.Errorf("missing status conditions")
		}

		cond := findCondition(curProf.Status.Conditions, tunedv1.TunedProfileApplied)
		if cond == nil {
			return fmt.Errorf("missing status applied condition")
		}
		err = checkAppliedConditionOK(cond)
		if err != nil {
			util.Logf("profile for target node %q does not match expectations about %q: %v", curProf.Name, expectedProfile, err)
		}
		return err
	}).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())
}

func checkNonTargetWorkerNodesAreUnaffected(ctx context.Context, workerNodes []corev1.Node, targetNodeName string) {
	ginkgo.GinkgoHelper()

	// all the other nodes should be fine as well. For them the state should be settled now, so we just check once
	// why: because of bugs, we had once allegedly unaffected nodes stuck in deferred updates (obvious bug), let's
	// be prudent and add a check this never happens again. Has sky fell yet?
	for _, workerNode := range workerNodes {
		if workerNode.Name == targetNodeName {
			continue // checked previously, if we got this far it's OK
		}

		ginkgo.By(fmt.Sprintf("checking non-target worker node after rollback: %q", workerNode.Name))

		gomega.Eventually(func() error {
			wrkNodeProf, err := cs.Profiles(ntoconfig.WatchNamespace()).Get(ctx, workerNode.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}

			ginkgo.By(fmt.Sprintf("checking profile for target node %q matches expectations about %q", wrkNodeProf.Name, defaultWorkerProfile))
			if wrkNodeProf.Spec.Config.TunedProfile != defaultWorkerProfile {
				return fmt.Errorf("checking profile for worker node %q matches expectations %q", wrkNodeProf.Name, defaultWorkerProfile)
			}

			ginkgo.By(fmt.Sprintf("checking condition for target node %q matches expectations about %q", wrkNodeProf.Name, defaultWorkerProfile))
			if len(wrkNodeProf.Status.Conditions) == 0 {
				return fmt.Errorf("missing status conditions")
			}
			cond := findCondition(wrkNodeProf.Status.Conditions, tunedv1.TunedProfileApplied)
			if cond == nil {
				return fmt.Errorf("missing status applied condition")
			}
			return checkAppliedConditionOK(cond)
		}).WithPolling(10. * time.Second).WithTimeout(1 * time.Minute).Should(gomega.Succeed())
	}
}
