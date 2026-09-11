package inventory

import "time"

// FreshnessState describes how recently an inventory record was observed.
type FreshnessState string

const (
	FreshnessCurrent FreshnessState = "current"
	FreshnessStale   FreshnessState = "stale"
	FreshnessExpired FreshnessState = "expired"
)

// EvaluateFreshness classifies an observation by age.
func EvaluateFreshness(observedAt, now time.Time, staleAfter, expireAfter time.Duration) FreshnessState {
	if now.Before(observedAt) {
		return FreshnessCurrent
	}
	age := now.Sub(observedAt)
	if expireAfter >= 0 && age >= expireAfter {
		return FreshnessExpired
	}
	if staleAfter >= 0 && age >= staleAfter {
		return FreshnessStale
	}
	return FreshnessCurrent
}
