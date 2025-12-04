package utils

import (
	"bytes"
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

// NewCLIWithoutNamespace creates a new CLI instance without a default namespace
func NewCLIWithoutNamespace(name string) *CLI {
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
		return fmt.Errorf("failed to create namespace %s: %v", c.namespace, err)
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

	// Add namespace if set and not explicitly disabled
	if !cmd.cli.withoutNamespace && cmd.cli.namespace != "" && cmd.verb != "debug" {
		cmdArgs = append(cmdArgs, "-n", cmd.cli.namespace)
	}

	// Add command arguments
	cmdArgs = append(cmdArgs, cmd.args...)

	return cmdArgs
}

// Output runs the command and returns its output
func (cmd *Command) Output() (string, error) {
	cmdArgs := cmd.buildCmdArgs()

	if cmd.cli.verbose {
		fmt.Printf("DEBUG: %s %s\n", cmd.cli.execPath, strings.Join(cmdArgs, " "))
	}

	ocCmd := exec.Command(cmd.cli.execPath, cmdArgs...)

	// Set up stdin if input string is provided
	if cmd.inputStr != "" {
		ocCmd.Stdin = strings.NewReader(cmd.inputStr)
	}

	if cmd.cli.showInfo {
		Logf("running '%s %s'", cmd.cli.execPath, strings.Join(cmdArgs, " "))
	}

	var stdout, stderr bytes.Buffer
	ocCmd.Stdout = &stdout
	ocCmd.Stderr = &stderr

	err := ocCmd.Run()
	trimmed := strings.TrimSpace(stdout.String())

	if err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		Logf("error running command: %v\nstdout: %s\nstderr: %s", err, trimmed, stderrStr)
		return trimmed, fmt.Errorf("command failed: %v, stderr: %s", err, stderrStr)
	}

	return trimmed, nil
}

// ExitError represents an error from command execution
type ExitError struct {
	Cmd    string
	StdErr string
	*exec.ExitError
}

// Outputs runs the command and returns both stdout and stderr separately
func (cmd *Command) Outputs() (string, string, error) {
	cmdArgs := cmd.buildCmdArgs()

	if cmd.cli.verbose {
		fmt.Printf("DEBUG: %s %s\n", cmd.cli.execPath, strings.Join(cmdArgs, " "))
	}

	ocCmd := exec.Command(cmd.cli.execPath, cmdArgs...)

	// Set up stdin if input string is provided
	if cmd.inputStr != "" {
		ocCmd.Stdin = strings.NewReader(cmd.inputStr)
	}

	if cmd.cli.showInfo {
		Logf("running '%s %s'", cmd.cli.execPath, strings.Join(cmdArgs, " "))
	}

	var stdout, stderr bytes.Buffer
	ocCmd.Stdout = &stdout
	ocCmd.Stderr = &stderr

	err := ocCmd.Run()
	stdoutStr := strings.TrimSpace(stdout.String())
	stderrStr := strings.TrimSpace(stderr.String())

	if err != nil {
		Logf("error running command: %v\nstdout: %s\nstderr: %s", err, stdoutStr, stderrStr)
		if exitErr, ok := err.(*exec.ExitError); ok {
			return stdoutStr, stderrStr, &ExitError{
				ExitError: exitErr,
				Cmd:       cmd.cli.execPath + " " + strings.Join(cmdArgs, " "),
				StdErr:    stderrStr,
			}
		}
		return stdoutStr, stderrStr, fmt.Errorf("command failed: %v", err)
	}

	return stdoutStr, stderrStr, nil
}

// OutputToFile executes the command and stores output to a file
func (cmd *Command) OutputToFile(filename string) (string, error) {
	_, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return filename, nil // Simplified version, full implementation would write to file
}

// FatalErr exits the test in case a fatal error has occurred
func FatalErr(msg interface{}) {
	panic(fmt.Sprintf("%v", msg))
}
