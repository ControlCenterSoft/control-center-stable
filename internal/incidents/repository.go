package incidents

import (
	"context"
	"errors"

	"control-center/internal/corecontracts"
)

// ErrNotFound is returned when an incident object does not exist in the
// caller-visible repository scope. API adapters should avoid distinguishing a
// missing object from one hidden by authorization policy.
var ErrNotFound = errors.New("incident not found")

// Reader is the minimal persistence boundary required by read-only incident
// API/UI adapters. Authorization and scope derivation happen before this
// interface is called; implementations still validate all returned objects.
type Reader interface {
	Get(context.Context, string) (Incident, error)
	List(context.Context, ListQuery) (ListPage, error)
}

// MutationWriter is the optimistic-concurrency persistence boundary for
// PreparedMutation results. Implementations must validate Precondition in the
// same transaction as the write and must not trust DesiredChanged without also
// validating the metadata successor.
type MutationWriter interface {
	Replace(context.Context, corecontracts.ObjectPrecondition, Incident, bool) error
}

// MutationRepository is the complete storage boundary required by an
// authenticated incident operator API. Authorization, step-up/MFA and audit
// emission intentionally remain outside persistence.
type MutationRepository interface {
	Reader
	MutationWriter
}
