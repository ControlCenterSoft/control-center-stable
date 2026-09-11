package integration

import "sort"

type Requirement struct {
	Name     string
	Requires []string
}

func Missing(requirement Requirement, available map[string]bool) []string {
	missing := make([]string, 0)
	seen := make(map[string]struct{}, len(requirement.Requires))
	for _, capability := range requirement.Requires {
		if capability == "" {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		if !available[capability] {
			missing = append(missing, capability)
		}
	}
	sort.Strings(missing)
	return missing
}

func Compatible(requirement Requirement, available map[string]bool) bool {
	return len(Missing(requirement, available)) == 0
}
