package ginkgo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/openshift-eng/openshift-tests-extension/pkg/dbtime"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
)

// parentGracePeriod is extra time the parent waits beyond the child's
// --timeout before sending SIGINT, so the child can exit cooperatively
// (e.g. via NodeTimeout) before the parent escalates.
const parentGracePeriod = 2 * time.Minute

// SpawnProcessToRunTestWithEnv is like SpawnProcessToRunTest but merges extra
// environment variables into the child process on top of os.Environ().
// A nil or empty env inherits the parent environment plus SpawnedChildEnv.
func SpawnProcessToRunTestWithEnv(ctx context.Context, testName string, timeout time.Duration, env map[string]string) *extensiontests.ExtensionTestResult {
	return spawnProcess(ctx, testName, timeout, env)
}

func SpawnProcessToRunTest(ctx context.Context, testName string, timeout time.Duration) *extensiontests.ExtensionTestResult {
	return SpawnProcessToRunTestWithEnv(ctx, testName, timeout, nil)
}

func spawnProcess(ctx context.Context, testName string, timeout time.Duration, env map[string]string) *extensiontests.ExtensionTestResult {
	parentTimeout := timeout + parentGracePeriod
	// longerCtx is used to backstop the process, but leave termination up to us if possible to allow a double interrupt
	longerCtx, longerCancel := context.WithTimeout(ctx, parentTimeout+15*time.Minute)
	defer longerCancel()
	timeoutCtx, shorterCancel := context.WithTimeout(longerCtx, parentTimeout)
	defer shorterCancel()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	start := time.Now()
	command := exec.CommandContext(longerCtx, os.Args[0], "run-test", "--output=json", fmt.Sprintf("--timeout=%s", timeout), testName)
	command.Stdout = stdout
	command.Stderr = stderr
	mergedEnv, err := mergeEnv(os.Environ(), envWithSpawnedChildMarker(env))
	if err != nil {
		fmt.Fprintf(stderr, "Invalid child environment: %v\n", err)
		return newTestResult(testName, extensiontests.ResultFailed, start, time.Now(), stdout, stderr)
	}
	command.Env = mergedEnv

	err = command.Start()
	if err != nil {
		fmt.Fprintf(stderr, "Command Start Error: %v\n", err)
		return newTestResult(testName, extensiontests.ResultFailed, start, time.Now(), stdout, stderr)
	}

	go func() {
		// interrupt after timeout, or exit early if the process finishes first
		select {
		case <-time.After(parentTimeout):
		case <-timeoutCtx.Done():
		}
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGINT)
		}
		// Canceled means the process exited and the context was cancelled — no need to escalate
		if timeoutCtx.Err() == context.Canceled {
			return
		}
		// if the process is hung, send SIGABRT after a grace period for a stack dump
		<-time.After(time.Minute)
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGABRT)
		}
	}()

	cmdErr := command.Wait()
	end := time.Now()

	subcommandResult, parseErr := newTestResultFromOutput(stdout)
	if parseErr == nil {
		// even if we have a cmdErr, if we were able to parse the result, trust the output.
		return handleSubprocessResult(subcommandResult, testName, start, end, stdout, stderr)
	}

	fmt.Fprintf(stderr, "Command Error: %v\n", cmdErr)
	fmt.Fprintf(stderr, "Deserialization Error: %v\n", parseErr)
	return newTestResult(testName, extensiontests.ResultFailed, start, end, stdout, stderr)
}

// handleSubprocessResult validates the result type from a parsed subprocess output.
// If the result type is unknown (e.g. empty string caused by stdout JSON pollution),
// it writes a diagnostic to stderr and falls back to a properly-constructed failure
// result using the original test name and timing.
func handleSubprocessResult(parsed *extensiontests.ExtensionTestResult, testName string, start, end time.Time, stdout, stderr *bytes.Buffer) *extensiontests.ExtensionTestResult {
	switch parsed.Result {
	case extensiontests.ResultPassed, extensiontests.ResultFailed, extensiontests.ResultSkipped:
		return parsed
	default:
		fmt.Fprintf(stderr, "subprocess produced invalid result type %q for test %q; likely JSON pollution in stdout\n", parsed.Result, testName)
		return newTestResult(testName, extensiontests.ResultFailed, start, end, stdout, stderr)
	}
}

func newTestResultFromOutput(stdout *bytes.Buffer) (*extensiontests.ExtensionTestResult, error) {
	if len(stdout.Bytes()) == 0 {
		return nil, errors.New("no output from command")
	}

	jsonData, err := extractJSON(stdout.Bytes())
	if err != nil {
		return nil, err
	}

	// when the command runs correctly, we get json or json slice output
	retArray := []extensiontests.ExtensionTestResult{}
	if arrayItemErr := json.Unmarshal(jsonData, &retArray); arrayItemErr == nil {
		if len(retArray) != 1 {
			return nil, fmt.Errorf("expected 1 result, got %d results", len(retArray))
		}
		return &retArray[0], nil
	}

	// when the command runs correctly, we get json output
	ret := &extensiontests.ExtensionTestResult{}
	if singleItemErr := json.Unmarshal(jsonData, ret); singleItemErr != nil {
		return nil, singleItemErr
	}

	return ret, nil
}

// extractJSON finds the first JSON object or array in output, skipping any non-JSON
// lines that precede it (e.g. klog lines, Ginkgo reporter output). It also ignores
// trailing non-JSON content after the JSON payload. This is necessary because extension
// binaries may emit log output to stdout before or after the JSON result, which would
// otherwise cause deserialization failures.
func extractJSON(output []byte) ([]byte, error) {
	lines := bytes.Split(output, []byte("\n"))
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			// Calculate byte offset to the start of the JSON content
			offset := 0
			for j := 0; j < i; j++ {
				offset += len(lines[j]) + 1 // +1 for the newline
			}

			var raw json.RawMessage
			dec := json.NewDecoder(bytes.NewReader(output[offset:]))
			if err := dec.Decode(&raw); err != nil {
				continue // not valid JSON, try next candidate line
			}
			return raw, nil
		}
	}

	return nil, fmt.Errorf("no JSON object or array found in output (%d bytes)", len(output))
}

func newTestResult(name string, result extensiontests.Result, start, end time.Time, stdout, stderr *bytes.Buffer) *extensiontests.ExtensionTestResult {
	duration := end.Sub(start)
	dbStart := dbtime.DBTime(start)
	dbEnd := dbtime.DBTime(end)
	ret := &extensiontests.ExtensionTestResult{
		Name:      name,
		Lifecycle: "", // lifecycle is completed one level above this.
		Duration:  int64(duration),
		StartTime: &dbStart,
		EndTime:   &dbEnd,
		Result:    result,
		Details:   nil,
	}

	if stdout != nil && stderr != nil {
		stdoutStr := stdout.String()
		stderrStr := stderr.String()

		ret.Output = fmt.Sprintf("STDOUT:\n%s\n\nSTDERR:\n%s\n", stdoutStr, stderrStr)

		// try to choose the best summary
		switch {
		case len(stderrStr) > 0 && len(stderrStr) < 5000:
			ret.Error = stderrStr
		case len(stderrStr) > 0 && len(stderrStr) >= 5000:
			ret.Error = stderrStr[len(stderrStr)-5000:]

		case len(stdoutStr) > 0 && len(stdoutStr) < 5000:
			ret.Error = stdoutStr
		case len(stdoutStr) > 0 && len(stdoutStr) >= 5000:
			ret.Error = stdoutStr[len(stdoutStr)-5000:]
		}
	}

	return ret
}

// mergeEnv validates and appends extra environment variables to a base slice
// (typically os.Environ()). Duplicate keys are not deduplicated — the last value
// wins per exec.Cmd.Env semantics (Go's os/exec uses the last occurrence).
func mergeEnv(base []string, extra map[string]string) ([]string, error) {
	merged := make([]string, len(base), len(base)+len(extra))
	copy(merged, base)
	for k, v := range extra {
		if k == "" {
			return nil, fmt.Errorf("environment variable name must not be empty")
		}
		if strings.ContainsAny(k, "=\x00") {
			return nil, fmt.Errorf("environment variable name %q contains '=' or NUL", k)
		}
		if strings.ContainsRune(v, '\x00') {
			return nil, fmt.Errorf("environment variable %q contains a NUL value", k)
		}
		merged = append(merged, fmt.Sprintf("%s=%s", k, v))
	}
	return merged, nil
}

func envWithSpawnedChildMarker(env map[string]string) map[string]string {
	out := make(map[string]string, len(env)+1)
	maps.Copy(out, env)
	out[extensiontests.SpawnedChildEnv] = "1"
	return out
}
