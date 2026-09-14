package incidents

import (
	"context"
	"errors"
	"testing"
	"time"

	"control-center/internal/corecontracts"
)

type operatorReaderStub struct {
	incident  Incident
	page      ListPage
	getErr    error
	listErr   error
	getCalls  int
	listCalls int
	lastQuery ListQuery
}

func (r *operatorReaderStub) Get(context.Context, string) (Incident, error) {
	r.getCalls++
	return r.incident, r.getErr
}

func (r *operatorReaderStub) List(_ context.Context, query ListQuery) (ListPage, error) {
	r.listCalls++
	r.lastQuery = query
	return r.page, r.listErr
}

type operatorPolicyStub struct {
	listErr        error
	incidentErr    error
	listCalls      int
	incidentCalls  int
	lastCapability OperatorCapability
}

func (p *operatorPolicyStub) AuthorizeList(context.Context, string, OperatorCapability, ListQuery) error {
	p.listCalls++
	return p.listErr
}

func (p *operatorPolicyStub) AuthorizeIncident(_ context.Context, _ string, capability OperatorCapability, _ Incident) error {
	p.incidentCalls++
	p.lastCapability = capability
	return p.incidentErr
}

type operatorVersionStub struct {
	value string
	err   error
	calls int
}

func (v *operatorVersionStub) NextIncidentResourceVersion(context.Context, Incident) (string, error) {
	v.calls++
	return v.value, v.err
}

type operatorCommitterStub struct {
	err      error
	calls    int
	mutation PreparedMutation
	audit    OperatorAuditRecord
	result   *Incident
}

func (c *operatorCommitterStub) CommitIncidentMutation(_ context.Context, mutation PreparedMutation, audit OperatorAuditRecord) (Incident, error) {
	c.calls++
	c.mutation = mutation
	c.audit = audit
	if c.err != nil {
		return Incident{}, c.err
	}
	if c.result != nil {
		return *c.result, nil
	}
	return mutation.Next, nil
}

func TestOperatorAcknowledgeBuildsAtomicAuditBoundMutation(t *testing.T) {
	current := validIncident(StatusOpen)
	reader := &operatorReaderStub{incident: current}
	policy := &operatorPolicyStub{}
	versions := &operatorVersionStub{value: "rv-3"}
	committer := &operatorCommitterStub{}
	service := NewOperatorService(reader, policy, versions, committer)
	generation := current.Generation
	at := current.UpdatedAt.Add(time.Minute)

	next, err := service.Acknowledge(context.Background(), "user:operator", current.ObjectID, AcknowledgeCommand{
		Precondition: corecontracts.ObjectPrecondition{ObjectID: current.ObjectID, ResourceVersion: current.ResourceVersion, Generation: &generation},
		OccurredAt:   at,
		Note:         "Принято в работу",
	})
	if err != nil {
		t.Fatalf("Acknowledge() error = %v", err)
	}
	if next.Status != StatusAcknowledged || next.ResourceVersion != "rv-3" || next.Generation != current.Generation+1 {
		t.Fatalf("Acknowledge() next = %#v", next)
	}
	if policy.lastCapability != CapabilityIncidentAcknowledge {
		t.Fatalf("policy capability = %q", policy.lastCapability)
	}
	if versions.calls != 1 || committer.calls != 1 {
		t.Fatalf("version calls = %d, commit calls = %d", versions.calls, committer.calls)
	}
	if committer.audit.ActorID != "user:operator" || committer.audit.IncidentID != current.ObjectID || committer.audit.ScopeID != current.ScopeID {
		t.Fatalf("audit identity = %#v", committer.audit)
	}
	if committer.audit.BeforeResourceVersion != current.ResourceVersion || committer.audit.AfterResourceVersion != "rv-3" {
		t.Fatalf("audit revision binding = %#v", committer.audit)
	}
	if committer.audit.BeforeStatus != StatusOpen || committer.audit.AfterStatus != StatusAcknowledged {
		t.Fatalf("audit lifecycle binding = %#v", committer.audit)
	}
	if !committer.audit.OccurredAt.Equal(at.UTC()) {
		t.Fatalf("audit occurred_at = %s, want %s", committer.audit.OccurredAt, at.UTC())
	}
}

func TestOperatorMutationReturnsCanonicalCommitterState(t *testing.T) {
	current := validIncident(StatusOpen)
	generation := current.Generation
	at := current.UpdatedAt.Add(time.Minute + 789*time.Nanosecond)
	canonicalAt := at.Truncate(time.Microsecond)
	canonical := current
	canonical.Status = StatusAcknowledged
	canonical.Generation++
	canonical.ResourceVersion = "rv-3"
	canonical.UpdatedAt = canonicalAt
	canonical.Acknowledgement = &Acknowledgement{ActorID: "user:operator", At: canonicalAt}
	canonical.Timeline = append(canonical.Timeline, TimelineEntry{Kind: TimelineAcknowledged, At: canonicalAt, ActorID: "user:operator"})

	service := NewOperatorService(
		&operatorReaderStub{incident: current},
		&operatorPolicyStub{},
		&operatorVersionStub{value: "rv-3"},
		&operatorCommitterStub{result: &canonical},
	)
	got, err := service.Acknowledge(context.Background(), "user:operator", current.ObjectID, AcknowledgeCommand{
		Precondition: corecontracts.ObjectPrecondition{ObjectID: current.ObjectID, ResourceVersion: current.ResourceVersion, Generation: &generation},
		OccurredAt:   at,
	})
	if err != nil {
		t.Fatalf("Acknowledge() error = %v", err)
	}
	if !got.UpdatedAt.Equal(canonicalAt) || got.UpdatedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("returned state is not canonical persisted state: %s", got.UpdatedAt)
	}
}

func TestOperatorMutationDenialHidesIncidentAndStopsBeforeRevision(t *testing.T) {
	current := validIncident(StatusOpen)
	reader := &operatorReaderStub{incident: current}
	policy := &operatorPolicyStub{incidentErr: ErrOperatorAccessDenied}
	versions := &operatorVersionStub{value: "rv-3"}
	committer := &operatorCommitterStub{}
	service := NewOperatorService(reader, policy, versions, committer)

	_, err := service.Acknowledge(context.Background(), "user:viewer", current.ObjectID, AcknowledgeCommand{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Acknowledge() error = %v, want ErrNotFound", err)
	}
	if versions.calls != 0 || committer.calls != 0 {
		t.Fatalf("denied mutation reached version/commit: versions=%d commits=%d", versions.calls, committer.calls)
	}
}

func TestOperatorMutationPreservesStepUpRequirement(t *testing.T) {
	current := validIncident(StatusOpen)
	reader := &operatorReaderStub{incident: current}
	policy := &operatorPolicyStub{incidentErr: ErrOperatorStepUpRequired}
	versions := &operatorVersionStub{value: "rv-3"}
	committer := &operatorCommitterStub{}
	service := NewOperatorService(reader, policy, versions, committer)

	_, err := service.Acknowledge(context.Background(), "user:operator", current.ObjectID, AcknowledgeCommand{})
	if !errors.Is(err, ErrOperatorStepUpRequired) {
		t.Fatalf("Acknowledge() error = %v, want ErrOperatorStepUpRequired", err)
	}
	if versions.calls != 0 || committer.calls != 0 {
		t.Fatalf("step-up blocked mutation reached version/commit: versions=%d commits=%d", versions.calls, committer.calls)
	}
}

func TestOperatorCommitFailureNeverReturnsPreparedSuccess(t *testing.T) {
	current := validIncident(StatusOpen)
	reader := &operatorReaderStub{incident: current}
	policy := &operatorPolicyStub{}
	versions := &operatorVersionStub{value: "rv-3"}
	commitErr := errors.New("atomic audit commit failed")
	committer := &operatorCommitterStub{err: commitErr}
	service := NewOperatorService(reader, policy, versions, committer)
	generation := current.Generation

	_, err := service.Acknowledge(context.Background(), "user:operator", current.ObjectID, AcknowledgeCommand{
		Precondition: corecontracts.ObjectPrecondition{ObjectID: current.ObjectID, ResourceVersion: current.ResourceVersion, Generation: &generation},
		OccurredAt:   current.UpdatedAt.Add(time.Minute),
	})
	if !errors.Is(err, commitErr) {
		t.Fatalf("Acknowledge() error = %v, want commit failure", err)
	}
	if committer.calls != 1 {
		t.Fatalf("commit calls = %d, want 1", committer.calls)
	}
}

func TestOperatorListAuthorizesBeforeStorage(t *testing.T) {
	current := validIncident(StatusAcknowledged)
	reader := &operatorReaderStub{page: ListPage{Items: []Incident{current}}}
	policy := &operatorPolicyStub{listErr: ErrOperatorAccessDenied}
	service := NewOperatorService(reader, policy, &operatorVersionStub{value: "unused"}, &operatorCommitterStub{})

	_, err := service.List(context.Background(), "user:viewer", ListQuery{ScopeID: current.ScopeID})
	if !errors.Is(err, ErrOperatorAccessDenied) {
		t.Fatalf("List() error = %v, want ErrOperatorAccessDenied", err)
	}
	if reader.listCalls != 0 {
		t.Fatalf("unauthorized list reached storage %d times", reader.listCalls)
	}
}

func TestOperatorListRejectsPolicyStorageScopeMismatch(t *testing.T) {
	current := validIncident(StatusAcknowledged)
	reader := &operatorReaderStub{page: ListPage{Items: []Incident{current}}}
	policy := &operatorPolicyStub{incidentErr: ErrOperatorAccessDenied}
	service := NewOperatorService(reader, policy, &operatorVersionStub{value: "unused"}, &operatorCommitterStub{})

	_, err := service.List(context.Background(), "user:viewer", ListQuery{ScopeID: current.ScopeID})
	if !errors.Is(err, ErrOperatorDependencyUnavailable) {
		t.Fatalf("List() error = %v, want ErrOperatorDependencyUnavailable", err)
	}
	if reader.listCalls != 1 || policy.incidentCalls != 1 {
		t.Fatalf("list calls=%d item auth calls=%d", reader.listCalls, policy.incidentCalls)
	}
}

func TestOperatorReadDenialDoesNotExposeExistingIdentifier(t *testing.T) {
	current := validIncident(StatusAcknowledged)
	reader := &operatorReaderStub{incident: current}
	policy := &operatorPolicyStub{incidentErr: ErrOperatorAccessDenied}
	service := NewOperatorService(reader, policy, &operatorVersionStub{}, &operatorCommitterStub{})

	_, err := service.Get(context.Background(), "user:viewer", current.ObjectID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}
