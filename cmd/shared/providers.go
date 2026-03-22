package shared

import "strings"

// ValidProviders is the list of supported cloud providers
var ValidProviders = []string{"civo", "aws", "gcp", "azure"}

// IsValidProvider checks if the given provider is in the list of valid providers
func IsValidProvider(provider string) bool {
	provider = strings.ToLower(provider)
	for _, p := range ValidProviders {
		if p == provider {
			return true
		}
	}
	return false
}

// ValidProvidersString returns a formatted string of valid providers for error messages
func ValidProvidersString() string {
	return strings.Join(ValidProviders, ", ")
}
