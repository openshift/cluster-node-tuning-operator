package utils

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo"

	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
)

const defaultExecTimeout = 2 * time.Minute

func BeforeAll(fn func()) {
	first := true
	BeforeEach(func() {
		if first {
			fn()
			first = false
		}
	})
}

func ExecAndLogCommand(name string, arg ...string) ([]byte, error) {
	outData, _, err := ExecAndLogCommandWithStderr(name, arg...)
	return outData, err
}

func ExecAndLogCommandWithStderr(name string, arg ...string) ([]byte, []byte, error) {
	// Create a new context and add a timeout to it
	ctx, cancel := context.WithTimeout(context.Background(), defaultExecTimeout)
	defer cancel() // The cancel should be deferred so resources are cleaned up

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, arg...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	outData := stdout.Bytes()
	errData := stderr.Bytes()
	testlog.Infof("run command '%s %v' (err=%v):\n  stdout=%q\n  stderr=%q", name, arg, err, outData, errData)

	// We want to check the context error to see if the timeout was executed.
	// The error returned by cmd.Output() will be OS specific based on what
	// happens when a process is killed.
	if ctx.Err() == context.DeadlineExceeded {
		return nil, nil, fmt.Errorf("command '%s %v' failed because of the timeout", name, arg)
	}

	if _, ok := err.(*exec.ExitError); ok {
		testlog.Infof("run command '%s %v' (err=%v):\n  stderr=%s", name, arg, err, string(errData))
	}
	return outData, errData, err
}
