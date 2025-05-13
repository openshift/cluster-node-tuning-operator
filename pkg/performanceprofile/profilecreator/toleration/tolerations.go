package toleration

const (
	EnableHardwareTuning = "enableHardwareTuning"
	DifferentCoreIDs     = "differentCoreIDs"
)

// Set records the data to be tolerated or warned about based on the tool handling
type Set map[string]bool
