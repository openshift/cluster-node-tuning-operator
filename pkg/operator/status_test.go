// Assisted-by: Claude Code IDE; model: claude-4.5-sonnet

package operator

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoclient "github.com/openshift/cluster-node-tuning-operator/pkg/client"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	testDaemonSetName = "tuned"
	testNamespace     = "openshift-cluster-node-tuning-operator"
	testReleaseVer    = "4.20.0"
)

// makeFakeManifest returns a test DaemonSet manifest as bytes
func makeFakeManifest() []byte {
	return []byte(`
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: tuned
  namespace: openshift-cluster-node-tuning-operator
  labels:
    openshift-app: tuned
  annotations:
    tuned.openshift.io/stable-generation: "1"
spec:
  selector:
    matchLabels:
      openshift-app: tuned
  updateStrategy:
    rollingUpdate:
      maxUnavailable: 10%
    type: RollingUpdate
  template:
    metadata:
      labels:
        openshift-app: tuned
        name: tuned
    spec:
      serviceAccountName: tuned
      containers:
      - name: tuned
        image: ${CLUSTER_NODE_TUNED_IMAGE}
        imagePullPolicy: IfNotPresent
        command: ["/usr/bin/cluster-node-tuning-operator","ocp-tuned","--in-cluster"]
        env:
        - name: WATCH_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        - name: OCP_NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: RELEASE_VERSION
          value: ${RELEASE_VERSION}
        - name: CLUSTER_NODE_TUNED_IMAGE
          value: ${CLUSTER_NODE_TUNED_IMAGE}
      hostNetwork: true
      hostPID: true
      nodeSelector:
        kubernetes.io/os: linux
      priorityClassName: "system-node-critical"
      tolerations:
      - operator: Exists
`)
}

// Test case structure
type testCase struct {
	name                   string
	tuned                  *tunedv1.Tuned
	daemonSet              *appsv1.DaemonSet
	profiles               []*tunedv1.Profile
	bootcmdlineConflict    map[string]bool
	mcLabelsAcrossMCP      map[string]bool
	oldConditions          []configv1.ClusterOperatorStatusCondition
	expectedConditions     []configv1.ClusterOperatorStatusCondition
	expectedOperandVersion string
	expectErr              bool
}

// Tuned CR modifiers
type tunedModifier func(*tunedv1.Tuned) *tunedv1.Tuned

func makeFakeTuned(modifiers ...tunedModifier) *tunedv1.Tuned {
	tuned := &tunedv1.Tuned{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: testNamespace,
		},
		Spec: tunedv1.TunedSpec{
			ManagementState: operatorv1.Managed,
		},
	}
	for _, modifier := range modifiers {
		tuned = modifier(tuned)
	}
	return tuned
}

func withManagementState(state operatorv1.ManagementState) tunedModifier {
	return func(t *tunedv1.Tuned) *tunedv1.Tuned {
		t.Spec.ManagementState = state
		return t
	}
}

type daemonSetModifier func(*appsv1.DaemonSet) *appsv1.DaemonSet

// getDaemonSet creates a DaemonSet from the manifest and applies modifiers
func getDaemonSet(modifiers ...daemonSetModifier) *appsv1.DaemonSet {
	manifest := makeFakeManifest()

	// Decode the manifest
	ds := &appsv1.DaemonSet{}
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	if err := decoder.Decode(ds); err != nil {
		panic(fmt.Sprintf("failed to decode DaemonSet manifest: %v", err))
	}

	// Replace placeholders in the manifest
	for i := range ds.Spec.Template.Spec.Containers {
		container := &ds.Spec.Template.Spec.Containers[i]
		if strings.Contains(container.Image, "${CLUSTER_NODE_TUNED_IMAGE}") {
			container.Image = "quay.io/openshift/origin-cluster-node-tuning-operator:latest"
		}
		for j := range container.Env {
			env := &container.Env[j]
			if env.Name == "RELEASE_VERSION" && strings.Contains(env.Value, "${RELEASE_VERSION}") {
				env.Value = testReleaseVer
			}
			if env.Name == "CLUSTER_NODE_TUNED_IMAGE" && strings.Contains(env.Value, "${CLUSTER_NODE_TUNED_IMAGE}") {
				env.Value = "quay.io/openshift/origin-cluster-node-tuning-operator:latest"
			}
		}
	}

	// Set default status values for testing
	ds.Generation = 1
	ds.Status = appsv1.DaemonSetStatus{
		CurrentNumberScheduled: 1,
		DesiredNumberScheduled: 1,
		NumberAvailable:        1,
		NumberReady:            1,
		NumberUnavailable:      0,
		ObservedGeneration:     1,
		UpdatedNumberScheduled: 1,
	}

	// Apply modifiers
	for _, modifier := range modifiers {
		ds = modifier(ds)
	}

	return ds
}

func withDaemonSetStatus(numberReady, updatedNumber, numberAvailable, numberUnavailable int32) daemonSetModifier {
	return func(instance *appsv1.DaemonSet) *appsv1.DaemonSet {
		instance.Status.NumberReady = numberReady
		instance.Status.NumberAvailable = numberAvailable
		instance.Status.UpdatedNumberScheduled = updatedNumber
		instance.Status.NumberUnavailable = numberUnavailable
		return instance
	}
}

func withDaemonSetGeneration(generations ...int64) daemonSetModifier {
	return func(instance *appsv1.DaemonSet) *appsv1.DaemonSet {
		instance.Generation = generations[0]
		if len(generations) > 1 {
			instance.Status.ObservedGeneration = generations[1]
		}
		return instance
	}
}

func withDaemonSetAnnotation(annotation string, value string) daemonSetModifier {
	return func(instance *appsv1.DaemonSet) *appsv1.DaemonSet {
		if instance.Annotations == nil {
			instance.Annotations = map[string]string{}
		}
		instance.Annotations[annotation] = value
		return instance
	}
}

// Tuned-operator specific modifiers
func withDesiredNumberScheduled(desired int32) daemonSetModifier {
	return func(instance *appsv1.DaemonSet) *appsv1.DaemonSet {
		instance.Status.DesiredNumberScheduled = desired
		instance.Status.CurrentNumberScheduled = instance.Status.NumberAvailable
		return instance
	}
}

// Profile modifiers
type profileModifier func(*tunedv1.Profile) *tunedv1.Profile

func makeFakeProfile(name, tunedProfile string, modifiers ...profileModifier) *tunedv1.Profile {
	profile := &tunedv1.Profile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: tunedv1.ProfileSpec{
			Config: tunedv1.ProfileConfig{
				TunedProfile: tunedProfile,
			},
		},
		Status: tunedv1.ProfileStatus{
			TunedProfile: tunedProfile,
			Conditions:   []tunedv1.StatusCondition{},
		},
	}
	for _, modifier := range modifiers {
		profile = modifier(profile)
	}
	return profile
}

func withProfileApplied() profileModifier {
	return func(p *tunedv1.Profile) *tunedv1.Profile {
		p.Status.Conditions = append(p.Status.Conditions, tunedv1.StatusCondition{
			Type:   tunedv1.TunedProfileApplied,
			Status: corev1.ConditionTrue,
		})
		return p
	}
}

func withProfileDegraded() profileModifier {
	return func(p *tunedv1.Profile) *tunedv1.Profile {
		p.Status.Conditions = append(p.Status.Conditions, tunedv1.StatusCondition{
			Type:   tunedv1.TunedDegraded,
			Status: corev1.ConditionTrue,
		})
		return p
	}
}

// Condition helpers
func makeCondition(condType configv1.ClusterStatusConditionType, status configv1.ConditionStatus, reason string) configv1.ClusterOperatorStatusCondition {
	return configv1.ClusterOperatorStatusCondition{
		Type:   condType,
		Status: status,
		Reason: reason,
	}
}

// Mock implementations
type mockDaemonSetLister struct {
	ds  *appsv1.DaemonSet
	err error
}

func (m *mockDaemonSetLister) List(selector labels.Selector) ([]*appsv1.DaemonSet, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.ds != nil {
		return []*appsv1.DaemonSet{m.ds}, nil
	}
	return []*appsv1.DaemonSet{}, nil
}

func (m *mockDaemonSetLister) Get(name string) (*appsv1.DaemonSet, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.ds != nil && m.ds.Name == name {
		return m.ds, nil
	}
	return nil, &notFoundError{name: name}
}

type mockProfileLister struct {
	profiles []*tunedv1.Profile
	err      error
}

func (m *mockProfileLister) List(selector labels.Selector) ([]*tunedv1.Profile, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.profiles, nil
}

func (m *mockProfileLister) Get(name string) (*tunedv1.Profile, error) {
	if m.err != nil {
		return nil, m.err
	}
	for _, p := range m.profiles {
		if p.Name == name {
			return p, nil
		}
	}
	return nil, &notFoundError{name: name}
}

type notFoundError struct {
	name string
}

func (e *notFoundError) Error() string {
	return "not found: " + e.name
}

func (e *notFoundError) Status() metav1.Status {
	return metav1.Status{Reason: metav1.StatusReasonNotFound}
}

// Test context helper
func newTestController(daemonSet *appsv1.DaemonSet, profiles []*tunedv1.Profile, bootcmdlineConflict, mcLabelsAcrossMCP map[string]bool) *Controller {
	if bootcmdlineConflict == nil {
		bootcmdlineConflict = make(map[string]bool)
	}
	if mcLabelsAcrossMCP == nil {
		mcLabelsAcrossMCP = make(map[string]bool)
	}

	return &Controller{
		listers: &ntoclient.Listers{
			DaemonSets:    &mockDaemonSetLister{ds: daemonSet},
			TunedProfiles: &mockProfileLister{profiles: profiles},
		},
		bootcmdlineConflict: bootcmdlineConflict,
		mcLabelsAcrossMCP:   mcLabelsAcrossMCP,
	}
}

// ConditionsEqual returns true if and only if the provided slices of conditions
// (ignoring LastTransitionTime and Message) are equal.
func conditionsEqual(oldConditions, newConditions []configv1.ClusterOperatorStatusCondition) bool {
	if len(newConditions) != len(oldConditions) {
		return false
	}

	for _, conditionA := range oldConditions {
		foundMatchingCondition := false

		for _, conditionB := range newConditions {
			// Compare every field except LastTransitionTime.
			if conditionA.Type == conditionB.Type &&
				conditionA.Status == conditionB.Status &&
				conditionA.Reason == conditionB.Reason {
				foundMatchingCondition = true
				break
			}
		}

		if !foundMatchingCondition {
			return false
		}
	}

	return true
}

func TestComputeStatus(t *testing.T) {
	testCases := []testCase{
		{
			// No previous operator deployment, new installation.
			name:  "initial sync, new installation",
			tuned: makeFakeTuned(),
			// No daemonSet (nil), no profiles, no old conditions
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionFalse, "TunedUnavailable"),
				// OCPSTRAT-2484: Operators MUST go progressing when transitioning between versions (fresh install here).
				makeCondition(configv1.OperatorProgressing, configv1.ConditionTrue, "Reconciling"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionFalse, "Reconciling"),
			},
			expectedOperandVersion: "", // daemonset progressing, empty string is returned
		},
		{
			// DaemonSet is fully deployed, its stable generation annotation is not yet updated.
			name:  "daemonset fully available, annotation not updated",
			tuned: makeFakeTuned(),
			daemonSet: getDaemonSet(
				withDaemonSetGeneration(2, 2),
				withDaemonSetStatus(3, 3, 3, 0),
				withDesiredNumberScheduled(3),
				withDaemonSetAnnotation(tunedv1.StableGenerationAnnotationName, "1"),
			),
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionTrue, "AsExpected"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionFalse, "AsExpected"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionFalse, "AsExpected"),
			},
			expectedOperandVersion: testReleaseVer,
		},
		{
			// DaemonSet is fully deployed, its stable generation annotation is updated.
			name:  "daemonset fully available, annotation updated",
			tuned: makeFakeTuned(),
			daemonSet: getDaemonSet(
				withDaemonSetGeneration(2, 2),
				withDaemonSetStatus(3, 3, 3, 0),
				withDesiredNumberScheduled(3),
				withDaemonSetAnnotation(tunedv1.StableGenerationAnnotationName, "2"),
			),
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionTrue, "AsExpected"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionFalse, "AsExpected"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionFalse, "AsExpected"),
			},
			expectedOperandVersion: testReleaseVer,
		},
		{
			// Operator upgrade, ClusterOperator must be progressing.
			name:  "daemonset/operator progressing, generation mismatch",
			tuned: makeFakeTuned(),
			daemonSet: getDaemonSet(
				withDaemonSetGeneration(2, 2),
				withDaemonSetStatus(3, 1, 3, 0), // numberReady=3, updatedNumber=1, numberAvailable=3, numberUnavailable=0
				withDesiredNumberScheduled(3),
				withDaemonSetAnnotation(tunedv1.StableGenerationAnnotationName, "1"),
			),
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionTrue, "AsExpected"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionTrue, "Reconciling"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionFalse, "AsExpected"),
			},
			expectedOperandVersion: "",
		},
		{
			// DaemonSet is not fully updated, generation != observedGeneration
			name:  "daemonset generation vs observedGeneration mismatch",
			tuned: makeFakeTuned(),
			daemonSet: getDaemonSet(
				withDaemonSetGeneration(2, 1),
				withDaemonSetStatus(3, 3, 3, 0), // numberReady=3, updatedNumber=3, numberAvailable=3, numberUnavailable=0
				withDesiredNumberScheduled(3),
				withDaemonSetAnnotation(tunedv1.StableGenerationAnnotationName, "1"),
			),
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionTrue, "AsExpected"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionTrue, "Reconciling"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionFalse, "AsExpected"),
			},
			expectedOperandVersion: "",
		},
		{
			// Upgrade finished, we have a stale ClusterOperator progressing condition.
			name:      "progressed to available/stable (upgrade finished)",
			tuned:     makeFakeTuned(),
			daemonSet: getDaemonSet(),
			oldConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionFalse, "TunedUnavailable"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionTrue, "Reconciling"),
			},
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionTrue, "AsExpected"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionFalse, "AsExpected"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionFalse, "AsExpected"),
			},
			expectedOperandVersion: testReleaseVer,
		},
		{
			// When DaemonSet has no available pods, we must report ClusterOperator unavailable condition.
			name:  "daemonset has no available pods",
			tuned: makeFakeTuned(),
			daemonSet: getDaemonSet(
				withDaemonSetStatus(3, 0, 0, 3),
				withDesiredNumberScheduled(3),
				withDaemonSetAnnotation(tunedv1.StableGenerationAnnotationName, "1"),
			),
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionFalse, "TunedUnavailable"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionFalse, "AsExpected"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionFalse, "AsExpected"),
			},
			expectedOperandVersion: testReleaseVer,
		},
		{
			// Profiles progressing do not cause ClusterOperator progressing=true condition.
			name:      "profile progressing",
			tuned:     makeFakeTuned(),
			daemonSet: getDaemonSet(),
			profiles: []*tunedv1.Profile{
				makeFakeProfile("node1", "openshift-node", withProfileApplied()),
				makeFakeProfile("node2", "openshift-node"),
				makeFakeProfile("node3", "openshift-node"),
			},
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionTrue, "ProfileProgressing"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionFalse, "AsExpected"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionFalse, "AsExpected"),
			},
			expectedOperandVersion: testReleaseVer,
		},
		{
			// Profiles progressing do not cause ClusterOperator degraded=true condition.
			name:      "profiles degraded",
			tuned:     makeFakeTuned(),
			daemonSet: getDaemonSet(),
			profiles: []*tunedv1.Profile{
				makeFakeProfile("node1", "openshift-node", withProfileApplied()),
				makeFakeProfile("node2", "openshift-node", withProfileDegraded()),
			},
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionTrue, "ProfileDegraded"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionFalse, "AsExpected"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionFalse, "AsExpected"),
			},
			expectedOperandVersion: testReleaseVer,
		},
		{
			// bootcmdline conflict is a serious cluster mis-configuration, report ClusterOperator degraded=true condition.
			name:      "bootcmdline conflict",
			tuned:     makeFakeTuned(),
			daemonSet: getDaemonSet(),
			profiles: []*tunedv1.Profile{
				makeFakeProfile("node1", "openshift-node", withProfileApplied()),
				makeFakeProfile("node2", "openshift-node", withProfileApplied()),
			},
			bootcmdlineConflict: map[string]bool{"node1": true},
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionTrue, "AsExpected"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionFalse, "AsExpected"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionTrue, "ProfileConflict"),
			},
			expectedOperandVersion: testReleaseVer,
		},
		{
			// MC labels across MCPs is a serious cluster mis-configuration, report ClusterOperator degraded=true condition.
			name:      "MC labels across MCPs",
			tuned:     makeFakeTuned(),
			daemonSet: getDaemonSet(),
			profiles: []*tunedv1.Profile{
				makeFakeProfile("node1", "openshift-node", withProfileApplied()),
				makeFakeProfile("node2", "openshift-node", withProfileApplied()),
			},
			mcLabelsAcrossMCP: map[string]bool{"node1": true},
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionTrue, "AsExpected"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionFalse, "AsExpected"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionTrue, "MCLabelsAcrossMCPs"),
			},
			expectedOperandVersion: testReleaseVer,
		},
		{
			name:  "daemonset not found with existing conditions",
			tuned: makeFakeTuned(),
			// daemonSet is nil (not found)
			oldConditions: []configv1.ClusterOperatorStatusCondition{
				{
					Type:   configv1.OperatorAvailable,
					Status: configv1.ConditionFalse,
					Reason: "TunedUnavailable",
				},
			},
			expectErr: true,
		},
		{
			name:      "management state unmanaged",
			tuned:     makeFakeTuned(withManagementState(operatorv1.Unmanaged)),
			daemonSet: getDaemonSet(),
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionTrue, "Unmanaged"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionFalse, "Unmanaged"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionFalse, "Unmanaged"),
			},
			expectedOperandVersion: "",
		},
		{
			name:      "management state removed",
			tuned:     makeFakeTuned(withManagementState(operatorv1.Removed)),
			daemonSet: getDaemonSet(),
			expectedConditions: []configv1.ClusterOperatorStatusCondition{
				makeCondition(configv1.OperatorAvailable, configv1.ConditionTrue, "Removed"),
				makeCondition(configv1.OperatorProgressing, configv1.ConditionFalse, "Removed"),
				makeCondition(configv1.OperatorDegraded, configv1.ConditionFalse, "Removed"),
			},
			expectedOperandVersion: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			os.Setenv("RELEASE_VERSION", testReleaseVer)
			defer os.Unsetenv("RELEASE_VERSION")

			c := newTestController(tc.daemonSet, tc.profiles, tc.bootcmdlineConflict, tc.mcLabelsAcrossMCP)

			conditions, operandVersion, err := c.computeStatus(tc.tuned, tc.oldConditions)

			if err != nil && !tc.expectErr {
				t.Errorf("computeStatus() returned unexpected error: %v", err)
			}
			if err == nil && tc.expectErr {
				t.Error("computeStatus() unexpectedly succeeded when error was expected")
			}
			if tc.expectErr {
				// Skip further checks if error was expected
				return
			}

			// Assert - Check operand version
			if operandVersion != tc.expectedOperandVersion {
				t.Errorf("operandVersion mismatch: expected %q, got %q", tc.expectedOperandVersion, operandVersion)
			}

			// Assert - Check conditions
			if !conditionsEqual(tc.expectedConditions, conditions) {
				t.Errorf("conditions mismatch:\nExpected: %+v\nGot:      %+v", tc.expectedConditions, conditions)
			}
		})
	}
}
