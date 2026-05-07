// cli_wrapper.go wraps the oc command-line tool to provide a simple interface
// for test operations. It defines the CLI and Command structs with builder-pattern
// methods for constructing and executing oc commands.

package utils

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CLI wraps the oc command-line tool to provide a simple interface for test operations
type CLI struct {
	execPath         string
	namespace        string
	asAdmin          bool
	guestKubeconfig  string
	adminKubeconfig  string
	configPath       string
	useGuestConfig   bool
	withoutNamespace bool
	withoutKubeconf  bool
	showInfo         bool
	verbose          bool
}

// defaultCommandTimeout is the maximum duration an oc command is allowed to run.
const defaultCommandTimeout = 5 * time.Minute

// NewCLIWithoutNamespace creates a new CLI instance without a default namespace
func NewCLIWithoutNamespace() *CLI {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("HOME") + "/.kube/config"
	}
	return &CLI{
		namespace:       "",
		asAdmin:         false,
		execPath:        "oc",
		showInfo:        true,
		adminKubeconfig: kubeconfig,
		configPath:      kubeconfig,
	}
}

// AsAdmin returns a CLI that will run commands with admin privileges
func (c *CLI) AsAdmin() *CLI {
	newCLI := &CLI{
		namespace:        c.namespace,
		asAdmin:          true,
		guestKubeconfig:  c.guestKubeconfig,
		adminKubeconfig:  c.adminKubeconfig,
		configPath:       c.adminKubeconfig,
		useGuestConfig:   c.useGuestConfig,
		withoutNamespace: c.withoutNamespace,
		withoutKubeconf:  c.withoutKubeconf,
		execPath:         c.execPath,
		showInfo:         c.showInfo,
		verbose:          c.verbose,
	}
	return newCLI
}

// WithoutNamespace returns a CLI that won't use a namespace flag
func (c *CLI) WithoutNamespace() *CLI {
	newCLI := &CLI{
		namespace:        "",
		asAdmin:          c.asAdmin,
		guestKubeconfig:  c.guestKubeconfig,
		adminKubeconfig:  c.adminKubeconfig,
		configPath:       c.configPath,
		useGuestConfig:   c.useGuestConfig,
		withoutNamespace: true,
		withoutKubeconf:  c.withoutKubeconf,
		execPath:         c.execPath,
		showInfo:         c.showInfo,
		verbose:          c.verbose,
	}
	return newCLI
}

// WithoutKubeconf instructs the command should be invoked without adding --kubeconfig parameter
func (c *CLI) WithoutKubeconf() *CLI {
	newCLI := &CLI{
		namespace:        c.namespace,
		asAdmin:          c.asAdmin,
		guestKubeconfig:  c.guestKubeconfig,
		adminKubeconfig:  c.adminKubeconfig,
		configPath:       c.configPath,
		useGuestConfig:   c.useGuestConfig,
		withoutNamespace: c.withoutNamespace,
		withoutKubeconf:  true,
		execPath:         c.execPath,
		showInfo:         c.showInfo,
		verbose:          c.verbose,
	}
	return newCLI
}

// SetGuestKubeconf sets the guest kubeconfig path for hosted cluster operations
func (c *CLI) SetGuestKubeconf(kubeconfigPath string) *CLI {
	c.guestKubeconfig = kubeconfigPath
	return c
}

// GetGuestKubeconf gets the guest cluster kubeconfig file
func (c *CLI) GetGuestKubeconf() string {
	return c.guestKubeconfig
}

// SetAdminKubeconf sets the admin kubeconfig path
func (c *CLI) SetAdminKubeconf(kubeconfigPath string) *CLI {
	c.adminKubeconfig = kubeconfigPath
	if c.asAdmin {
		c.configPath = kubeconfigPath
	}
	return c
}

// SetKubeconf sets the kubeconfig path
func (c *CLI) SetKubeconf(kubeconfigPath string) *CLI {
	c.configPath = kubeconfigPath
	return c
}

// GetKubeconf gets the current kubeconfig path
func (c *CLI) GetKubeconf() string {
	return c.configPath
}

// AsGuestKubeconf returns a CLI that will use the guest kubeconfig
func (c *CLI) AsGuestKubeconf() *CLI {
	newCLI := &CLI{
		namespace:        c.namespace,
		asAdmin:          c.asAdmin,
		guestKubeconfig:  c.guestKubeconfig,
		adminKubeconfig:  c.adminKubeconfig,
		configPath:       c.configPath,
		useGuestConfig:   true,
		withoutNamespace: true, // Guest cluster operations require explicit namespace
		withoutKubeconf:  c.withoutKubeconf,
		execPath:         c.execPath,
		showInfo:         c.showInfo,
		verbose:          c.verbose,
	}
	return newCLI
}

// SetNamespace sets the namespace for operations
func (c *CLI) SetNamespace(ns string) *CLI {
	c.namespace = ns
	return c
}

// Namespace returns the current namespace
func (c *CLI) Namespace() string {
	return c.namespace
}

// SetupProject creates a temporary namespace for testing
func (c *CLI) SetupProject() error {
	// Generate a unique namespace name using timestamp
	c.namespace = fmt.Sprintf("e2e-nto-test-%d", time.Now().Unix())

	// Create the namespace
	err := c.AsAdmin().WithoutNamespace().Run("create").Args("namespace", c.namespace).Execute()
	if err != nil {
		return fmt.Errorf("failed to create namespace %s: %w", c.namespace, err)
	}

	return nil
}

// TeardownProject deletes the namespace created for testing
func (c *CLI) TeardownProject() {
	if c.namespace != "" {
		_ = c.AsAdmin().WithoutNamespace().Run("delete").Args("namespace", c.namespace, "--ignore-not-found", "--wait=false").Execute()
	}
}

// NotShowInfo instructs the command will not be logged
func (c *CLI) NotShowInfo() *CLI {
	c.showInfo = false
	return c
}

// SetShowInfo instructs the command will be logged
func (c *CLI) SetShowInfo() *CLI {
	c.showInfo = true
	return c
}

// Verbose turns on printing verbose messages when executing OpenShift commands
func (c *CLI) Verbose() *CLI {
	c.verbose = true
	return c
}

// Command represents a command to be executed
type Command struct {
	cli      *CLI
	verb     string
	args     []string
	inputStr string
	timeout  time.Duration
	ctx      context.Context
}

// Run starts building a command
func (c *CLI) Run(verb string) *Command {
	return &Command{
		cli:  c,
		verb: verb,
		args: []string{},
	}
}

// Args adds arguments to the command
func (cmd *Command) Args(args ...string) *Command {
	cmd.args = append(cmd.args, args...)
	return cmd
}

// InputString sets the input string for the command (for stdin)
func (cmd *Command) InputString(input string) *Command {
	cmd.inputStr = input
	return cmd
}

// WithTimeout overrides defaultCommandTimeout for this command invocation. Use it for
// commands that are expected to legitimately run longer than the default (e.g. a long
// `oc wait`/`oc debug` call), so they aren't killed with a generic "context deadline
// exceeded" indistinguishable from a genuine hang.
func (cmd *Command) WithTimeout(timeout time.Duration) *Command {
	cmd.timeout = timeout
	return cmd
}

// WithCtx sets the context for the command. This allows callers to cancel in-flight
// oc commands when the provided context is cancelled (e.g. test context or cleanup
// context). If not set, context.Background() is used.
func (cmd *Command) WithCtx(ctx context.Context) *Command {
	cmd.ctx = ctx
	return cmd
}

// timeoutOrDefault returns the per-call timeout if one was set via WithTimeout,
// otherwise defaultCommandTimeout.
func (cmd *Command) timeoutOrDefault() time.Duration {
	if cmd.timeout > 0 {
		return cmd.timeout
	}
	return defaultCommandTimeout
}

// Execute runs the command and returns an error if it fails
func (cmd *Command) Execute() error {
	_, err := cmd.Output()
	return err
}

// buildCmdArgs constructs the command arguments
func (cmd *Command) buildCmdArgs() []string {
	cmdArgs := []string{}

	// Add kubeconfig if needed
	if !cmd.cli.withoutKubeconf {
		if cmd.cli.useGuestConfig && cmd.cli.guestKubeconfig != "" {
			cmdArgs = append(cmdArgs, "--kubeconfig="+cmd.cli.guestKubeconfig)
		} else if cmd.cli.configPath != "" {
			cmdArgs = append(cmdArgs, "--kubeconfig="+cmd.cli.configPath)
		}
	}

	// Add verb
	cmdArgs = append(cmdArgs, cmd.verb)

	// Add namespace if set and not explicitly disabled. The `debug` verb does not
	// accept `-n` in this position, so skip it to avoid command failures.
	if !cmd.cli.withoutNamespace && cmd.cli.namespace != "" && cmd.verb != "debug" {
		cmdArgs = append(cmdArgs, "-n", cmd.cli.namespace)
	}

	// Add command arguments
	cmdArgs = append(cmdArgs, cmd.args...)

	return cmdArgs
}

// run builds and executes the underlying oc command, applying the shared logging,
// timeout, and stdin/stdout/stderr plumbing used by both Output and Outputs. It
// returns the trimmed stdout/stderr and the raw error from ocCmd.Run() (nil on
// success), along with the resolved command-line args for callers that need them
// (e.g. to build an ExitError). It does not itself log or wrap the error on
// failure — callers are responsible for that, since Output and Outputs differ in
// how they want to report failures.
func (cmd *Command) run() (stdout, stderr string, cmdArgs []string, err error) {
	cmdArgs = cmd.buildCmdArgs()

	if cmd.cli.verbose {
		fmt.Fprintf(os.Stderr, "DEBUG: %s %s\n", cmd.cli.execPath, strings.Join(cmdArgs, " "))
	}

	parentCtx := cmd.ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, cmd.timeoutOrDefault())
	defer cancel()

	ocCmd := exec.CommandContext(ctx, cmd.cli.execPath, cmdArgs...)

	// Set up stdin if input string is provided
	if cmd.inputStr != "" {
		ocCmd.Stdin = strings.NewReader(cmd.inputStr)
	}

	if cmd.cli.showInfo {
		Logf("running '%s %s'", cmd.cli.execPath, strings.Join(cmdArgs, " "))
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	ocCmd.Stdout = &stdoutBuf
	ocCmd.Stderr = &stderrBuf

	err = ocCmd.Run()
	stdout = strings.TrimSpace(stdoutBuf.String())
	stderr = strings.TrimSpace(stderrBuf.String())

	if err != nil {
		Logf("error running command: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	return stdout, stderr, cmdArgs, err
}

// Output runs the command and returns its output
func (cmd *Command) Output() (string, error) {
	stdout, stderr, _, err := cmd.run()
	if err != nil {
		return stdout, fmt.Errorf("command failed: %w, stderr: %s", err, stderr)
	}

	return stdout, nil
}

// ExitError represents an error from command execution
type ExitError struct {
	Cmd    string
	StdErr string
	*exec.ExitError
}

// Outputs runs the command and returns both stdout and stderr separately
func (cmd *Command) Outputs() (string, string, error) {
	stdout, stderr, cmdArgs, err := cmd.run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return stdout, stderr, &ExitError{
				ExitError: exitErr,
				Cmd:       cmd.cli.execPath + " " + strings.Join(cmdArgs, " "),
				StdErr:    stderr,
			}
		}
		return stdout, stderr, fmt.Errorf("command failed: %w", err)
	}

	return stdout, stderr, nil
}

// FollowUntilContains runs a streaming command (e.g. "oc logs -f") and returns true as soon
// as a line of its output contains keyword. It returns false if the command exits or ctx is
// done before the keyword appears. The underlying process is always stopped before returning,
// even if it would otherwise keep streaming forever.
func (cmd *Command) FollowUntilContains(ctx context.Context, keyword string) bool {
	// maxFollowedLineSize is the largest single line bufio.Scanner will accept while reading
	// streamed command output. The default 64 KiB limit is too small for occasionally long
	// container log lines.
	const maxFollowedLineSize = 1024 * 1024

	cmdArgs := cmd.buildCmdArgs()
	if cmd.cli.showInfo {
		Logf("running '%s %s'", cmd.cli.execPath, strings.Join(cmdArgs, " "))
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ocCmd := exec.CommandContext(ctx, cmd.cli.execPath, cmdArgs...)
	// If WaitDelay is non-zero, the command's I/O pipes will be closed after
	// WaitDelay has elapsed after either the command's process has exited or
	// (if Context is non-nil) Context is done, whichever occurs first.
	ocCmd.WaitDelay = 2 * time.Second
	var stderrBuf bytes.Buffer
	ocCmd.Stderr = &stderrBuf

	stdout, err := ocCmd.StdoutPipe()
	if err != nil {
		Logf("FollowUntilContains: failed to create stdout pipe: %v", err)
		return false
	}
	if err := ocCmd.Start(); err != nil {
		Logf("FollowUntilContains: failed to start command: %v", err)
		return false
	}

	// Scan the output on a separate goroutine so that we can stop waiting as soon as either
	// the keyword shows up or ctx is done, without waiting for the command to exit on its own.
	foundCh := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), maxFollowedLineSize)

		found := false
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), keyword) {
				found = true
				break
			}
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
			Logf("FollowUntilContains: error reading command output: %v", err)
		}
		foundCh <- found
	}()

	var found bool
	select {
	case found = <-foundCh:
	case <-ctx.Done():
		// The child may not release its stdout write end promptly (e.g. a subprocess
		// that inherited the descriptor outlives it, or the process is slow to die),
		// so the pipe would never EOF on its own. Close our read end to guarantee the
		// scanner goroutine unblocks and cannot leak; the caller must still wait for it
		// so that Wait() below is not called while stdout is still being read.
		if err := stdout.Close(); err != nil && cmd.cli.verbose {
			Logf("FollowUntilContains: failed to close stdout pipe: %v", err)
		}
		found = <-foundCh // block until the scanner goroutine stops reading stdout
	}

	// Cancel unconditionally: a match may have been found while the command is still
	// streaming, in which case it would otherwise never exit on its own.
	// Wait must only be called once stdout has stopped being read, which is guaranteed
	// here since the scanner goroutine has already returned by this point.
	cancel()
	if err := ocCmd.Wait(); err != nil && cmd.cli.verbose {
		Logf("FollowUntilContains: command exited: %v", err)
	}

	if !found && stderrBuf.Len() > 0 {
		Logf("FollowUntilContains: keyword %q not found; stderr:\n%s", keyword, strings.TrimSpace(stderrBuf.String()))
	}

	return found
}
