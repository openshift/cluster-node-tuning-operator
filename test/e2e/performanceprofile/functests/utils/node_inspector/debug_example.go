package node_inspector

// Example usage of the debugging functions for troubleshooting node inspector issues
//
// This file contains examples of how to use the enhanced debugging features
// to diagnose why the node inspector might not be running.

import (
	"context"
	"fmt"

	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
)

// ExampleDebuggingNodeInspector shows how to use the debugging functions
// Call this in your test's BeforeEach or when you encounter node inspector issues
func ExampleDebuggingNodeInspector(ctx context.Context) {
	// Example 1: Check status without executing commands
	testlog.Info("=== Checking node inspector status ===")
	if err := CheckStatus(ctx); err != nil {
		testlog.Errorf("Node inspector status check failed: %v", err)
	}

	// Example 2: If having persistent issues, try force reinitializing
	testlog.Info("=== Force reinitializing node inspector ===")
	if err := ForceReinitialize(ctx); err != nil {
		testlog.Errorf("Force reinitialization failed: %v", err)
	}
}

// ExampleErrorHandling shows how the enhanced error messages help with debugging
func ExampleErrorHandling(ctx context.Context) {
	// When you encounter the "node inspector is not running" error,
	// the new implementation will automatically provide detailed debug information
	// in the logs including:
	//
	// 1. Namespace status
	// 2. DaemonSet status (desired vs ready pods)
	// 3. Individual pod statuses and conditions
	// 4. Container states (waiting, running, terminated)
	// 5. Recent events in the namespace
	// 6. Target node conditions
	//
	// Example log output:
	// === NODE INSPECTOR DEBUG INFORMATION ===
	// ✅ Namespace node-inspector-ns: exists (phase: Active)
	// ✅ DaemonSet node-inspector-ns/node-inspector: exists
	//    Desired: 2, Current: 2, Ready: 1, Available: 1
	//    Updated: 2, Misscheduled: 0
	// 📋 Found 2 node inspector pods:
	//    Pod 1: node-inspector-abc (node: worker-0)
	//      Phase: Running, Ready: true
	//    Pod 2: node-inspector-def (node: worker-1)
	//      Phase: Pending, Ready: false
	//      ⭐ This is the pod for the target node
	//      Container 1 (node-daemon): Ready=false, Restarts=0
	//        State: Waiting (ImagePullBackOff: Failed to pull image)
	// 📅 Recent events:
	//    15:06:12: Warning Failed Failed to pull image (node-inspector-def)
	// 🖥️  Target node worker-1 conditions:
	//    Ready: True (kubelet is posting ready status)
	//    MemoryPressure: False (kubelet has sufficient memory available)

	// This makes it much easier to diagnose the actual problem!
	fmt.Println("See the logs for detailed debugging information when node inspector fails")
}
