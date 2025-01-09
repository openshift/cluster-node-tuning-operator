/*
 * Copyright 2025 Red Hat, Inc.
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

package components

import (
	"testing"

	apiconfigv1 "github.com/openshift/api/config/v1"
)

func TestMachineConfigOptionsClone(t *testing.T) {
	mode := apiconfigv1.CPUPartitioningAllNodes
	mco := &MachineConfigOptions{
		PinningMode: &mode,
	}

	mode2 := apiconfigv1.CPUPartitioningNone
	mco2 := mco.Clone()
	mco2.PinningMode = &mode2
	mco2.LLCFileEnabled = true

	// verify changes did not propagate back to the original copy
	if *mco.PinningMode != apiconfigv1.CPUPartitioningAllNodes {
		t.Fatalf("mutation of the cloned PinningMode altered back the original")
	}
	if mco.LLCFileEnabled {
		t.Fatalf("mutation of the cloned LLCFileEnabled altered back the original")
	}
}
