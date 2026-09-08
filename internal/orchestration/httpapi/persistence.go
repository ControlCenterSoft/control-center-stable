package httpapi

import (
	"context"
	"encoding/json"
	"time"

	"control-center/internal/orchestration/change"
)

// StatePersistence is the durable boundary for orchestration state not owned
// by the Job repository. Runtime wiring must provide a PostgreSQL-backed
// implementation; in-memory implementations are limited to tests.
type StatePersistence interface {
	Load(context.Context) (PersistedState, error)
	CreateRevision(context.Context, string, string, string, json.RawMessage, time.Time) (PersistedRevision, error)
	CreateChange(context.Context, PersistedChange) (PersistedChange, bool, error)
	UpdateChange(context.Context, PersistedChange) error
}

type PersistedState struct {
	Revisions []PersistedRevision
	Changes   []PersistedChange
}

type PersistedRevision struct {
	ID             string
	Sequence       uint64
	Digest         string
	Content        json.RawMessage
	CreatedAt      time.Time
	CreatedBy      string
	IdempotencyKey string
	Fingerprint    string
}

type PersistedChange struct {
	Snapshot       change.Snapshot
	Input          json.RawMessage
	IdempotencyKey string
	Fingerprint    string
	JobID          string
}
