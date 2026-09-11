package agent

import (
	"fmt"
	"strings"
	"time"
)

const (
	LeaseActive  = "active"
	LeaseGrace   = "grace"
	LeaseExpired = "expired"
)

// NodeLease records the latest control-plane heartbeat for an enrolled node.
type NodeLease struct {
	NodeID        string
	LastHeartbeat time.Time
}

// LeasePolicy controls when a node becomes stale or expired.
type LeasePolicy struct {
	TTL   time.Duration
	Grace time.Duration
}

// EvaluateNodeLease returns active, grace, or expired for a node lease.
func EvaluateNodeLease(lease NodeLease, policy LeasePolicy, now time.Time) (string, error) {
	if strings.TrimSpace(lease.NodeID) == "" {
		return "", fmt.Errorf("node id is required")
	}
	if lease.LastHeartbeat.IsZero() {
		return "", fmt.Errorf("last heartbeat is required")
	}
	if policy.TTL <= 0 {
		return "", fmt.Errorf("ttl must be positive")
	}
	if policy.Grace < 0 {
		return "", fmt.Errorf("grace must be non-negative")
	}
	if now.Before(lease.LastHeartbeat) {
		return "", fmt.Errorf("current time is before last heartbeat")
	}

	age := now.Sub(lease.LastHeartbeat)
	if age <= policy.TTL {
		return LeaseActive, nil
	}
	if age <= policy.TTL+policy.Grace {
		return LeaseGrace, nil
	}
	return LeaseExpired, nil
}
