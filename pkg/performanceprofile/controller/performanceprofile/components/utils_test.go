package components

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
)

type listToMask struct {
	cpuList string
	cpuMask string
}

var cpuListToMask = []listToMask{
	{"0", "00000001"},
	{"2-3", "0000000c"},
	{"3,4,53-55,61-63", "e0e00000,00000018"},
	{"0-127", "ffffffff,ffffffff,ffffffff,ffffffff"},
	{"0-255", "ffffffff,ffffffff,ffffffff,ffffffff,ffffffff,ffffffff,ffffffff,ffffffff"},
}

func intersectHelper(cpuListA, cpuListB string) ([]int, error) {
	cpuLists, err := NewCPULists(cpuListA, cpuListB)
	if err != nil {
		return nil, err
	}
	return cpuLists.Intersect(), nil
}

var _ = Describe("Components utils", func() {
	Context("Convert CPU list to CPU mask", func() {
		It("should generate a valid CPU mask from CPU list ", func() {
			for _, cpuEntry := range cpuListToMask {
				cpuMask, err := CPUListToMaskList(cpuEntry.cpuList)
				Expect(err).ToNot(HaveOccurred())
				Expect(cpuMask).Should(Equal(cpuEntry.cpuMask))
			}
		})
	})

	Context("Convert CPU mask to CPU list", func() {
		It("should generate a valid CPU list from CPU mask ", func() {
			for _, cpuEntry := range cpuListToMask {
				cpuSetFromList, err := cpuset.Parse(cpuEntry.cpuList)
				Expect(err).ToNot(HaveOccurred())
				cpuSetFromMask, err := CPUMaskToCPUSet(cpuEntry.cpuMask)
				Expect(err).ToNot(HaveOccurred())

				Expect(cpuSetFromList).Should(Equal(cpuSetFromMask))
			}
		})
	})

	Context("Check intersections between CPU sets", func() {
		It("should detect invalid cpulists", func() {
			var cpuListInvalid = []string{
				"0-", "-", "-3", ",,", ",2", "-,", "0-1,", "0,1,3,,4",
			}

			for _, entry := range cpuListInvalid {
				_, err := intersectHelper(entry, entry)
				Expect(err).To(HaveOccurred())

				_, err = intersectHelper(entry, "0-3")
				Expect(err).To(HaveOccurred())

				_, err = intersectHelper("0-3", entry)
				Expect(err).To(HaveOccurred())
			}
		})

		It("should detect cpulist intersections", func() {
			type cpuListIntersect struct {
				cpuListA string
				cpuListB string
				result   []int
			}

			var cpuListIntersectTestcases = []cpuListIntersect{
				{"", "0-3", []int{}},
				{"0-3", "", []int{}},
				{"0-3", "4-15", []int{}},
				{"0-3", "8-15", []int{}},
				{"0-3", "0-15", []int{0, 1, 2, 3}},
				{"0-3", "3-15", []int{3}},
				{"3-7", "6-15", []int{6, 7}},
			}

			for _, entry := range cpuListIntersectTestcases {
				res, err := intersectHelper(entry.cpuListA, entry.cpuListB)
				Expect(err).ToNot(HaveOccurred())

				Expect(len(res)).To(Equal(len(entry.result)))
				for idx, cpuid := range res {
					Expect(cpuid).To(Equal(entry.result[idx]))
				}
			}
		})
	})
})
