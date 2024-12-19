package label

// The label package contains a list of labels that can be used in
// the functests to indicate and point certain behavior or characteristics
// of the various tests.
// Those can be filtered/focused by ginkgo before the test runs.

// Kind is a label indicates specific criteria that classify the test.
// A Mixture of kinds can be used for the same test
type Kind string

const (
	// Slow means test that usually requires reboot or takes a long time to finish.
	Slow Kind = "slow"

	// ReleaseCritical is for tests that are critical for a successful release of the component that is being tested.
	ReleaseCritical Kind = "release-critical"

	// SpecializedHardware is for tests that need special Hardware like SR-IOV devices, specific NICs, etc.
	SpecializedHardware Kind = "specialized-hardware"
)

// Feature is a label indicates the feature that being tested.
type Feature string

const (
	// WorkloadHints should be added in tests that are validating/verifying workload-hints behavior.
	WorkloadHints Feature = "workload-hints"

	// MustGather should be added in tests that are validating/verifying must-gather tool functionality.
	MustGather Feature = "must-gather"

	// MixedCPUs should be added in tests that are validating/verifying mixed-cpus feature functionality.
	MixedCPUs Feature = "mixed-cpus"

	// Latency should be added in tests that are meant to test latency sensitivity.
	Latency Feature = "latency"

	// PerformanceProfileCreator should be added in tests that are validating/verifying performance-profile-creator tool
	// functionally.
	PerformanceProfileCreator Feature = "performance-profile-creator"

	// MemoryManager should be added in tests that are meant to test memory manager tests
	MemoryManager Feature = "memory-manager"

	// OfflineCPUs should be added in tests that are meant to test offline cpus tests
	OfflineCPUs Feature = "offline-cpus"

	// RPSMask should be added in tests that are meant to test RPS Masks
	RPSMask Feature = "rps-mask"

	// OVSPinning should be added in tests that test Dynamic OVS Pinning
	OVSPinning Feature = "ovs-pinning"

	// ExperimentalAnnotations ExperimentalAnnotation should be added in tests that test Kubelet changes using
	// experimental annotation
	ExperimentalAnnotations Feature = "experimental-annotations"

	// TunedDeferred should be added in tests that test tuned deferred updates annotation
	TunedDeferred Feature = "tuned-deferred"
)

// Tier is a label to classify tests under specific grade/level
// that should roughly describe the execution complexity, maintainer identity and processing criteria.
type Tier string

const (
	// Tier0 are automated unit tests
	// Minimal time needed to execute (minutes to 1 hour)
	// Process criteria:
	// 100% automated, must-pass 100%
	// Development maintains tests, and reviews result
	Tier0 Tier = "tier-0"

	// Tier1 are component level functional tests
	// Minimal time needed to execute (minutes to hours)
	// Process criteria:
	// Executed after tier 0 passing
	// 100% automated and must pass 100%
	// QE / Development maintains tests and reviews results
	Tier1 Tier = "tier-1"

	// Tier2 are integration level functional tests
	// May include basic non-functional tests (security, performance regression, install, compose validation)
	// Runs during nightly time frame
	// Process criteria:
	// Executed after components pass tier 1 testing
	// 100% automated and must pass 100%
	// QE maintains tests and reviews result
	Tier2 Tier = "tier-2"

	// Tier3 are system, scenario and non-functional tests (which includes Fault Tolerance / Recovery / Fail over)
	// Test that don't fit in tier 2 due to time, complexity, and other factors
	// Process criteria:
	// Executed after components pass tier 2 testing
	// System or scenario testing doesn't block Non-functional tests.They can be executed in parallel.
	// 100% automated
	// QE maintains tests and reviews result
	Tier3 Tier = "tier-3"
)

// Platform is a label that used to specify the type of platform on which
// tests are supposed to run
type Platform string

const (
	// OpenShift means that tests are relevant only for OpenShift platform
	OpenShift Platform = "openshift"

	// HyperShift means that tests relevant only for HyperShift platform
	HyperShift Platform = "hypershift"
)
