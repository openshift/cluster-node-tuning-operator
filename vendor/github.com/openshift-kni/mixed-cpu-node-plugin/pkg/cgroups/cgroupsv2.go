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
)

type v2Adapter struct{}

func (v2 *v2Adapter) absoluteCgroupPath(processCgroupPath string) (string, error) {
	var err error
	processCgroupPath, err = expandSlice(processCgroupPath)
	if err != nil {
		return "", fmt.Errorf("%q: systemd failed to expand slice: %w", cgroupv2UnifiedMode, err)
	}
	return filepath.Join(cgroupMountPoint, processCgroupPath), nil
}

func (v2 *v2Adapter) cfsQuotaPath(processCgroupPath string) (string, error) {
	absolutePath, err := v2.absoluteCgroupPath(processCgroupPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(absolutePath, "cpu.max"), nil
}

func (v2 *v2Adapter) crioContainerCFSQuotaPath(parentPath, ctrId string) (string, error) {
	absoluteParentPath, err := v2.absoluteCgroupPath(parentPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(absoluteParentPath, crioPrefix+"-"+ctrId+".scope", "cpu.max"), nil
}
