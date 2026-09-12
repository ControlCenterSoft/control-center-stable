package job

import (
	"context"
	"sort"
	"sync"
)

type MemoryTimelineRepository struct {
	mu      sync.RWMutex
	entries map[string]map[uint64]TimelineEntry
}

func NewMemoryTimelineRepository() *MemoryTimelineRepository {
	return &MemoryTimelineRepository{entries: make(map[string]map[uint64]TimelineEntry)}
}

func (r *MemoryTimelineRepository) Append(ctx context.Context, entry TimelineEntry) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := ValidateTimelineEntry(entry); err != nil {
		return false, err
	}
	entry.OccurredAt = entry.OccurredAt.UTC()

	r.mu.Lock()
	defer r.mu.Unlock()
	versions := r.entries[entry.JobID]
	if versions == nil {
		versions = make(map[uint64]TimelineEntry)
		r.entries[entry.JobID] = versions
	}
	if existing, ok := versions[entry.JobVersion]; ok {
		if existing == entry {
			return false, nil
		}
		return false, ErrTimelineConflict
	}
	versions[entry.JobVersion] = entry
	return true, nil
}

func (r *MemoryTimelineRepository) List(ctx context.Context, jobID string) ([]TimelineEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if jobID == "" {
		return nil, ErrInvalidTimelineEntry
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	versions := r.entries[jobID]
	result := make([]TimelineEntry, 0, len(versions))
	for _, entry := range versions {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].JobVersion != result[j].JobVersion {
			return result[i].JobVersion < result[j].JobVersion
		}
		return result[i].OccurredAt.Before(result[j].OccurredAt)
	})
	return result, nil
}
