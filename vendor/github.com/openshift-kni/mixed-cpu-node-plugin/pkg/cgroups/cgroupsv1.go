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
	"path/filepath"

	"github.com/opencontainers/runc/libcontainer/cgroups"
)

type v1Adapter struct{}

func (v1 *v1Adapter) absoluteCgroupPath(processCgroupPath string) (string, error) {
	cpuMountPoint, err := cgroups.FindCgroupMountpoint(cgroupMountPoint, "cpu")
	if err != nil {
		return "", fmt.Errorf("%q: failed to find cgroup mount point: %w", cgroupv1, err)
	}
	processCgroupPath, err = expandSlice(processCgroupPath)
	if err != nil {
		return "", fmt.Errorf("%q: systemd failed to expand slice: %w", cgroupv1, err)
	}
	return filepath.Join(cpuMountPoint, processCgroupPath), nil
}

func (v1 *v1Adapter) cfsQuotaPath(processCgroupPath string) (string, error) {
	absolutePath, err := v1.absoluteCgroupPath(processCgroupPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(absolutePath, "cpu.cfs_quota_us"), nil
}

func (v1 *v1Adapter) crioContainerCFSQuotaPath(parentPath, ctrId string) (string, error) {
	parentAbsolutePath, err := v1.absoluteCgroupPath(parentPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(parentAbsolutePath, crioPrefix+"-"+ctrId+".scope", "cpu.cfs_quota_us"), nil
}
