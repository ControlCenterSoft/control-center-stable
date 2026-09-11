package market

import "sort"

// CompatibilityRequest describes the target environment for a module.
type CompatibilityRequest struct {
	Platform string
	Arch     string
}

// NormalizeCompatibilitySet returns a stable, duplicate-free set.
func NormalizeCompatibilitySet(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		if value == "" {
			continue
		}
		seen[value] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// IsCompatible reports whether requested platform and architecture are supported.
func IsCompatible(platforms, arches []string, request CompatibilityRequest) bool {
	if request.Platform == "" || request.Arch == "" {
		return false
	}
	platformOK := false
	for _, value := range platforms {
		if value == request.Platform {
			platformOK = true
			break
		}
	}
	if !platformOK {
		return false
	}
	for _, value := range arches {
		if value == request.Arch {
			return true
		}
	}
	return false
}
