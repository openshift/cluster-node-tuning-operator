package utils

import (
	"bytes"
	"context"
	"fmt"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/bugzilla"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/jira"
	"os"
	"os/exec"
	"strings"
	"time"

	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"

	. "github.com/onsi/ginkgo/v2"
)

const defaultExecTimeout = 2 * time.Minute
const SkipBzChecksEnvVar = "NO_BZ_CHECKS"

func CustomBeforeAll(fn func()) {
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

func knownIssueIsFixedByStatus(status string) bool {
	lowStatus := strings.ToLower(status)
	return lowStatus == "verified" || lowStatus == "done" || lowStatus == "closed"
}

// Check status of an issue in Jira and skip the test when the issue
// is not yet resolved (Verified or Closed)
func KnownIssueJira(key string) {
	val := os.Getenv(SkipBzChecksEnvVar)
	if val != "" {
		testlog.Infof(fmt.Sprintf("Skipping Jira issue %s status check", key))
		return
	}

	response, err := jira.RetrieveJiraStatus(key)
	if err != nil {
		testlog.Warningf("failed to retrieve status of Jira issue %s: %v", key, err)
		return
	}

	if response.Fields.Status.Name == "" {
		testlog.Infof(fmt.Sprintf("Test is linked to an unknown Jira issue %s", key))
	} else if !knownIssueIsFixedByStatus(response.Fields.Status.Name) {
		Skip(fmt.Sprintf("Test skipped as it is linked to a known Jira issue %s - %s", response.Key, response.Fields.Summary))
	} else {
		testlog.Infof(fmt.Sprintf("Test is linked to a closed Jira issue %s - %s", response.Key, response.Fields.Summary))
	}
}

// Check status of an issue in Bugzilla and skip the test when the issue
// is not yet resolved (Verified or Closed)
func KnownIssueBugzilla(bugId int) {
	val := os.Getenv(SkipBzChecksEnvVar)
	if val != "" {
		testlog.Infof(fmt.Sprintf("Skipping rhbz#%d status check", bugId))
		return
	}

	bug, err := bugzilla.RetrieveBug(bugId)
	if err != nil {
		testlog.Warningf("failed to retrieve status of rhbz#%d: %v", bugId, err)
		return
	}

	if !knownIssueIsFixedByStatus(bug.Status) {
		Skip(fmt.Sprintf("Test skipped as it is linked to a known Bugzilla bug rhbz#%d - %s", bug.Id, bug.Summary))
	} else {
		testlog.Infof(fmt.Sprintf("Test is linked to a closed Bugzilla bug rhbz#%d - %s", bug.Id, bug.Summary))
	}
}
