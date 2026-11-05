package extensiontests

import "sync/atomic"

type SpecTask struct {
	fn func(spec ExtensionTestSpec)
}

func (t *SpecTask) Run(spec ExtensionTestSpec) {
	t.fn(spec)
}

type TestResultTask struct {
	fn func(result *ExtensionTestResult)
}

func (t *TestResultTask) Run(result *ExtensionTestResult) {
	t.fn(result)
}

// BeforeSpawnTask wraps a function that can configure a parallel test process
// immediately before it is spawned.
type BeforeSpawnTask struct {
	fn func(name string, options *SpawnOptions)
}

func (t *BeforeSpawnTask) Run(name string, options *SpawnOptions) {
	t.fn(name, options)
}

type OneTimeTask struct {
	fn       func()
	executed int32 // Atomic boolean to indicate whether the function has been run
}

func (t *OneTimeTask) Run() {
	// Ensure one-time tasks are only run once
	if atomic.CompareAndSwapInt32(&t.executed, 0, 1) {
		t.fn()
	}
}
