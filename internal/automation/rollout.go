package automation

import (
	"fmt"
	"sort"
	"strings"
)

// RolloutStrategy controls canary size and maximum parallel targets.
type RolloutStrategy struct {
	CanarySize  int
	MaxParallel int
}

// BuildRolloutWaves normalizes targets and builds deterministic deployment waves.
func BuildRolloutWaves(targets []string, strategy RolloutStrategy) ([][]string, error) {
	if strategy.MaxParallel < 1 {
		return nil, fmt.Errorf("max parallel must be at least one")
	}
	if strategy.CanarySize < 0 {
		return nil, fmt.Errorf("canary size must be non-negative")
	}

	unique := map[string]struct{}{}
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			return nil, fmt.Errorf("target is required")
		}
		unique[target] = struct{}{}
	}
	ordered := make([]string, 0, len(unique))
	for target := range unique {
		ordered = append(ordered, target)
	}
	sort.Strings(ordered)

	if len(ordered) == 0 {
		return nil, nil
	}

	waves := make([][]string, 0)
	start := 0
	if strategy.CanarySize > 0 {
		canary := strategy.CanarySize
		if canary > len(ordered) {
			canary = len(ordered)
		}
		waves = append(waves, append([]string(nil), ordered[:canary]...))
		start = canary
	}

	for start < len(ordered) {
		end := start + strategy.MaxParallel
		if end > len(ordered) {
			end = len(ordered)
		}
		waves = append(waves, append([]string(nil), ordered[start:end]...))
		start = end
	}
	return waves, nil
}
