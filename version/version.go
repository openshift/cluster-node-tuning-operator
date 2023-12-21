package version

const (
	OperandFilename          = "openshift-tuned"
	OperatorFilename         = "cluster-node-tuning-operator"
	ReleaseVersionEnvVarName = "RELEASE_VERSION"
	OverrideReleaseVersionVarName = "OVERRIDE_RELEASE_VERSION"
)

var (
	// Version is the operator version
	Version = "0.0.1"
	// GitCommit is the current git commit hash
	GitCommit = "n/a"
	// BuildDate is the build date
	BuildDate = "n/a"
)
