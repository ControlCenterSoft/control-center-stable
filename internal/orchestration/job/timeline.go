package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidTimelineEntry = errors.New("invalid job timeline entry")
	ErrTimelineConflict     = errors.New("job timeline version already represents different evidence")
)

type TimelineEvent string

const (
	TimelineObserved        TimelineEvent = "observed"
	TimelineCreated         TimelineEvent = "created"
	TimelineAttemptStarted  TimelineEvent = "attempt_started"
	TimelineRetryScheduled  TimelineEvent = "retry_scheduled"
	TimelineCancelRequested TimelineEvent = "cancel_requested"
	TimelineCancelled       TimelineEvent = "cancelled"
	TimelineSucceeded       TimelineEvent = "succeeded"
	TimelineFailed          TimelineEvent = "failed"
)

// TimelineEntry is deliberately bounded operator evidence. It records only the
// lifecycle fact needed to explain a durable Job. Raw input/output, lease
// tokens, worker identities and error strings do not belong in this contract.
type TimelineEntry struct {
	JobID      string        `json:"jobId"`
	Event      TimelineEvent `json:"event"`
	Status     Status        `json:"status"`
	Attempt    int           `json:"attempt"`
	JobVersion uint64        `json:"jobVersion"`
	OccurredAt time.Time     `json:"occurredAt"`
}

// TimelineRepository stores append-only lifecycle evidence independently from
// the operator read model. Production mutation paths should append the entry in
// the same durable transaction as the corresponding Job state transition.
type TimelineRepository interface {
	Append(context.Context, TimelineEntry) (created bool, err error)
	List(context.Context, string) ([]TimelineEntry, error)
}

func ValidateTimelineEntry(entry TimelineEntry) error {
	if strings.TrimSpace(entry.JobID) == "" || entry.JobID != strings.TrimSpace(entry.JobID) {
		return fmt.Errorf("%w: canonical job id is required", ErrInvalidTimelineEntry)
	}
	if entry.JobVersion == 0 || entry.OccurredAt.IsZero() || entry.Attempt < 0 {
		return fmt.Errorf("%w: job version, timestamp and non-negative attempt are required", ErrInvalidTimelineEntry)
	}
	if !validTimelineStatus(entry.Status) {
		return fmt.Errorf("%w: unsupported status %q", ErrInvalidTimelineEntry, entry.Status)
	}
	if !timelineEventMatchesStatus(entry.Event, entry.Status) {
		return fmt.Errorf("%w: event %q is incompatible with status %q", ErrInvalidTimelineEntry, entry.Event, entry.Status)
	}
	if (entry.Event == TimelineCreated || entry.Event == TimelineObserved) && entry.Attempt != 0 && entry.Status == StatusQueued {
		return fmt.Errorf("%w: queued creation evidence cannot have an attempt", ErrInvalidTimelineEntry)
	}
	return nil
}

func validTimelineStatus(status Status) bool {
	switch status {
	case StatusQueued, StatusRunning, StatusRetryWait, StatusCancelRequested, StatusCancelled, StatusSucceeded, StatusFailed:
		return true
	default:
		return false
	}
}

func timelineEventMatchesStatus(event TimelineEvent, status Status) bool {
	switch event {
	case TimelineObserved:
		return validTimelineStatus(status)
	case TimelineCreated:
		return status == StatusQueued
	case TimelineAttemptStarted:
		return status == StatusRunning
	case TimelineRetryScheduled:
		return status == StatusRetryWait
	case TimelineCancelRequested:
		return status == StatusCancelRequested
	case TimelineCancelled:
		return status == StatusCancelled
	case TimelineSucceeded:
		return status == StatusSucceeded
	case TimelineFailed:
		return status == StatusFailed
	default:
		return false
	}
}
