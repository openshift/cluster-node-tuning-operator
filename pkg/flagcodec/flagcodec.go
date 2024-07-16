/*
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2021 Red Hat, Inc.
 */

// flagcodec allows to manipulate foreign command lines following the
// standard golang conventions. It offeres two-way processing aka
// parsing/marshaling of flags. It is different from the other, well
// established packages (pflag...) because it aims to manipulate command
// lines in general, not this program command line.

package flagcodec

import (
	"fmt"
	"strings"
)

const (
	FlagToggle = iota
	FlagOption
)

type Val struct {
	Kind int
	Data string
}

type Flags struct {
	command         string
	args            map[string]Val
	keys            []string
	processFlagName func(string) string
}

// ParseArgvKeyValue parses a clean (trimmed) argv whose components
// are either toggles or key=value pairs. IOW, this is a restricted and easier
// to parse flavor of argv on which option and value are guaranteed to
// be in the same item.
// IOW, we expect
// "--opt=foo"
// AND NOT
// "--opt", "foo"
func ParseArgvKeyValue(args []string, opts ...Option) *Flags {
	ret := &Flags{
		command:         "",
		args:            make(map[string]Val),
		processFlagName: func(v string) string { return v },
	}
	for _, opt := range opts {
		opt(ret)
	}
	for _, arg := range args {
		fields := strings.SplitN(arg, "=", 2)
		if len(fields) == 1 {
			ret.SetToggle(fields[0])
			continue
		}
		ret.SetOption(fields[0], fields[1])
	}
	return ret
}

// ParseArgvKeyValueWithCommand parses a clean (trimmed) argv whose components
// are either toggles or key=value pairs. IOW, this is a restricted and easier
// to parse flavor of argv on which option and value are guaranteed to
// be in the same item.
// IOW, we expect
// "--opt=foo"
// AND NOT
// "--opt", "foo"
// The command is supplied explicitly as parameter.
// DEPRECATED: use ParseArgvValue and WithCommand option
func ParseArgvKeyValueWithCommand(command string, args []string) *Flags {
	return ParseArgvKeyValue(args, WithCommand(command))
}

type Option func(*Flags) *Flags

func normalizeFlagName(v string) string {
	if len(v) == 3 && v[0] == '-' && v[1] == '-' {
		// single char, double dash flag (ugly?), fix it
		return v[1:]
	}
	// everything else pass through silently
	return v
}

// WithFlagNormalization optionally enables flag normalization.
// The canonical representation of flags in this package is:
// * single-dash for one-char flags (-v, -h)
// * double-dash for multi-char flags (--foo, --long-option)
// pflag allows one-char to have one or two dashes. For flagcodec
// these were different options. When normalization is enabled,
// though, all flag names are processed to adhere to the canonical
// representation, so flagcodec will treat `--v` and `-v` to
// be the same flag. Since this is possibly breaking change,
// this treatment is opt-in.
func WithFlagNormalization(fl *Flags) *Flags {
	fl.processFlagName = normalizeFlagName
	return fl
}

func WithCommand(command string) Option {
	return func(fl *Flags) *Flags {
		fl.command = command
		return fl
	}
}

func (fl *Flags) recordFlag(name string) {
	if _, ok := fl.args[name]; !ok {
		fl.keys = append(fl.keys, name)
	}
}

func (fl *Flags) forgetFlag(name string) {
	var keys []string
	for _, k := range fl.keys {
		if k == name {
			continue
		}
		keys = append(keys, k)
	}
	fl.keys = keys
}

func (fl *Flags) SetToggle(name string) {
	name = fl.processFlagName(name)
	fl.recordFlag(name)
	fl.args[name] = Val{
		Kind: FlagToggle,
	}
}

func (fl *Flags) SetOption(name, data string) {
	name = fl.processFlagName(name)
	fl.recordFlag(name)
	fl.args[name] = Val{
		Kind: FlagOption,
		Data: data,
	}
}

func (fl *Flags) Delete(name string) {
	name = fl.processFlagName(name)
	fl.forgetFlag(name)
	delete(fl.args, name)
}

func (fl *Flags) Command() string {
	return fl.command
}

func (fl *Flags) Args() []string {
	var args []string
	for _, name := range fl.keys {
		args = append(args, toString(name, fl.args[name]))
	}
	return args
}

func (fl *Flags) Argv() []string {
	args := fl.Args()
	if fl.command == "" {
		return args
	}
	return append([]string{fl.Command()}, args...)
}

func (fl *Flags) GetFlag(name string) (Val, bool) {
	name = fl.processFlagName(name)
	if val, ok := fl.args[name]; ok {
		return val, ok
	}
	return Val{}, false
}

func toString(name string, val Val) string {
	if val.Kind == FlagToggle {
		return name
	}
	return fmt.Sprintf("%s=%s", name, val.Data)
}
