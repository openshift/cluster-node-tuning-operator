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

package resources

import (
	"k8s.io/utils/cpuset"

	"github.com/openshift-kni/debug-tools/pkg/cgroups"
	"github.com/openshift-kni/debug-tools/pkg/environ"
)

type Resources struct {
	CPUs cpuset.CPUSet
}

func Discover(env *environ.Environ) (Resources, error) {
	cpus, err := cgroups.Cpuset(env)
	if err != nil {
		return Resources{}, err
	}
	env.Log.V(2).Info("detected resources", "cpus", cpus)
	return Resources{
		CPUs: cpus,
	}, nil
}
