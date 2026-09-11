package change

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"control-center/internal/orchestration/config"
	"control-center/internal/orchestration/policy"
)

type State string

const (
	StatePendingApproval State = "pending_approval"
	StateApproved        State = "approved"
	StateQueued          State = "queued"
	StateExecuting       State = "executing"
	StateVerifying       State = "verifying"
	StateSucceeded       State = "succeeded"
	StateFailed          State = "failed"
	StateCancelled       State = "cancelled"
	StateRejected        State = "rejected"
)

var ErrInvalidTransition = errors.New("invalid change transition")

type Snapshot struct {
	ID         string            `json:"id"`
	Action     string            `json:"action"`
	Requester  string            `json:"requester"`
	RevisionID string            `json:"revisionId"`
	Risk       policy.Risk       `json:"risk"`
	State      State             `json:"state"`
	Decision   policy.Decision   `json:"decision"`
	Approvals  []policy.Approval `json:"approvals"`
	Version    uint64            `json:"version"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

type Machine struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

func New(id, action, requester string, revision config.Revision, decision policy.Decision, now time.Time) (*Machine, error) {
	if id == "" || action == "" || requester == "" {
		return nil, errors.New("change id, action, and requester are required")
	}
	if now.IsZero() {
		return nil, errors.New("change time is required")
	}
	if err := decision.Validate(); err != nil {
		return nil, fmt.Errorf("policy decision: %w", err)
	}
	state := StateRejected
	if decision.Effect == policy.EffectAllow {
		state = StateApproved
		if decision.Requirement.Minimum > 0 {
			state = StatePendingApproval
		}
	}
	return &Machine{snapshot: Snapshot{ID: id, Action: action, Requester: requester, RevisionID: revision.ID(), Risk: decision.Risk, State: state, Decision: decision, Version: 1, UpdatedAt: now.UTC()}}, nil
}

func (m *Machine) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	copy := m.snapshot
	copy.Approvals = append([]policy.Approval(nil), m.snapshot.Approvals...)
	return copy
}
func (m *Machine) Approve(approval policy.Approval, expectedVersion uint64, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snapshot.Version != expectedVersion {
		return fmt.Errorf("change version conflict: expected %d, current %d", expectedVersion, m.snapshot.Version)
	}
	if m.snapshot.State != StatePendingApproval {
		return fmt.Errorf("%w: cannot approve from %s", ErrInvalidTransition, m.snapshot.State)
	}
	m.snapshot.Approvals = append(m.snapshot.Approvals, approval)
	if policy.CheckApprovals(m.snapshot.Requester, m.snapshot.Decision.Requirement, m.snapshot.Approvals) == nil {
		m.snapshot.State = StateApproved
	}
	m.snapshot.Version++
	m.snapshot.UpdatedAt = now.UTC()
	return nil
}
func (m *Machine) Transition(to State, expectedVersion uint64, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snapshot.Version != expectedVersion {
		return fmt.Errorf("change version conflict: expected %d, current %d", expectedVersion, m.snapshot.Version)
	}
	if !allowed(m.snapshot.State, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, m.snapshot.State, to)
	}
	m.snapshot.State = to
	m.snapshot.Version++
	m.snapshot.UpdatedAt = now.UTC()
	return nil
}
func allowed(from, to State) bool {
	switch from {
	case StatePendingApproval:
		return to == StateCancelled || to == StateRejected
	case StateApproved:
		return to == StateQueued || to == StateCancelled
	case StateQueued:
		return to == StateExecuting || to == StateCancelled || to == StateFailed
	case StateExecuting:
		return to == StateVerifying || to == StateFailed || to == StateCancelled
	case StateVerifying:
		return to == StateSucceeded || to == StateFailed
	default:
		return false
	}
}
