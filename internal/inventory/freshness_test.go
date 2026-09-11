package inventory

import (
	"testing"
	"time"
)

func TestEvaluateFreshness(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	if got := EvaluateFreshness(now.Add(-5*time.Minute), now, 10*time.Minute, time.Hour); got != FreshnessCurrent {
		t.Fatalf("expected current, got %s", got)
	}
	if got := EvaluateFreshness(now.Add(-30*time.Minute), now, 10*time.Minute, time.Hour); got != FreshnessStale {
		t.Fatalf("expected stale, got %s", got)
	}
	if got := EvaluateFreshness(now.Add(-2*time.Hour), now, 10*time.Minute, time.Hour); got != FreshnessExpired {
		t.Fatalf("expected expired, got %s", got)
	}
}
