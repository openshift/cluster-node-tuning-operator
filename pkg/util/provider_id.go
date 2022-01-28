package util

import (
	"strings"
)

// GetProviderName returns ProviderName part of 'providerID' in the format:
// <ProviderName>://<ProviderSpecificNodeID>
func GetProviderName(providerID string) string {
	i := strings.Index(providerID, "://")
	if i < 0 {
		// ProviderName/ProviderSpecificNodeID separator not found, return the whole providerID
		return providerID
	}
	return providerID[:i]
}
