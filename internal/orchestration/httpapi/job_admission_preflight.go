package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"control-center/internal/orchestration/executionguard"
)

var errJobAdmissionBlocked = errors.New("durable job admission blocked by execution preflight")

type jobAdmissionBlockError struct {
	Blockers []string
}

func (e *jobAdmissionBlockError) Error() string {
	if e == nil || len(e.Blockers) == 0 {
		return errJobAdmissionBlocked.Error()
	}
	return fmt.Sprintf("%s: %s", errJobAdmissionBlocked, strings.Join(e.Blockers, ","))
}

func (e *jobAdmissionBlockError) Unwrap() error { return errJobAdmissionBlocked }

// evaluateJobAdmission revalidates the exact immutable Change revision and
// effective approval evidence immediately before durable Job admission. The
// caller must hold s.mu so currentRevision and revisions are read from one
// coherent server snapshot.
//
// The returned decision is evidence only. It never grants execution authority
// and does not mutate Change or Job state.
func (s *Server) evaluateJobAdmission(record *changeRecord) (executionguard.Decision, error) {
	if record == nil || record.machine == nil {
		return executionguard.Decision{}, fmt.Errorf("%w: change record is unavailable", errJobAdmissionBlocked)
	}

	snapshot := record.machine.Snapshot()
	revision, exists := s.revisions[snapshot.RevisionID]
	if !exists {
		return executionguard.Decision{}, fmt.Errorf("%w: immutable revision is unavailable", errJobAdmissionBlocked)
	}
	if s.now == nil {
		return executionguard.Decision{}, fmt.Errorf("%w: admission clock is unavailable", errJobAdmissionBlocked)
	}

	decision, err := executionguard.Evaluate(executionguard.Input{
		Change:            snapshot,
		Revision:          revision,
		CurrentRevisionID: s.currentRevision,
		Now:               s.now().UTC(),
	})
	if err != nil {
		return executionguard.Decision{}, fmt.Errorf("%w: invalid preflight evidence: %v", errJobAdmissionBlocked, err)
	}
	if !decision.Eligible {
		return decision, &jobAdmissionBlockError{Blockers: append([]string(nil), decision.Blockers...)}
	}
	return decision, nil
}

// enqueueWithAdmissionPreflight is the fail-closed admission wrapper for the
// existing durable enqueue path. It deliberately delegates actual mutation to
// enqueue only after the side-effect-free exact-revision check succeeds.
//
// Callers are responsible for the same Server mutex discipline as enqueue.
func (s *Server) enqueueWithAdmissionPreflight(ctx context.Context, record *changeRecord) error {
	if _, err := s.evaluateJobAdmission(record); err != nil {
		return err
	}
	return s.enqueue(ctx, record)
}
