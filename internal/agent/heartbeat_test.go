package agent

import (
	"testing"
	"time"
)

func TestEvaluateHeartbeat(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	if got := EvaluateHeartbeat(now.Add(-10*time.Second), now, time.Minute, 5*time.Minute); got != HeartbeatOnline {
		t.Fatalf("expected online, got %s", got)
	}
	if got := EvaluateHeartbeat(now.Add(-2*time.Minute), now, time.Minute, 5*time.Minute); got != HeartbeatDelayed {
		t.Fatalf("expected delayed, got %s", got)
	}
	if got := EvaluateHeartbeat(now.Add(-10*time.Minute), now, time.Minute, 5*time.Minute); got != HeartbeatOffline {
		t.Fatalf("expected offline, got %s", got)
	}
}
