package automation

import "sort"

// BatchTargets splits normalized targets into deterministic execution batches.
func BatchTargets(targets []string, batchSize int) [][]string {
	if batchSize <= 0 {
		return nil
	}
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(targets))
	for _, target := range targets {
		if target == "" {
			continue
		}
		if _, exists := seen[target]; exists {
			continue
		}
		seen[target] = struct{}{}
		normalized = append(normalized, target)
	}
	sort.Strings(normalized)
	batches := make([][]string, 0, (len(normalized)+batchSize-1)/batchSize)
	for start := 0; start < len(normalized); start += batchSize {
		end := start + batchSize
		if end > len(normalized) {
			end = len(normalized)
		}
		batch := append([]string(nil), normalized[start:end]...)
		batches = append(batches, batch)
	}
	return batches
}
