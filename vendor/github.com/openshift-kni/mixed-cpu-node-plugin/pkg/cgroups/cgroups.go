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

package cgroups

import (
	"fmt"
	"strings"

	"github.com/opencontainers/runc/libcontainer/cgroups"
	"github.com/opencontainers/runc/libcontainer/cgroups/systemd"
)

const (
	crioPrefix       = "crio"
	cgroupMountPoint = "/sys/fs/cgroup"
)

type Mode string

const (
	cgroupv1            = "cgroupv1"
	cgroupv2UnifiedMode = "cgroupv2UnifiedMode"
)

// Adapter is a global variable that convert the different cgroups version APIs
// into a single API that can be used by the user code
// the adapter is global since two different cgroups version cannot co-exist on the same system.
var Adapter adapter

func init() {
	if cgroups.IsCgroup2UnifiedMode() {
		Adapter = adapter{
			ai:   &v2Adapter{},
			mode: cgroupv2UnifiedMode,
		}
	} else {
		Adapter = adapter{
			ai:   &v1Adapter{},
			mode: cgroupv1,
		}
	}
}

type adapter struct {
	mode Mode
	ai   adapterInterface
}

func (a *adapter) GetMode() Mode {
	return a.mode
}

func (a *adapter) GetCFSQuotaPath(processCgroupPath string) (string, error) {
	return a.ai.cfsQuotaPath(processCgroupPath)
}

func (a *adapter) GetCrioContainerCFSQuotaPath(parentPath, ctrId string) (string, error) {
	return a.ai.crioContainerCFSQuotaPath(parentPath, ctrId)
}

type adapterInterface interface {
	cfsQuotaPath(processCgroupPath string) (string, error)
	crioContainerCFSQuotaPath(parentPath, ctrId string) (string, error)
}

func expandSlice(path string) (string, error) {
	// systemd fs, otherwise cgroupfs
	if strings.HasSuffix(path, ".slice") {
		return systemd.ExpandSlice(path)
	}
	// TODO implement for cgroupfs)
	return "", fmt.Errorf("cgroupfs not implemented")
}
