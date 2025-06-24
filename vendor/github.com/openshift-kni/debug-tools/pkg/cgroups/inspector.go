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

package cgroups

import (
	"os"
	"path/filepath"
	"strings"

	"k8s.io/utils/cpuset"

	"github.com/openshift-kni/debug-tools/pkg/environ"
)

const (
	CgroupPath = "fs/cgroup"
	CpusetFile = "cpuset.cpus.effective"
)

func CpusetPath(env *environ.Environ) string {
	return filepath.Join(env.Root.Sys, CgroupPath, CpusetFile)
}

func Cpuset(env *environ.Environ) (cpuset.CPUSet, error) {
	data, err := os.ReadFile(CpusetPath(env))
	if err != nil {
		return cpuset.New(), err
	}
	return cpuset.Parse(strings.TrimSpace(string(data)))
}
