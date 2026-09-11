package agent

import (
	"testing"
	"time"
)

func TestEvaluateNodeLeaseStates(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	policy := LeasePolicy{TTL: 5 * time.Minute, Grace: 2 * time.Minute}

	tests := []struct {
		name      string
		heartbeat time.Time
		want      string
	}{
		{name: "active", heartbeat: now.Add(-4 * time.Minute), want: LeaseActive},
		{name: "grace", heartbeat: now.Add(-6 * time.Minute), want: LeaseGrace},
		{name: "expired", heartbeat: now.Add(-8 * time.Minute), want: LeaseExpired},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := EvaluateNodeLease(NodeLease{NodeID: "node-a", LastHeartbeat: test.heartbeat}, policy, now)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("state=%q want=%q", got, test.want)
			}
		})
	}
}

func TestEvaluateNodeLeaseRejectsInvalidTTL(t *testing.T) {
	_, err := EvaluateNodeLease(
		NodeLease{NodeID: "node-a", LastHeartbeat: time.Now().UTC()},
		LeasePolicy{},
		time.Now().UTC(),
	)
	if err == nil {
		t.Fatal("expected error")
	}
}
