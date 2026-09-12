package job

import (
	"fmt"
	"strings"
)

// TimelineEntryFromTransition converts one persisted Job state change into
// bounded lifecycle evidence. The boolean is false for state-neutral version
// changes such as lease renewal, which intentionally do not create operator
// timeline noise.
func TimelineEntryFromTransition(before *Job, after Job) (TimelineEntry, bool, error) {
	if strings.TrimSpace(after.ID) == "" || after.ID != strings.TrimSpace(after.ID) {
		return TimelineEntry{}, false, fmt.Errorf("%w: canonical job id is required", ErrInvalidTimelineEntry)
	}
	if !validTimelineStatus(after.Status) || after.Version == 0 || after.UpdatedAt.IsZero() || after.Attempt < 0 || after.MaxAttempts < 1 || after.Attempt > after.MaxAttempts {
		return TimelineEntry{}, false, fmt.Errorf("%w: incomplete Job state", ErrInvalidTimelineEntry)
	}

	if before == nil {
		if after.Status != StatusQueued || after.Attempt != 0 || after.Version != 1 {
			return TimelineEntry{}, false, fmt.Errorf("%w: first lifecycle evidence must be queued version 1", ErrInvalidTimelineEntry)
		}
		entry := timelineEntry(after, TimelineCreated)
		return entry, true, ValidateTimelineEntry(entry)
	}
	if before.ID != after.ID || before.Version >= after.Version || before.UpdatedAt.IsZero() || after.UpdatedAt.Before(before.UpdatedAt) {
		return TimelineEntry{}, false, fmt.Errorf("%w: transition identity, version or timestamp is inconsistent", ErrInvalidTimelineEntry)
	}
	if after.Attempt < before.Attempt || after.Attempt > before.Attempt+1 {
		return TimelineEntry{}, false, fmt.Errorf("%w: attempt progression is inconsistent", ErrInvalidTimelineEntry)
	}

	var event TimelineEvent
	switch after.Status {
	case StatusRunning:
		if after.Attempt != before.Attempt+1 {
			if before.Status == StatusRunning && after.Attempt == before.Attempt {
				return TimelineEntry{}, false, nil
			}
			return TimelineEntry{}, false, fmt.Errorf("%w: running transition must start exactly one attempt", ErrInvalidTimelineEntry)
		}
		switch before.Status {
		case StatusQueued, StatusRetryWait, StatusRunning:
			event = TimelineAttemptStarted
		default:
			return TimelineEntry{}, false, fmt.Errorf("%w: cannot start attempt from %q", ErrInvalidTimelineEntry, before.Status)
		}
	case StatusRetryWait:
		if before.Status != StatusRunning || after.Attempt != before.Attempt {
			return TimelineEntry{}, false, fmt.Errorf("%w: retry scheduling requires a running attempt", ErrInvalidTimelineEntry)
		}
		event = TimelineRetryScheduled
	case StatusCancelRequested:
		if before.Status != StatusRunning || after.Attempt != before.Attempt {
			return TimelineEntry{}, false, fmt.Errorf("%w: cancel request requires a running attempt", ErrInvalidTimelineEntry)
		}
		event = TimelineCancelRequested
	case StatusCancelled:
		switch before.Status {
		case StatusQueued, StatusRetryWait, StatusRunning, StatusCancelRequested:
			event = TimelineCancelled
		default:
			return TimelineEntry{}, false, fmt.Errorf("%w: cannot cancel from %q", ErrInvalidTimelineEntry, before.Status)
		}
	case StatusSucceeded:
		if before.Status != StatusRunning || after.Attempt != before.Attempt {
			return TimelineEntry{}, false, fmt.Errorf("%w: success requires the current running attempt", ErrInvalidTimelineEntry)
		}
		event = TimelineSucceeded
	case StatusFailed:
		switch before.Status {
		case StatusQueued, StatusRetryWait, StatusRunning:
			event = TimelineFailed
		default:
			return TimelineEntry{}, false, fmt.Errorf("%w: cannot fail from %q", ErrInvalidTimelineEntry, before.Status)
		}
	case before.Status:
		if after.Attempt != before.Attempt {
			return TimelineEntry{}, false, fmt.Errorf("%w: state-neutral transition changed attempt", ErrInvalidTimelineEntry)
		}
		return TimelineEntry{}, false, nil
	default:
		return TimelineEntry{}, false, fmt.Errorf("%w: unsupported transition %q -> %q", ErrInvalidTimelineEntry, before.Status, after.Status)
	}

	entry := timelineEntry(after, event)
	return entry, true, ValidateTimelineEntry(entry)
}

func timelineEntry(source Job, event TimelineEvent) TimelineEntry {
	return TimelineEntry{
		JobID:      source.ID,
		Event:      event,
		Status:     source.Status,
		Attempt:    source.Attempt,
		JobVersion: source.Version,
		OccurredAt: source.UpdatedAt.UTC(),
	}
}
