package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	commonapi "control-center/internal/httpapi"
	"control-center/internal/identity/rbac"
	"control-center/internal/orchestration/action"
	"control-center/internal/orchestration/change"
	orchestrationconfig "control-center/internal/orchestration/config"
	"control-center/internal/orchestration/job"
	"control-center/internal/orchestration/policy"
)

const maxRequestBody = 1 << 20

type Protect func(rbac.Permission, http.Handler) http.Handler
type Actor func(*http.Request) (string, bool)

type Config struct {
	Registry    *action.Registry
	Jobs        job.Repository
	Persistence StatePersistence
	Evaluator   policy.Evaluator
	Protect     Protect
	Actor       Actor
	Middleware  func(http.Handler) http.Handler
	Now         func() time.Time
	Context     context.Context
}

type Server struct {
	registry    *action.Registry
	jobs        job.Repository
	evaluator   policy.Evaluator
	persistence StatePersistence
	actor       Actor
	now         func() time.Time
	handler     http.Handler

	mu                       sync.RWMutex
	revisions                map[string]orchestrationconfig.Revision
	revisionDigests          map[string]string
	revisionIdempotency      map[string]idempotencyRecord
	currentRevision          string
	nextRevision             uint64
	changes                  map[string]*changeRecord
	changeIdempotency        map[string]idempotencyRecord
	reconciliationCandidates map[string]string
}

type idempotencyRecord struct {
	fingerprint string
	id          string
}

type changeRecord struct {
	machine        *change.Machine
	input          json.RawMessage
	idempotencyKey string
	jobID          string
}

type revisionView struct {
	ID        string          `json:"id"`
	Sequence  uint64          `json:"sequence"`
	Digest    string          `json:"digest"`
	Content   json.RawMessage `json:"content"`
	CreatedAt time.Time       `json:"createdAt"`
}

type changeView struct {
	ID         string            `json:"id"`
	Action     string            `json:"action"`
	Requester  string            `json:"requester"`
	RevisionID string            `json:"revisionId"`
	Risk       policy.Risk       `json:"risk"`
	State      change.State      `json:"state"`
	Approvals  []policy.Approval `json:"approvals,omitempty"`
	Version    uint64            `json:"version"`
	UpdatedAt  time.Time         `json:"updatedAt"`
	JobID      string            `json:"jobId,omitempty"`
}

func New(config Config) (*Server, error) {
	if config.Registry == nil || config.Jobs == nil || config.Persistence == nil || config.Evaluator == nil || config.Protect == nil || config.Actor == nil || config.Middleware == nil {
		return nil, errors.New("registry, jobs, durable persistence, evaluator, authentication/RBAC protection, actor resolver, and common middleware are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	s := &Server{
		registry: config.Registry, jobs: config.Jobs, evaluator: config.Evaluator,
		persistence: config.Persistence, actor: config.Actor, now: config.Now,
		revisions: make(map[string]orchestrationconfig.Revision), revisionDigests: make(map[string]string),
		revisionIdempotency: make(map[string]idempotencyRecord), changes: make(map[string]*changeRecord),
		changeIdempotency: make(map[string]idempotencyRecord), reconciliationCandidates: make(map[string]string),
	}
	loadContext := config.Context
	if loadContext == nil {
		loadContext = context.Background()
	}
	loaded, err := config.Persistence.Load(loadContext)
	if err != nil {
		return nil, err
	}
	for _, persisted := range loaded.Revisions {
		revision, err := orchestrationconfig.NewRevision(persisted.ID, persisted.Sequence, persisted.CreatedAt, persisted.Content)
		if err != nil || revision.Digest() != persisted.Digest {
			return nil, errors.New("invalid persisted configuration revision")
		}
		s.revisions[revision.ID()] = revision
		s.revisionDigests[revision.Digest()] = revision.ID()
		s.revisionIdempotency[persisted.IdempotencyKey] = idempotencyRecord{fingerprint: persisted.Fingerprint, id: revision.ID()}
		if revision.Sequence() > s.nextRevision {
			s.nextRevision = revision.Sequence()
			s.currentRevision = revision.ID()
		}
	}
	for _, persisted := range loaded.Changes {
		record, err := s.restoreChange(persisted)
		if err != nil {
			return nil, err
		}
		s.changes[persisted.Snapshot.ID] = record
		s.changeIdempotency[persisted.IdempotencyKey] = idempotencyRecord{fingerprint: persisted.Fingerprint, id: persisted.Snapshot.ID}
		if record.jobID != "" && !changeStateTerminal(record.machine.Snapshot().State) {
			s.reconciliationCandidates[persisted.Snapshot.ID] = record.jobID
		}
	}
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/config/revisions", config.Protect(rbac.PermissionRevisionsWrite, http.HandlerFunc(s.createRevision)))
	mux.Handle("GET /api/v1/actions", config.Protect(rbac.PermissionActionsRead, http.HandlerFunc(s.listActions)))
	mux.Handle("POST /api/v1/changes", config.Protect(rbac.PermissionChangesWrite, http.HandlerFunc(s.createChange)))
	mux.Handle("POST /api/v1/changes/{changeId}/approvals", config.Protect(rbac.PermissionChangesApprove, http.HandlerFunc(s.approveChange)))
	mux.Handle("GET /api/v1/jobs/{jobId}", config.Protect(rbac.PermissionJobsRead, http.HandlerFunc(s.getJob)))
	mux.Handle("POST /api/v1/jobs/{jobId}/cancel", config.Protect(rbac.PermissionJobsCancel, http.HandlerFunc(s.cancelJob)))
	s.handler = config.Middleware(mux)
	for _, record := range s.changes {
		if record.machine.Snapshot().State == change.StateApproved && record.jobID == "" {
			if err := s.enqueue(loadContext, record); err != nil {
				return nil, err
			}
		}
	}
	if err := s.ReconcileTerminalJobs(loadContext, s.now().UTC()); err != nil {
		return nil, fmt.Errorf("terminal job reconciliation failed: %w", err)
	}
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) createRevision(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	actor, ok := s.actor(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required")
		return
	}
	var request struct {
		Content json.RawMessage `json:"content"`
	}
	if err := decodeJSON(w, r, &request); err != nil || !jsonObject(request.Content) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "content must be a JSON object")
		return
	}
	fingerprint := digest(request.Content)

	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, exists := s.revisionIdempotency[key]; exists {
		if previous.fingerprint != fingerprint {
			writeError(w, r, http.StatusConflict, "idempotency_conflict", "Idempotency-Key represents different input")
			return
		}
		writeJSON(w, http.StatusCreated, revisionResponse(s.revisions[previous.id]))
		return
	}
	persisted, err := s.persistence.CreateRevision(r.Context(), actor, key, fingerprint, request.Content, s.now().UTC())
	if err != nil {
		if errors.Is(err, job.ErrIdempotencyConflict) {
			writeError(w, r, http.StatusConflict, "idempotency_conflict", "Idempotency-Key represents different input")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "revision_persistence_failed", "unable to persist configuration revision")
		return
	}
	revision, err := orchestrationconfig.NewRevision(persisted.ID, persisted.Sequence, persisted.CreatedAt, persisted.Content)
	if err != nil || revision.Digest() != persisted.Digest {
		writeError(w, r, http.StatusInternalServerError, "invalid_persisted_revision", "persisted revision failed integrity validation")
		return
	}
	s.revisions[revision.ID()] = revision
	s.revisionDigests[revision.Digest()] = revision.ID()
	s.revisionIdempotency[key] = idempotencyRecord{fingerprint: fingerprint, id: revision.ID()}
	if revision.Sequence() >= s.nextRevision {
		s.nextRevision = revision.Sequence()
		s.currentRevision = revision.ID()
	}
	writeJSON(w, http.StatusCreated, revisionResponse(revision))
}

func (s *Server) listActions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.registry.List()})
}

func (s *Server) createChange(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	precondition := strings.TrimSpace(r.Header.Get("If-Match-Revision"))
	if precondition == "" {
		writeError(w, r, http.StatusPreconditionRequired, "revision_precondition_required", "If-Match-Revision is required")
		return
	}
	actor, ok := s.actor(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required")
		return
	}
	var request struct {
		Action     string          `json:"action"`
		Input      json.RawMessage `json:"input"`
		RevisionID string          `json:"revisionId"`
	}
	if err := decodeJSON(w, r, &request); err != nil || request.Action == "" || request.RevisionID == "" || !jsonObject(request.Input) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "action, revisionId, and object input are required")
		return
	}
	definition, err := s.registry.Resolve(request.Action)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "unknown_action", "action is not registered")
		return
	}
	fingerprint := digest([]byte(request.Action + "\x00" + request.RevisionID + "\x00" + string(request.Input)))

	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, exists := s.changeIdempotency[key]; exists {
		if previous.fingerprint != fingerprint {
			writeError(w, r, http.StatusConflict, "idempotency_conflict", "Idempotency-Key represents different input")
			return
		}
		record := s.changes[previous.id]
		writeJSON(w, http.StatusAccepted, viewChange(record))
		return
	}
	revision, exists := s.revisions[request.RevisionID]
	if !exists || request.RevisionID != s.currentRevision || precondition != s.currentRevision {
		writeError(w, r, http.StatusPreconditionFailed, "revision_precondition_failed", "configuration revision is no longer current")
		return
	}
	decision, err := s.evaluator.Evaluate(policy.EvaluationInput{Action: request.Action, Requester: actor, Risk: definition.Risk})
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "policy_evaluation_failed", err.Error())
		return
	}
	changeID := "chg-" + newID()
	machine, err := change.New(changeID, request.Action, actor, revision, decision, s.now().UTC())
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "change_rejected", err.Error())
		return
	}
	record := &changeRecord{machine: machine, input: append(json.RawMessage(nil), request.Input...), idempotencyKey: key}
	persisted, created, err := s.persistence.CreateChange(r.Context(), persistedChange(record, fingerprint))
	if err != nil {
		if errors.Is(err, job.ErrIdempotencyConflict) {
			writeError(w, r, http.StatusConflict, "idempotency_conflict", "Idempotency-Key represents different input")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "change_persistence_failed", "unable to persist change")
		return
	}
	if !created {
		record, err = s.restoreChange(persisted)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "invalid_persisted_change", "persisted change failed integrity validation")
			return
		}
		changeID = persisted.Snapshot.ID
	}
	s.changes[changeID] = record
	s.changeIdempotency[key] = idempotencyRecord{fingerprint: fingerprint, id: changeID}
	if record.jobID != "" && !changeStateTerminal(record.machine.Snapshot().State) {
		s.reconciliationCandidates[changeID] = record.jobID
	}
	if record.machine.Snapshot().State == change.StateApproved && record.jobID == "" {
		if err := s.enqueue(r.Context(), record); err != nil {
			if record.jobID == "" {
				delete(s.changes, changeID)
				delete(s.changeIdempotency, key)
				delete(s.reconciliationCandidates, changeID)
			}
			writeError(w, r, http.StatusInternalServerError, "job_creation_failed", "unable to create durable job")
			return
		}
	}
	writeJSON(w, http.StatusAccepted, viewChange(record))
}

func (s *Server) approveChange(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.actor(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required")
		return
	}
	versionText := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), "\"")
	version, err := strconv.ParseUint(versionText, 10, 64)
	if err != nil || version == 0 {
		writeError(w, r, http.StatusBadRequest, "invalid_version", "If-Match must be a positive change version")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.changes[r.PathValue("changeId")]
	if !exists {
		writeError(w, r, http.StatusNotFound, "change_not_found", "change was not found")
		return
	}
	if err := record.machine.Approve(policy.Approval{Actor: actor, Permissions: []string{string(rbac.PermissionChangesApprove)}, ApprovedAt: s.now().UTC()}, version, s.now().UTC()); err != nil {
		writeError(w, r, http.StatusConflict, "change_conflict", err.Error())
		return
	}
	if err := s.persistence.UpdateChange(r.Context(), persistedChange(record, s.changeIdempotency[record.idempotencyKey].fingerprint)); err != nil {
		writeError(w, r, http.StatusInternalServerError, "change_persistence_failed", "unable to persist approval")
		return
	}
	if record.machine.Snapshot().State == change.StateApproved && record.jobID == "" {
		if err := s.enqueue(r.Context(), record); err != nil {
			writeError(w, r, http.StatusInternalServerError, "job_creation_failed", "unable to create durable job")
			return
		}
	}
	writeJSON(w, http.StatusOK, viewChange(record))
}

func (s *Server) enqueue(ctx context.Context, record *changeRecord) error {
	snapshot := record.machine.Snapshot()
	if snapshot.State != change.StateApproved {
		return errors.New("change is not approved")
	}
	if err := record.machine.Transition(change.StateQueued, snapshot.Version, s.now().UTC()); err != nil {
		return err
	}
	jobID := "job-" + newID()
	created, _, err := s.jobs.Create(ctx, job.CreateRequest{
		ID: jobID, ChangeID: snapshot.ID, ActionName: snapshot.Action, Input: record.input,
		IdempotencyKey: "change:" + record.idempotencyKey, MaxAttempts: 3, Now: s.now().UTC(),
	})
	if err != nil {
		return err
	}
	record.jobID = created.ID
	s.reconciliationCandidates[snapshot.ID] = created.ID
	return s.persistence.UpdateChange(ctx, persistedChange(record, s.changeIdempotency[record.idempotencyKey].fingerprint))
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	result, err := s.jobs.Get(r.Context(), r.PathValue("jobId"))
	if errors.Is(err, job.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "job_not_found", "job was not found")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "job_read_failed", "unable to read job")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	result, err := s.jobs.RequestCancel(r.Context(), r.PathValue("jobId"), s.now().UTC())
	if errors.Is(err, job.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "job_not_found", "job was not found")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "job_cancel_failed", "unable to cancel job")
		return
	}
	if result.Status == job.StatusCancelled {
		if err := s.ReconcileJob(result, s.now().UTC()); err != nil {
			writeError(w, r, http.StatusInternalServerError, "change_reconciliation_failed", "job was cancelled but change reconciliation failed")
			return
		}
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) ReconcileJob(execution job.Job, now time.Time) error {
	return s.reconcileJob(context.Background(), execution, now)
}

func (s *Server) ReconcileTerminalJobs(ctx context.Context, now time.Time) error {
	if ctx == nil || now.IsZero() {
		return errors.New("reconciliation context and time are required")
	}
	type candidate struct {
		changeID string
		jobID    string
	}
	s.mu.RLock()
	candidates := make([]candidate, 0, len(s.reconciliationCandidates))
	for changeID, jobID := range s.reconciliationCandidates {
		candidates = append(candidates, candidate{changeID: changeID, jobID: jobID})
	}
	s.mu.RUnlock()

	var reconciliationErrors []error
	for _, candidate := range candidates {
		execution, err := s.jobs.Get(ctx, candidate.jobID)
		if err != nil {
			reconciliationErrors = append(reconciliationErrors, fmt.Errorf("read job %s for change %s: %w", candidate.jobID, candidate.changeID, err))
			continue
		}
		if !execution.Status.Terminal() {
			continue
		}
		if execution.ChangeID != candidate.changeID {
			reconciliationErrors = append(reconciliationErrors, fmt.Errorf("job %s is bound to change %s, expected %s", execution.ID, execution.ChangeID, candidate.changeID))
			continue
		}
		if err := s.reconcileJob(ctx, execution, now); err != nil {
			reconciliationErrors = append(reconciliationErrors, fmt.Errorf("reconcile job %s: %w", execution.ID, err))
		}
	}
	return errors.Join(reconciliationErrors...)
}

func (s *Server) reconcileJob(ctx context.Context, execution job.Job, now time.Time) error {
	if execution.ChangeID == "" || now.IsZero() {
		return errors.New("job change id and reconciliation time are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.changes[execution.ChangeID]
	if !exists {
		return errors.New("job references an unknown change")
	}
	_, unresolved := s.reconciliationCandidates[execution.ChangeID]
	if expected, terminal := terminalChangeState(execution.Status); terminal && record.machine.Snapshot().State == expected && !unresolved {
		return nil
	}
	persist := func() error {
		err := s.persistence.UpdateChange(ctx, persistedChange(record, s.changeIdempotency[record.idempotencyKey].fingerprint))
		if err != nil {
			s.reconciliationCandidates[execution.ChangeID] = execution.ID
			return err
		}
		if execution.Status.Terminal() {
			delete(s.reconciliationCandidates, execution.ChangeID)
		}
		return nil
	}
	transition := func(to change.State) error {
		snapshot := record.machine.Snapshot()
		if snapshot.State == to {
			return nil
		}
		return record.machine.Transition(to, snapshot.Version, now.UTC())
	}
	state := record.machine.Snapshot().State
	if execution.Status == job.StatusCancelled {
		if state != change.StateCancelled {
			if err := transition(change.StateCancelled); err != nil {
				return err
			}
		}
		return persist()
	}
	if state == change.StateQueued && execution.Status != job.StatusQueued {
		if err := transition(change.StateExecuting); err != nil {
			return err
		}
	}
	switch execution.Status {
	case job.StatusSucceeded:
		if record.machine.Snapshot().State == change.StateExecuting {
			if err := transition(change.StateVerifying); err != nil {
				return err
			}
		}
		if err := transition(change.StateSucceeded); err != nil {
			return err
		}
	case job.StatusFailed:
		if err := transition(change.StateFailed); err != nil {
			return err
		}
	case job.StatusCancelRequested, job.StatusRunning, job.StatusRetryWait, job.StatusQueued:
	default:
		return errors.New("unsupported job status")
	}
	return persist()
}

func changeStateTerminal(state change.State) bool {
	return state == change.StateSucceeded || state == change.StateFailed || state == change.StateCancelled || state == change.StateRejected
}

func terminalChangeState(status job.Status) (change.State, bool) {
	switch status {
	case job.StatusSucceeded:
		return change.StateSucceeded, true
	case job.StatusFailed:
		return change.StateFailed, true
	case job.StatusCancelled:
		return change.StateCancelled, true
	default:
		return "", false
	}
}

func persistedChange(record *changeRecord, fingerprint string) PersistedChange {
	return PersistedChange{Snapshot: record.machine.Snapshot(), Input: append(json.RawMessage(nil), record.input...), IdempotencyKey: record.idempotencyKey, Fingerprint: fingerprint, JobID: record.jobID}
}

func (s *Server) restoreChange(persisted PersistedChange) (*changeRecord, error) {
	revision, exists := s.revisions[persisted.Snapshot.RevisionID]
	if !exists {
		return nil, errors.New("persisted change references unknown revision")
	}
	machine, err := change.New(persisted.Snapshot.ID, persisted.Snapshot.Action, persisted.Snapshot.Requester, revision, persisted.Snapshot.Decision, persisted.Snapshot.UpdatedAt)
	if err != nil {
		return nil, err
	}
	for _, approval := range persisted.Snapshot.Approvals {
		if machine.Snapshot().State != change.StatePendingApproval {
			break
		}
		if err := machine.Approve(approval, machine.Snapshot().Version, persisted.Snapshot.UpdatedAt); err != nil {
			return nil, err
		}
	}
	for machine.Snapshot().State != persisted.Snapshot.State {
		current := machine.Snapshot().State
		var next change.State
		switch persisted.Snapshot.State {
		case change.StateQueued:
			next = change.StateQueued
		case change.StateExecuting:
			if current == change.StateApproved {
				next = change.StateQueued
			} else {
				next = change.StateExecuting
			}
		case change.StateVerifying, change.StateSucceeded:
			switch current {
			case change.StateApproved:
				next = change.StateQueued
			case change.StateQueued:
				next = change.StateExecuting
			case change.StateExecuting:
				next = change.StateVerifying
			default:
				next = change.StateSucceeded
			}
		case change.StateFailed:
			if current == change.StateApproved {
				next = change.StateQueued
			} else if current == change.StateQueued && persisted.Snapshot.Version-machine.Snapshot().Version > 1 {
				next = change.StateExecuting
			} else {
				next = change.StateFailed
			}
		case change.StateCancelled:
			if current == change.StateApproved && persisted.JobID != "" {
				next = change.StateQueued
			} else if current == change.StateQueued && persisted.Snapshot.Version-machine.Snapshot().Version > 1 {
				next = change.StateExecuting
			} else {
				next = change.StateCancelled
			}
		default:
			return nil, errors.New("persisted change state cannot be restored")
		}
		if err := machine.Transition(next, machine.Snapshot().Version, persisted.Snapshot.UpdatedAt); err != nil {
			return nil, err
		}
	}
	if machine.Snapshot().Version != persisted.Snapshot.Version {
		return nil, errors.New("persisted change version failed integrity validation")
	}
	return &changeRecord{machine: machine, input: append(json.RawMessage(nil), persisted.Input...), idempotencyKey: persisted.IdempotencyKey, jobID: persisted.JobID}, nil
}

func viewChange(record *changeRecord) changeView {
	snapshot := record.machine.Snapshot()
	return changeView{ID: snapshot.ID, Action: snapshot.Action, Requester: snapshot.Requester, RevisionID: snapshot.RevisionID, Risk: snapshot.Risk, State: snapshot.State, Approvals: snapshot.Approvals, Version: snapshot.Version, UpdatedAt: snapshot.UpdatedAt, JobID: record.jobID}
}

func revisionResponse(revision orchestrationconfig.Revision) revisionView {
	return revisionView{ID: revision.ID(), Sequence: revision.Sequence(), Digest: revision.Digest(), Content: revision.Content(), CreatedAt: revision.CreatedAt()}
}

func idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		writeError(w, r, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must contain 1 to 200 characters")
		return "", false
	}
	return key, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return errors.New("application/json is required")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func jsonObject(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var object map[string]any
	return decoder.Decode(&object) == nil && object != nil
}

func digest(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func newID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(value[:])
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	commonapi.WriteError(w, r, status, code, message)
}
