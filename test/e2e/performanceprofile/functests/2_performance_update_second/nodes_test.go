package __2_performance_update_second

import (
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
)

var _ = Describe("Node Utils", func() {
	Context("CPU Tools", func() {
		table.DescribeTable("GetRanges should return valid CPU/NUMA ranges", func(x, y string, expected bool) {
			Expect(x == y).Should(Equal(expected))
		},
			table.Entry("x == y", nodes.GetNumaRanges("42,3,43,4,44,5,45,6,47,7"), "3-7,42-45,47", true),
			table.Entry("x == y", nodes.GetNumaRanges("10,11,12,13,14,15,16,19,20,21,22,23,24,32"), "10-16,19-24,32", true),
			table.Entry("x == y", nodes.GetNumaRanges("10,31,11,32,12,33,13,34,14,35"), "10-14,31,32,33,34,35", true),
			table.Entry("x == y", nodes.GetNumaRanges("1,2,3,4,9,10,11,12,32"), "1-4,9-12,32", true),
		)
	})
})
