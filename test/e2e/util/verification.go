package util

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

const (
	VerificationCommandAnnotation = "verificationCommand"
	VerificationOutputAnnotation  = "verificationOutput"
)

type VerificationData struct {
	OutputCurrent  string
	OutputExpected string
	CommandArgs    []string
	TargetTunedPod *corev1.Pod
}

func MustExtractVerificationOutputAndCommand(cs *framework.ClientSet, targetNode *corev1.Node, tuned *tunedv1.Tuned) VerificationData {
	ginkgo.GinkgoHelper()

	verificationCommand, ok := tuned.Annotations[VerificationCommandAnnotation]
	gomega.Expect(ok).To(gomega.BeTrue(), "missing verification command annotation %s", VerificationCommandAnnotation)

	verificationCommandArgs := []string{}
	err := json.Unmarshal([]byte(verificationCommand), &verificationCommandArgs)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(verificationCommandArgs).ToNot(gomega.BeEmpty(), "missing verification command args")
	ginkgo.By(fmt.Sprintf("verification command: %v", verificationCommandArgs))

	verificationOutputExpected, ok := tuned.Annotations[VerificationOutputAnnotation]
	gomega.Expect(ok).To(gomega.BeTrue(), "missing verification output annotation %s", VerificationOutputAnnotation)
	ginkgo.By(fmt.Sprintf("verification expected output: %q", verificationOutputExpected))

	TargetTunedPod, err := GetTunedForNode(cs, targetNode)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(TargetTunedPod.Status.Phase).To(gomega.Equal(corev1.PodRunning))

	// gather the output now before the profile is applied so we can check nothing changed
	verificationOutputCurrent, err := ExecCmdInPod(TargetTunedPod, verificationCommandArgs...)
	if err != nil {
		// not available, which is actually a valid state. Let's record it.
		verificationOutputCurrent = err.Error()
	} else {
		verificationOutputCurrent = strings.TrimSpace(verificationOutputCurrent)
	}
	ginkgo.By(fmt.Sprintf("verification current output: %q", verificationOutputCurrent))

	return VerificationData{
		OutputCurrent:  verificationOutputCurrent,
		OutputExpected: verificationOutputExpected,
		CommandArgs:    verificationCommandArgs,
		TargetTunedPod: TargetTunedPod,
	}
}
