// Package sync provides synchronization primitives for coordinating
// between the operator controller and the PerformanceProfile controller.
// This is used to ensure creating and updating MachineConfigs by both
// controllers is synchronized as much as possible to prevent multiple reboots.
package sync

import (
	"os"
	"strings"
	"sync"

	"k8s.io/klog/v2"
)

// BootcmdlineSync provides synchronization for bootcmdline readiness
// between the operator controller and the PerformanceProfile controller.
type BootcmdlineSync struct {
	mtx sync.RWMutex
	// readyPools maps pool name to the bootcmdlineDeps string that the bootcmdline
	// was calculated from. The bootcmdlineDeps is a comma-separated list with
	// RELEASE_VERSION first, followed by Tuned CR names and generations:
	// "<version>,<name1>:<gen1>,<name2>:<gen2>,...<nameN>:<genN>".
	readyPools map[string]string
	// reconcileTrigger is a channel that receives pool names when they become ready.
	// The PerformanceProfile controller watches this to trigger immediate reconciliation.
	reconcileTrigger chan string
}

// BootcmdlineNotReadyError is returned when the bootcmdline sync signal hasn't been received yet.
// This error indicates that the PerformanceProfile controller should requeue and wait for
// the operator controller to signal that bootcmdline is ready.
// The operator controller will trigger immediate reconciliation by SignalReady().  The requeue
// PerformanceProfile controller's interval serves only as a fallback.
type BootcmdlineNotReadyError struct {
	PoolName string
	Message  string
}

const (
	// Maximum number of (MachineConfig or node) pools for a cluster to guarantee an immediate
	// PerformanceProfile reconciliation.
	PoolsMax = 1024
)

// Global instance for cross-controller synchronization.
var globalSync = &BootcmdlineSync{
	readyPools: make(map[string]string),
}

func (e *BootcmdlineNotReadyError) Error() string {
	return e.Message
}

// GetBootcmdlineSync returns the global BootcmdlineSync instance.
func GetBootcmdlineSync() *BootcmdlineSync {
	return globalSync
}

// SetReconcileTrigger sets the channel that will receive pool names when
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

// SignalReady signals that bootcmdline is ready for the given pool with the
// specified bootcmdlineDeps. The bootcmdlineDeps is a comma-separated list with
// RELEASE_VERSION first, followed by Tuned CR names and generations:
// "<version>,<name1>:<gen1>,<name2>:<gen2>,...<nameN>:<genN>".
// This is called by the operator controller after it has verified that all
// nodes in the pool have their bootcmdline annotation set and they all agree
// on both the bootcmdlineDeps and bootcmdline values.
// It also triggers immediate reconciliation of the PerformanceProfile controller.
//
// SignalReady deduplicates.  If the pool's bootcmdlineDeps are unchanged from
// the last call, the state update and trigger are both skipped.  This is
// important because the operator controller calls SignalReady once per node in
// the pool (one per Tuned Profile), so without deduplication a cluster with N
// worker nodes would flood the trigger channel with N identical signals on
// every reconcile burst and potentially even overflow the buffered channel.
func (b *BootcmdlineSync) SignalReady(pool string, bootcmdlineDeps string) {
	b.mtx.Lock()
	defer b.mtx.Unlock()

	// Deduplicate.  Skip if bootcmdline dependencies are unchanged.  The readyPools
	// map is the authoritative record of what was last signaled.
	if b.readyPools[pool] == bootcmdlineDeps {
		klog.V(4).Infof("BootcmdlineSync: pool %q deps unchanged (%q), skipping signal", pool, bootcmdlineDeps)
		return
	}

	klog.Infof("BootcmdlineSync: signaling bootcmdline ready for pool %q with deps %q", pool, bootcmdlineDeps)
	b.readyPools[pool] = bootcmdlineDeps

	// Trigger PerformanceProfile reconciliation (non-blocking send).
	if b.reconcileTrigger != nil {
		select {
		case b.reconcileTrigger <- pool:
			klog.V(2).Infof("BootcmdlineSync: triggered reconciliation for pool %q", pool)
		default:
			// b.reconcileTrigger channel full!  Cannot guarantee immediate reconciliation, it will happen via regular requeue.
			klog.V(2).Infof("BootcmdlineSync: reconcile trigger channel full for pool %q, will use requeue", pool)
		}
	}
}

// IsReady returns true if bootcmdline is ready for the given pool and includes
// the expected Tuned CR dependency and the expected RELEASE_VERSION.
// This performs a Tuned CR generation-aware check by verifying expectedBootcmdlineDep
// (format: "name:gen") is present in the full bootcmdlineDeps string that
// was signaled by the operator controller, and that the RELEASE_VERSION matches.
func (b *BootcmdlineSync) IsReady(pool string, expectedBootcmdlineDep string) bool {
	b.mtx.RLock()
	defer b.mtx.RUnlock()

	bootcmdlineDeps := b.readyPools[pool]
	if bootcmdlineDeps == "" {
		// Not signaled ready yet.
		return false
	}

	// The bootcmdlineDeps is a comma-separated list:
	// "<version>,<name1>:<gen1>,<name2>:<gen2>,...<nameN>:<genN>".
	cachedDeps := strings.Split(bootcmdlineDeps, ",")

	// Always expect the *current* operator RELEASE_VERSION to match operand RELEASE_VERSION propagated to Node annotations.
	// Think upgrades.  We don't want to signal "ready" until all Nodes in the same pool recalculated kernel parameters.
	expectedReleaseVersion := os.Getenv("RELEASE_VERSION")
	releaseVersionMatch := cachedDeps[0] == expectedReleaseVersion

	// Check for the Tuned CR dependency (skip the first element/RELEASE_VERSION).
	tunedDepMatch := false
	for _, dep := range cachedDeps[1:] {
		if dep == expectedBootcmdlineDep {
			tunedDepMatch = true
			break
		}
	}

	ready := tunedDepMatch && releaseVersionMatch

	if ready {
		klog.V(2).Infof("BootcmdlineSync: pool %q ready: expected Tuned dep %q and RELEASE_VERSION %q found in actual deps %q", pool, expectedBootcmdlineDep, expectedReleaseVersion, bootcmdlineDeps)
	} else {
		if !tunedDepMatch {
			klog.V(2).Infof("BootcmdlineSync: pool %q not ready: expected Tuned dep %q not found in actual deps %q", pool, expectedBootcmdlineDep, bootcmdlineDeps)
		}
		if !releaseVersionMatch {
			klog.V(2).Infof("BootcmdlineSync: pool %q not ready: expected RELEASE_VERSION %q not found in actual deps %q", pool, expectedReleaseVersion, bootcmdlineDeps)
		}
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
