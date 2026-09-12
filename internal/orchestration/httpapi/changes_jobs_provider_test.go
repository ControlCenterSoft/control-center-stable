package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"control-center/internal/orchestration/change"
	orchestrationconfig "control-center/internal/orchestration/config"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
	productui "control-center/internal/ui"
)

func TestServerChangesJobsProjectsCurrentChangeAndDurableJobs(t *testing.T) {
	now := time.Date(2026, 9, 12, 7, 0, 0, 0, time.UTC)
	revision, err := orchestrationconfig.NewRevision("revision-a", 1, now.Add(-time.Hour), json.RawMessage(`{"service":"demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	machine, err := change.New("change-a", "service.ensure", "operator-a", revision, policy.Decision{
		Effect:   policy.EffectAllow,
		Risk:     policy.RiskLow,
		Reason:   "test policy allows low risk",
		PolicyID: "test-v1",
	}, now.Add(-10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	repository := job.NewMemoryRepository()
	created, wasCreated, err := repository.Create(context.Background(), job.CreateRequest{
		ID:             "job-a",
		ChangeID:       "change-a",
		ActionName:     "service.ensure",
		Input:          json.RawMessage(`{"enabled":true}`),
		IdempotencyKey: "job-a-create",
		MaxAttempts:    3,
		Now:            now.Add(-9 * time.Minute),
	})
	if err != nil || !wasCreated {
		t.Fatalf("create job: created=%v err=%v", wasCreated, err)
	}
	server := &Server{
		jobs: repository,
		now:  func() time.Time { return now },
		changes: map[string]*changeRecord{
			"change-a": {machine: machine, jobID: created.ID},
		},
	}

	view, err := server.ChangesJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.State != productui.ChangesJobsCurrent || view.ChangeCount != 1 || view.JobCount != 1 {
		t.Fatalf("unexpected view summary: %#v", view)
	}
	if len(view.Changes) != 1 || view.Changes[0].ID != "change-a" || len(view.Changes[0].Jobs) != 1 || view.Changes[0].Jobs[0].ID != "job-a" {
		t.Fatalf("authoritative orchestration state was not projected: %#v", view)
	}
	if view.Changes[0].WorkflowEvidence.Availability != productui.EvidenceUnavailable {
		t.Fatalf("workflow evidence must remain unavailable until independently verified: %#v", view.Changes[0].WorkflowEvidence)
	}
}

func TestServerChangesJobsFailsClosedOnDurableJobReadFailure(t *testing.T) {
	server := &Server{
		jobs:    failingChangesJobsRepository{Repository: job.NewMemoryRepository()},
		now:     time.Now,
		changes: map[string]*changeRecord{},
	}
	if _, err := server.ChangesJobs(context.Background()); err == nil || !errors.Is(err, errChangesJobsList) {
		t.Fatalf("durable job read failure must propagate, got %v", err)
	}
}

var errChangesJobsList = errors.New("injected durable job list failure")

type failingChangesJobsRepository struct {
	job.Repository
}

func (f failingChangesJobsRepository) List(context.Context, job.Filter) ([]job.Job, error) {
	return nil, errChangesJobsList
}
