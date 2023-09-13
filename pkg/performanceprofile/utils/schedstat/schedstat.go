package schedstat

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultPath = "/proc/schedstat"
)

func MakeCPUIDListFromCPUList(cpus []string) ([]int, error) {
	cpuIDs := make([]int, 0, len(cpus))
	for _, cpu := range cpus {
		if !strings.HasPrefix(cpu, "cpu") {
			continue
		}
		// len("cpu") == 3
		cpuID, err := strconv.Atoi(cpu[3:])
		if err != nil {
			return cpuIDs, err
		}
		cpuIDs = append(cpuIDs, cpuID)
	}
	return cpuIDs, nil
}

type Info struct {
	cpuDomains map[string][]string
}

// GetCPUs returns all the known CPUs, sorted.
func (inf Info) GetCPUs() []string {
	ret := []string{}
	for cpu := range inf.cpuDomains {
		ret = append(ret, cpu)
	}
	sort.Strings(ret)
	return ret
}

func (inf Info) GetDomainsByID(cpuID int) ([]string, bool) {
	return inf.GetDomains(fmt.Sprintf("cpu%d", cpuID))
}

// GetDomains will return all the domains the given cpu belongs.
// The domains are returned sorted.
func (inf Info) GetDomains(cpu string) ([]string, bool) {
	doms, ok := inf.cpuDomains[cpu]
	if !ok {
		// skip unnecessary work
		return doms, ok
	}
	ret := make([]string, len(doms))
	copy(ret, doms)
	sort.Strings(ret)
	return ret, ok
}

func ParseData(r io.Reader) (Info, error) {
	ret := Info{
		cpuDomains: make(map[string][]string),
	}
	curCpu := ""
	domains := []string{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if err := scanner.Err(); err != nil {
			return ret, err
		}
		tokens := strings.Fields(line)
		if len(tokens) == 0 {
			return ret, fmt.Errorf("malformed line: %q", line)
		}
		if strings.HasPrefix(tokens[0], "cpu") {
			if curCpu != "" {
				ret.cpuDomains[curCpu] = domains
			}
			curCpu = tokens[0]
			domains = []string{}
			continue
		}
		if strings.HasPrefix(tokens[0], "domain") {
			domains = append(domains, tokens[1])
			continue
		}
		// else just ignore
	}
	if curCpu != "" {
		ret.cpuDomains[curCpu] = domains
	}
	return ret, nil
}
