/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tuned

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"k8s.io/klog/v2"
)

func TunedCreateCmdline(debug bool) (string, []string) {
	args := []string{"--no-dbus"}
	if debug {
		args = append(args, "--debug")
	}
	return "/usr/sbin/tuned", args
}

func configDaemonMode() (func(), error) {
	daemon_key := "daemon"

	tunedMainCfg, err := iniFileLoad(tunedMainConfPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read global TuneD configuration file: %w", err)
	}

	daemon_value := tunedMainCfg.Section("").Key(daemon_key).MustBool()

	err = iniCfgSetKey(tunedMainCfg, daemon_key, false)
	if err != nil {
		return nil, err
	}
	err = iniFileSave(tunedMainConfPath, tunedMainCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to write global TuneD configuration file: %w", err)
	}

	restoreF := func() {
		tunedMainCfg, err := iniFileLoad(tunedMainConfPath)
		if err != nil {
			klog.Warningf("failed to read global TuneD configuration file: %v", err)
			return
		}
		err = iniCfgSetKey(tunedMainCfg, daemon_key, daemon_value)
		if err != nil {
			klog.Warningf("failed to set %s key to %v value: %v", daemon_key, daemon_value, err)
			return
		}
		err = iniFileSave(tunedMainConfPath, tunedMainCfg)
		if err != nil {
			klog.Warningf("failed to write global TuneD configuration file: %w", err)
		}
	}

	return restoreF, nil
}

func TunedRunNoDaemon(timeout time.Duration) error {
	var (
		cmd    *exec.Cmd
		daemon Daemon
	)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	command, args := TunedCreateCmdline(false)
	if timeout > 0 {
		// CommandContext sets Cancel to call the Kill (SIGKILL) method on the command's Process.
		cmd = exec.CommandContext(ctx, command, args...)
	} else {
		cmd = exec.Command(command, args...)
	}

	restoreFunction, err := configDaemonMode()
	if err != nil {
		return err
	}
	defer restoreFunction()

	onDaemonReload := func() {}
	return TunedRun(cmd, &daemon, onDaemonReload)
}

func TunedRun(cmd *exec.Cmd, daemon *Daemon, onDaemonReload func()) error {
	klog.Infof("running cmd...")

	cmdReader, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("error creating StderrPipe for tuned: %w", err)
	}

	scanner := bufio.NewScanner(cmdReader)
	go func() {
		for scanner.Scan() {
			l := scanner.Text()

			fmt.Printf("%s\n", l)

			if daemon.stopping {
				// We have decided to stop TuneD.  Apart from showing the logs it is
				// now unnecessary/undesirable to perform any of the following actions.
				// The undesirability comes from extra processing which will come if
				// TuneD manages to "get unstuck" during this phase before it receives
				// SIGKILL (note the time window between SIGTERM/SIGKILL).
				continue
			}

			profileApplied := strings.Contains(l, " tuned.daemon.daemon: static tuning from profile ") && strings.Contains(l, " applied")
			reloadFailed := strings.Contains(l, " tuned.daemon.controller: Failed to reload TuneD: ")
			profileDoesNotExist := strings.Contains(l, " tuned.daemon.daemon: Cannot set initial profile. No tunings will be enabled: Requested profile ") &&
				strings.Contains(l, " doesn't exist.")

			if profileApplied {
				daemon.status |= scApplied
			}

			strIndex := strings.Index(l, " WARNING ")
			if strIndex >= 0 {
				daemon.status |= scWarn
				prevError := ((daemon.status & scError) != 0)
				if !prevError { // don't overwrite an error message
					daemon.stderr = l[strIndex:] // trim timestamp from log
				}
			}

			strIndex = strings.Index(l, " ERROR ")
			if strIndex >= 0 {
				daemon.status |= scError
				daemon.stderr = l[strIndex:] // trim timestamp from log
			}

			sysctl := overridenSysctl(l)
			if sysctl != "" {
				daemon.status |= scSysctlOverride
				daemon.stderr = sysctl
			}

			if (daemon.status & scReloading) != 0 {
				// TuneD daemon status flags are set as reloading, see if this has changed based on the log line.
				reloaded := profileApplied || reloadFailed || profileDoesNotExist
				if reloaded {
					// TuneD has finished reloading.
					daemon.status &= ^scReloading // clear the scReloading status bit
					onDaemonReload()
				}
			}
		}
	}()

	// Clear the set out of which Profile status conditions are created.
	daemon.status = 0
	daemon.status |= scReloading
	daemon.stderr = ""
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("error starting tuned: %w", err)
	}

	if err = cmd.Wait(); err != nil {
		// The command exited with non 0 exit status, e.g. terminated by a signal.
		return fmt.Errorf("error waiting for tuned: %w", err)
	}

	return nil
}
