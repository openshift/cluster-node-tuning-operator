package tuned

import (
	"errors"  // errors.Is()
	"fmt"     // Printf()
	"io"      // io.EOF
	"os"      // os.Stat()
	"os/exec" // os.Exec()
	"strings" // strings.Split()
	"syscall" // syscall.SIGHUP, ...
	"time"    // time.Second, ...

	"gopkg.in/ini.v1"
	"k8s.io/klog/v2"
)

// iniFileLoad reads INI file `iniFile` into ini.v1 internal data structures.
// Returns the internal data structures and error if any.
func iniFileLoad(iniFile string) (*ini.File, error) {
	var (
		err error
	)

	content, err := os.ReadFile(iniFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %v", iniFile, err)
	}

	cfg, err := ini.Load(content)
	if err != nil || cfg == nil {
		// This looks like an invalid INI data or parser error.
		return cfg, fmt.Errorf("failed to read INI data: %v", err)
	}

	return cfg, nil
}

// iniFileSave writes INI file `iniFile` from ini.v1 internal data structures `cfg`.
// Returns an error if any.
func iniFileSave(iniFile string, cfg *ini.File) error {
	if cfg == nil {
		return fmt.Errorf("INI file configuration is empty, refusing to write empty file")
	}

	err := cfg.SaveTo(iniFile)
	if err != nil {
		// This looks like we failed to write the INI file for some reason.
		return fmt.Errorf("failed to write INI file: %v", err)
	}

	return nil
}

// iniCfgSetKey sets value `value` to key `key` ini.v1 internal data structures
// `cfg` that represent an INI file.  Additionally, conversion for boolean types
// true -> "1" and false -> "0" is done.  Returns an error if any.
func iniCfgSetKey(cfg *ini.File, key string, value interface{}) error {
	if cfg == nil {
		return fmt.Errorf("unable to set %v=%v, INI file configuration is not initialized", key, value)
	}

	if !cfg.Section("").HasKey(key) {
		return fmt.Errorf("global TuneD configuration has no key %q", key)
	}

	b := func(value interface{}) string {
		switch val := value.(type) {
		case bool:
			if val {
				return "1"
			}
			return "0"
		default:
			return fmt.Sprintf("%v", value)
		}
	}

	cfg.Section("").Key(key).SetValue(b(value))

	return nil
}

// getIniFileSectionSlice searches INI file `data` inside [`section`]
// for key `key`.  It takes the key's value and uses separator
// `separator` to return a slice of strings.
func getIniFileSectionSlice(data *string, section, key, separator string) []string {
	var ret []string

	if data == nil {
		return ret
	}

	cfg, err := ini.Load([]byte(*data))
	if err != nil || cfg == nil {
		// This looks like an invalid INI data or parser error.
		klog.Errorf("unable to read INI file data: %v", err)
		return ret
	}

	if !cfg.Section(section).HasKey(key) {
		return ret
	}

	ret = strings.Split(cfg.Section(section).Key(key).String(), separator)

	return ret
}

// profileIncludesRaw returns a slice of strings containing TuneD profile names
// profile <tunedProfilesDir>/<profileName> includes.  The profile names may
// contain built-in functions that still need to be expanded.
func profileIncludesRaw(profileName string, tunedProfilesDir string) []string {
	profileFile := fmt.Sprintf("%s/%s/%s", tunedProfilesDir, profileName, tunedConfFile)

	content, err := os.ReadFile(profileFile)
	if err != nil {
		content = []byte{}
	}

	s := string(content)

	return getIniFileSectionSlice(&s, "main", "include", ",")
}

// profileIncludes returns a slice of strings containing TuneD profile names
// profile 'profileName' includes.  Expansion of TuneD built-in functions is
// performed on the included profile names and optional loading characters
// ('-') removed.  Only custom <tunedProfilesDirCustom>/<profileName> and/or
// system <tunedProfilesDirSystem>/<profileName> TuneD profiles are scanned.
// For a full list of profiles included from other profiles 'profileName'
// depends on use profileDepends function.
func profileIncludes(profileName string) []string {
	var (
		custom   bool
		system   bool
		profiles []string
		expanded []string
	)

	if profileExists(profileName, tunedProfilesDirCustom) {
		custom = true
		profiles = profileIncludesRaw(profileName, tunedProfilesDirCustom)
	} else {
		profiles = profileIncludesRaw(profileName, tunedProfilesDirSystem)
	}

	realProfileName := func(profile string) string {
		if profile[0:1] == "-" {
			// Conditional profile loading, strip the '-' from profile name.
			profile = profile[1:]
		}

		return expandTuneDBuiltin(profile)
	}

	for _, profile := range profiles {
		p := realProfileName(profile)
		if profileName == p && custom {
			// Custom profile 'profileName' includes profile of the same name.
			// We need to get included profiles from system profile 'profileName' too.
			system = true
		}
		expanded = append(expanded, p)
	}

	if !system {
		return expanded
	}

	for _, profile := range profileIncludesRaw(profileName, tunedProfilesDirSystem) {
		p := realProfileName(profile)
		expanded = append(expanded, p)
	}

	return expanded
}

// profileExists returns true if TuneD profile <tunedProfilesDir>/<profileName> exists.
func profileExists(profileName string, tunedProfilesDir string) bool {
	profileFile := fmt.Sprintf("%s/%s/%s", tunedProfilesDir, profileName, tunedConfFile)

	_, err := os.Stat(profileFile)
	return !errors.Is(err, os.ErrNotExist)
}

// profileDepends returns "TuneD profile name"->bool map that custom
// (/etc/tuned/<profileName>/) profile 'profileName' depends on as keys.
// The dependency is resolved by finding all the "parent" profiles which are
// included by using the "include" keyword in the profile's [main] section.
// Note: only basic expansion of TuneD built-in functions into profiles is
// performed.  See expandTuneDBuiltin for more detail.
func profileDepends(profileName string) map[string]bool {
	return profileDependsLoop(profileName, map[string]bool{})
}

func profileDependsLoop(profileName string, seenProfiles map[string]bool) map[string]bool {
	profiles := profileIncludes(profileName)
	for _, profile := range profiles {
		if seenProfiles[profile] {
			// We have already seen/processed custom profile 'p'.
			continue
		}
		seenProfiles[profile] = true
		seenProfiles = profileDependsLoop(profile, seenProfiles)
	}
	return seenProfiles
}

// execCmd starts command 'command' and waits for it to complete.
// Optional arguments for the command start at command[1].
// If the command does not exit within 'waitSeconds' seconds, SIGTERM
// is sent to the underlying process.  SIGKILL is sent 'waitSeconds'
// seconds after if the process still refuses to exit.
// Returns standard output of the command and an associated error.
func execCmd(command []string) (string, error) {
	const (
		chunkSize   = 4 * 1024
		waitSeconds = 5
	)
	var (
		err error
		out string
		cmd *exec.Cmd
	)

	chSupervisor := make(chan bool, 1)
	chReader := make(chan bool, 1)
	defer func() {
		close(chSupervisor)
	}()

	switch len(command) {
	case 0:
		return "", fmt.Errorf("called execCmd() with undefined command")
	case 1:
		cmd = exec.Command(command[0])
	default:
		cmd = exec.Command(command[0], command[1:]...)
	}

	cmdReader, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("error creating StdoutPipe for command %v: %v\n", command, err)
	}

	go func(chReader chan bool) {
		buf := make([]byte, 0, chunkSize)
		for {
			n, err := cmdReader.Read(buf[:cap(buf)])
			buf = buf[:n]
			if n == 0 {
				if err == nil {
					continue
				}
				if err == io.EOF {
					break
				}
				// This should never happen
				klog.Errorf("error reading from pipe: %v", err)
			}

			out += string(buf)
		}
		chReader <- true
	}(chReader)

	// Supervisor goroutine with process timeout functionality.
	go func(chSupervisor chan bool) {
		select {
		case <-chSupervisor:
			return
		case <-time.After(time.Second * waitSeconds):
			if cmd.Process == nil {
				// Process doesn't seem to exist any longer.
				return
			}
			// Ask nicely first.
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				klog.Errorf("failed to signal process: %v", err)
			}
		}
		// Wait for the process to stop.
		select {
		case <-chSupervisor:
		case <-time.After(time.Second * waitSeconds):
			// The process refused to terminate gracefully on SIGTERM.
			klog.V(1).Infof("sending SIGKILL to PID %d", cmd.Process.Pid)
			if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
				klog.Errorf("failed to signal process: %v", err)
			}
			return
		}
	}(chSupervisor)

	if err = cmd.Start(); err != nil {
		return out, fmt.Errorf("error starting command %v: %v\n", command, err)
	}

	<-chReader // Wait for the reader to prevent missing (part of) its output.  Do not move after cmd.Wait()!
	if err = cmd.Wait(); err != nil {
		// The command exited with non 0 exit status, e.g. terminated by a signal.
		return out, fmt.Errorf("error waiting for command %v: %v\n", command, err)
	}

	return out, nil
}

// execTuneDBuiltin executes TuneD built-in function 'function' with
// arguments 'args'.  Returns the result/expansion of running the built-in.
// If the execution of the built-in fails, returns the string 'onFail'.
func execTuneDBuiltin(function string, args []string, onFail string) string {
	switch {
	case function == "exec":
		out, err := execCmd(args)
		if err != nil {
			klog.Errorf("error calling built-in exec: %v", err)
			return onFail
		}
		return out

	// The virt-what script needed by "virt_check" must be run as root user.
	// Exclude it from unit testing.
	case function == "virt_check":
		// Check whether running inside virtual machine (VM) or on bare metal.
		// If running inside a VM expand to argument 1, otherwise expand to
		// argument 2.  Note the expansion to argument 2 is done also on error
		// to match the semantics of the TuneD "virt_check".
		if len(args) != 2 {
			klog.Errorf("built-in \"virt_check\" requires 2 arguments")
			return onFail
		}
		out, err := execCmd([]string{"virt-what"})
		if err == nil && len(out) > 0 {
			return args[0]
		}
		if err != nil {
			klog.Errorf("failure calling built-in exec: %v", err)
		}

		return args[1]

	default:
		klog.Errorf("calling unsupported built-in: %v", function)
		// unsupported built-in
	}

	return onFail
}

// expandTuneDBuiltin is a naive and incomplete parser of TuneD built-in
// functions in the form ${f:function(:argN)*}.  A typical use case is
// evaluating ${f:virt_check:profile-a:profile-b} and ${f:exec(:argN)}
// TuneD built-in functions in "include" statements.  If (parts of)
// the expansion fail, the function returns the original string for the
// parts that failed the expansion.
func expandTuneDBuiltin(s string) string {
	const (
		sInit   = 0
		sFnName = 1 // ${f:name
		sFnArg  = 2 // ${f:name:arg
	)
	var (
		esc              bool
		ret, function    string
		iDollar, bracket int
		arguments        []string
	)

	byteAtIs := func(i int, b byte) bool {
		if i < 0 || i >= len(s) {
			return false
		}
		return s[i] == b
	}

	state := sInit
	for i := 0; i < len(s); i++ {
		esc = byteAtIs(i-1, '\\')

		switch {
		case state == sFnName:
			// ${f:name
			if s[i] == '$' && byteAtIs(i+1, '{') && !esc {
				bracket++
				function += "${"
				i++
				continue
			}
			if s[i] == '}' && !esc {
				bracket--
				if bracket >= 1 {
					// We do not have enough closing brackets for expansion,
					// the nesting is still too deep.
					goto append_fn
				}
				// Assert: bracket == 0 && no arguments.

				// End of a function.  We now have a function name without arguments.
				// Function name possibly needs expanding.
				functionExpanded := expandTuneDBuiltin(function)
				ret += execTuneDBuiltin(functionExpanded, arguments, s[iDollar:i+1])
				goto init
			}
			if s[i] == ':' && !esc && bracket == 1 {
				state = sFnArg
				arguments = append(arguments, "") // add the first argument
				continue
			}
		append_fn:
			function += string(s[i])
		case state == sFnArg:
			// ${f:name:arg0[:argN]
			if s[i] == '$' && byteAtIs(i+1, '{') && !esc {
				bracket++
				arguments[len(arguments)-1] += "${"
				i++
				continue
			}

			if s[i] == ':' && !esc && bracket == 1 {
				arguments = append(arguments, "") // add additional argument
				continue
			}

			if s[i] == '}' && !esc {
				bracket--
				if bracket >= 1 {
					// We do not have enough closing brackets for expansion,
					// the nesting is still too deep.
					goto append_arg
				}
				// Assert: bracket == 0.

				// End of a function.  We have a function name and arguments
				// that possibly also need expanding.
				for j := 0; j < len(arguments); j++ {
					arguments[j] = expandTuneDBuiltin(arguments[j])
				}
				functionExpanded := expandTuneDBuiltin(function)
				ret += execTuneDBuiltin(functionExpanded, arguments, s[iDollar:i+1])
				goto init
			}
		append_arg:
			arguments[len(arguments)-1] += string(s[i])
		case s[i] == '$' && byteAtIs(i+1, '{') && byteAtIs(i+2, 'f') && byteAtIs(i+3, ':') && !esc:
			iDollar = i
			i += 3
			bracket++
			state = sFnName
		default:
			if state == sInit {
				ret += string(s[i])
				continue
			}
			ret += s[iDollar : i+1]
			goto init
		}
		continue

	init:
		state = sInit
		function = ""
		arguments = []string{}
	}
	if state != sInit {
		// Unterminated function sequence such as "${f:exec"
		ret += s[iDollar:]
	}

	return ret
}
