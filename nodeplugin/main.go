/*
 * Copyright 2023 Red Hat, Inc.
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

package main

import (
	"context"
	"flag"

	"github.com/golang/glog"
	"github.com/kubevirt/device-plugin-manager/pkg/dpm"

	"github.com/openshift-kni/mixed-cpu-node-plugin/pkg/deviceplugin"
	"github.com/openshift-kni/mixed-cpu-node-plugin/pkg/nriplugin"
)

func main() {
	args := parseArgs()
	p, err := nriplugin.New(args)
	if err != nil {
		glog.Fatalf("%v", err)
	}

	dp, err := deviceplugin.New(args.MutualCPUs)
	if err != nil {
		glog.Fatalf("%v", err)
	}

	execute(p, dp)
}

func parseArgs() *nriplugin.Args {
	args := &nriplugin.Args{}
	flag.StringVar(&args.PluginName, "name", "", "plugin name to register to NRI")
	flag.StringVar(&args.PluginIdx, "idx", "", "plugin index to register to NRI")
	flag.StringVar(&args.MutualCPUs, "mutual-cpus", "", "mutual cpus list")
	flag.Parse()
	return args
}

func execute(p *nriplugin.Plugin, dp *dpm.Manager) {
	go func() {
		err := p.Stub.Run(context.Background())
		if err != nil {
			glog.Fatalf("plugin exited with error %v", err)
		}
	}()
	dp.Run()
}
