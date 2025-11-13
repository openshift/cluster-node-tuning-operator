/*
 * Copyright 2026 Red Hat, Inc.
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

package outputparser

import (
	"fmt"
	"strconv"
	"strings"
)

// ParsePSROutput parses multi-line "ps -o psr=" output (one integer per line) and returns all valid CPU IDs.
func ParsePSROutput(output string) ([]int, error) {
	var cpus []int
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cpu, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		cpus = append(cpus, cpu)
	}
	if len(cpus) == 0 {
		return nil, fmt.Errorf("no valid PSR values in output %q", output)
	}
	return cpus, nil
}
