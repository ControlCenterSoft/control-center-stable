package health

import "sort"

// ReadinessCheck is one deterministic service readiness signal.
type ReadinessCheck struct {
	Name  string
	Ready bool
}

// ReadinessSummary is the normalized result of a readiness evaluation.
type ReadinessSummary struct {
	Ready   bool
	Failed  []string
	Checked int
}

// SummarizeReadiness evaluates readiness signals with stable failure ordering.
func SummarizeReadiness(checks []ReadinessCheck) ReadinessSummary {
	failed := make([]string, 0)
	for _, check := range checks {
		if check.Name == "" {
			continue
		}
		if !check.Ready {
			failed = append(failed, check.Name)
		}
	}
	sort.Strings(failed)
	return ReadinessSummary{Ready: len(failed) == 0, Failed: failed, Checked: len(checks)}
}
