package job

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"control-center/internal/orchestration/events"
)

var (
	ErrNotFound            = errors.New("job not found")
	ErrLeaseLost           = errors.New("job lease lost")
	ErrIdempotencyConflict = errors.New("idempotency key already represents different input")
	ErrVersionConflict     = errors.New("job version conflict")
)

type Status string

const (
	StatusQueued          Status = "queued"
	StatusRunning         Status = "running"
	StatusRetryWait       Status = "retry_wait"
	StatusCancelRequested Status = "cancel_requested"
	StatusCancelled       Status = "cancelled"
	StatusSucceeded       Status = "succeeded"
	StatusFailed          Status = "failed"
)

func (s Status) Terminal() bool {
	return s == StatusCancelled || s == StatusSucceeded || s == StatusFailed
}

type Lease struct {
	Token     string    `json:"token"`
	WorkerID  string    `json:"workerId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type Job struct {
	ID             string          `json:"id"`
	ChangeID       string          `json:"changeId"`
	ActionName     string          `json:"actionName"`
	Input          json.RawMessage `json:"input"`
	IdempotencyKey string          `json:"idempotencyKey"`
	Status         Status          `json:"status"`
	Attempt        int             `json:"attempt"`
	MaxAttempts    int             `json:"maxAttempts"`
	NextAttemptAt  time.Time       `json:"nextAttemptAt,omitempty"`
	Lease          *Lease          `json:"lease,omitempty"`
	Output         *events.Output  `json:"output,omitempty"`
	LastError      string          `json:"lastError,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	Version        uint64          `json:"version"`
}

type CreateRequest struct {
	ID             string
	ChangeID       string
	ActionName     string
	Input          json.RawMessage
	IdempotencyKey string
	MaxAttempts    int
	Now            time.Time
}
type RetryPolicy struct {
	BaseDelay time.Duration
	MaxDelay  time.Duration
}
type Filter struct {
	ChangeID string
	Status   Status
}

type Repository interface {
	Create(context.Context, CreateRequest) (created Job, wasCreated bool, err error)
	Get(context.Context, string) (Job, error)
	List(context.Context, Filter) ([]Job, error)
	Claim(context.Context, string, time.Time, time.Duration) (Job, bool, error)
	RenewLease(context.Context, string, string, time.Time, time.Duration) (Job, error)
	Succeed(context.Context, string, string, events.Output, time.Time) (Job, error)
	Fail(context.Context, string, string, string, events.Output, RetryPolicy, time.Time) (Job, error)
	RequestCancel(context.Context, string, time.Time) (Job, error)
}

// VersionedCancellationRepository is the optimistic-concurrency boundary for
// operator-driven cancellation. Callers bind a cancellation decision to the
// exact Job version that was reviewed instead of mutating whichever state is
// current when the request arrives.
type VersionedCancellationRepository interface {
	RequestCancelIfVersion(context.Context, string, uint64, time.Time) (Job, error)
}
