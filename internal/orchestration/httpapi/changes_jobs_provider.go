package httpapi

import (
	"context"
	"errors"
	"fmt"

	"control-center/internal/orchestration/change"
	"control-center/internal/orchestration/job"
	productui "control-center/internal/ui"
)

// ChangesJobs implements the bounded Control Center Changes / Jobs read-model
// provider from the same authoritative orchestration state already used by the
// mutation API. It is deliberately read-only: it snapshots current Change
// machines and lists durable Jobs without approving, enqueueing, claiming,
// retrying, cancelling or executing work.
func (s *Server) ChangesJobs(ctx context.Context) (productui.ChangesJobsView, error) {
	if s == nil || s.jobs == nil {
		return productui.ChangesJobsView{}, errors.New("orchestration changes/jobs provider is unavailable")
	}
	if ctx == nil {
		return productui.ChangesJobsView{}, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return productui.ChangesJobsView{}, err
	}

	s.mu.RLock()
	if s.now == nil {
		s.mu.RUnlock()
		return productui.ChangesJobsView{}, errors.New("orchestration clock is unavailable")
	}
	changes := make([]change.Snapshot, 0, len(s.changes))
	for changeID, record := range s.changes {
		if record == nil || record.machine == nil {
			s.mu.RUnlock()
			return productui.ChangesJobsView{}, fmt.Errorf("change %q has no authoritative state machine", changeID)
		}
		changes = append(changes, record.machine.Snapshot())
	}
	now := s.now().UTC()
	s.mu.RUnlock()

	jobs, err := s.jobs.List(ctx, job.Filter{})
	if err != nil {
		return productui.ChangesJobsView{}, fmt.Errorf("list durable jobs: %w", err)
	}
	return productui.BuildChangesJobsView(productui.ChangesJobsInput{
		Changes:       changes,
		Jobs:          jobs,
		ChangesLoaded: true,
		JobsLoaded:    true,
		Now:           now,
	})
}
