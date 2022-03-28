/*
Copyright 2022 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/reporters"
	. "github.com/onsi/gomega"

	ginkgo_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"

	_ "github.com/openshift/cluster-node-tuning-operator/test/e2e/pao/functests/0_config"
	_ "github.com/openshift/cluster-node-tuning-operator/test/e2e/pao/functests/1_performance"
	_ "github.com/openshift/cluster-node-tuning-operator/test/e2e/pao/functests/2_performance_update"
	_ "github.com/openshift/cluster-node-tuning-operator/test/e2e/pao/functests/3_performance_status"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)

	rr := []Reporter{}
	if ginkgo_reporters.Polarion.Run {
		rr = append(rr, &ginkgo_reporters.Polarion)
	}
	rr = append(rr, reporters.NewJUnitReporter("performanceprofile"))
	RunSpecsWithDefaultAndCustomReporters(t, "Performance Profile Functional tests", rr)
}
