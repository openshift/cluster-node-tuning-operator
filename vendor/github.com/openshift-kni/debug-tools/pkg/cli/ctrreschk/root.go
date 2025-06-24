/*
 * Copyright 2024 Red Hat, Inc.
 *
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
 */

package ctrreschk

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-logr/stdr"
	"github.com/spf13/cobra"

	"github.com/openshift-kni/debug-tools/pkg/environ"
)

type Options struct {
	Verbose      int
	SleepForever bool
}

func ShowHelp(cmd *cobra.Command, args []string) error {
	fmt.Fprint(cmd.OutOrStderr(), cmd.UsageString())
	return nil
}

type NewCommandFunc func(ko *Options) *cobra.Command

// NewRootCommand returns entrypoint command to interact with all other commands
func NewRootCommand(env *environ.Environ, extraCmds ...NewCommandFunc) *cobra.Command {
	opts := Options{}

	root := &cobra.Command{
		Use:   "ctrreschk",
		Short: "ctrreschk inspects the resources actually allocated to a container",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// this MUST be the first thing we do
			stdr.SetVerbosity(opts.Verbose)
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return ShowHelp(cmd, args)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().BoolVar(&opts.SleepForever, "sleepforever", false, "run and sleep forever after executing the command")
	root.PersistentFlags().IntVar(&opts.Verbose, "verbose", 0, "log verbosity")

	root.AddCommand(
		NewAlignCommand(env, &opts),
		NewInfoCommand(env, &opts),
		NewK8SCommand(env, &opts),
	)
	for _, extraCmd := range extraCmds {
		root.AddCommand(extraCmd(&opts))
	}

	return root
}

func MainLoop(opts *Options) error {
	if !opts.SleepForever {
		return nil
	}
	exitSignal := make(chan os.Signal, 1)
	signal.Notify(exitSignal, syscall.SIGINT, syscall.SIGTERM)
	<-exitSignal
	return nil
}
