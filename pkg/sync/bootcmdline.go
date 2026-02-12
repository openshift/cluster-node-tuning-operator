// Package sync provides synchronization primitives for coordinating
// between the operator controller and the PerformanceProfile controller.
// This is used to ensure MachineConfigs created by both controllers
// are synchronized to prevent race conditions.
package sync

import (
	"strings"
	"sync"

	"k8s.io/klog/v2"
)

// BootcmdlineSync provides synchronization for bootcmdline readiness
// between the operator controller and the PerformanceProfile controller.
// The operator controller signals when bootcmdline parameters are ready
// for a given MachineConfigPool, including the bootcmdlineDeps (list of
// Tuned CR names and generations) that the bootcmdline was calculated from.
// The PerformanceProfile controller checks that the bootcmdlineDeps matches
// its expected value before creating its MachineConfig.
type BootcmdlineSync struct {
	mtx sync.RWMutex
	// readyPools maps MCP name to the bootcmdlineDeps string that the bootcmdline
	// was calculated from. The bootcmdlineDeps is a comma-separated list of
	// Tuned CR names and generations: "name1:gen1,name2:gen2,...nameN:genN".
	// This ensures generation-aware readiness checking to prevent races.
	readyPools map[string]string
	// reconcileTrigger is a channel that receives MCP names when they become ready.
	// The PerformanceProfile controller watches this to trigger immediate reconciliation.
	reconcileTrigger chan string
}

// Global instance for cross-controller synchronization.
var globalSync = &BootcmdlineSync{
	readyPools: make(map[string]string),
}

// GetBootcmdlineSync returns the global BootcmdlineSync instance.
func GetBootcmdlineSync() *BootcmdlineSync {
	return globalSync
}

// SetReconcileTrigger sets the channel that will receive MCP names when
// bootcmdline becomes ready. This should be called during controller setup.
// The channel should be buffered to avoid blocking the operator controller.
func (b *BootcmdlineSync) SetReconcileTrigger(ch chan string) {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	b.reconcileTrigger = ch
}

// GetReconcileTrigger returns the reconcile trigger channel.
func (b *BootcmdlineSync) GetReconcileTrigger() chan string {
	b.mtx.RLock()
	defer b.mtx.RUnlock()
	return b.reconcileTrigger
}

// SignalReady signals that bootcmdline is ready for the given MCP with the
// specified bootcmdlineDeps. The bootcmdlineDeps is a comma-separated list of
// Tuned CR names and generations: "name1:gen1,name2:gen2,...nameN:genN".
// This is called by the operator controller after it has verified that all
// nodes in the MCP have their bootcmdline annotation set and they all agree
// on both the bootcmdlineDeps and bootcmdline values.
// It also triggers immediate reconciliation of the PerformanceProfile controller.
func (b *BootcmdlineSync) SignalReady(mcpName string, bootcmdlineDeps string) {
	b.mtx.Lock()
	defer b.mtx.Unlock()

	klog.V(2).Infof("BootcmdlineSync: signaling bootcmdline ready for MCP %q with deps %q", mcpName, bootcmdlineDeps)
	b.readyPools[mcpName] = bootcmdlineDeps

	// Trigger PerformanceProfile reconciliation (non-blocking send).
	if b.reconcileTrigger != nil {
		select {
		case b.reconcileTrigger <- mcpName:
			klog.V(2).Infof("BootcmdlineSync: triggered reconciliation for MCP %q", mcpName)
		default:
			// Channel full, reconciliation will happen via regular requeue.
			klog.V(2).Infof("BootcmdlineSync: reconcile trigger channel full for MCP %q, will use requeue", mcpName)
		}
	}
}

// IsReady returns true if bootcmdline is ready for the given MCP and includes
// the expected Tuned CR dependency. This performs a generation-aware check by
// verifying that expectedBootcmdlineDeps (format: "name:generation") is present
// as a substring in the full bootcmdlineDeps string that was signaled by the
// operator controller.
func (b *BootcmdlineSync) IsReady(mcpName string, expectedBootcmdlineDeps string) bool {
	b.mtx.RLock()
	defer b.mtx.RUnlock()

	if expectedBootcmdlineDeps == "" {
		// Empty expectedBootcmdlineDeps means skip the check (e.g., for tests)
		return true
	}

	actualBootcmdlineDeps := b.readyPools[mcpName]
	if actualBootcmdlineDeps == "" {
		// Not signaled ready yet
		return false
	}

	// The bootcmdlineDeps is a comma-separated list: "name1:gen1,name2:gen2,...".
	actualDeps := strings.Split(actualBootcmdlineDeps, ",")
	ready := false
	for _, dep := range actualDeps {
		if dep == expectedBootcmdlineDeps {
			ready = true
			break
		}
	}

	if !ready {
		klog.V(2).Infof("BootcmdlineSync: MCP %q not ready - expected Tuned dep %q not found in actual deps %q", mcpName, expectedBootcmdlineDeps, actualBootcmdlineDeps)
	} else {
		klog.V(2).Infof("BootcmdlineSync: MCP %q ready - expected Tuned dep %q found in actual deps %q", mcpName, expectedBootcmdlineDeps, actualBootcmdlineDeps)
	}
	return ready
}

// ClearCacheForPool removes the cache entry for the specified pool.
func (b *BootcmdlineSync) ClearCacheForPool(pool string) {
	b.mtx.Lock()
	defer b.mtx.Unlock()

	klog.V(2).Infof("BootcmdlineSync: clearing cache for pool %q", pool)
	delete(b.readyPools, pool)
}

// Reset clears all state. Primarily for testing.
func (b *BootcmdlineSync) Reset() {
	b.mtx.Lock()
	defer b.mtx.Unlock()
	b.readyPools = make(map[string]string)
}
