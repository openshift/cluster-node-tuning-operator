package config

import "os"

func SharedCPUs() string {
	cpus, ok := os.LookupEnv("E2E_SHARED_CPUS")
	if !ok {
		return ""
	}
	return cpus
}
