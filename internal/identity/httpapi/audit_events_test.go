package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"control-center/internal/identity/audit"
	"control-center/internal/identity/auth"
	"control-center/internal/identity/rbac"
	"control-center/internal/identity/security"
)

type auditEventsFixture struct {
	server *Server
	log    *audit.MemoryLog
}

func newAuditEventsFixture(t *testing.T) auditEventsFixture {
	t.Helper()
	store := auth.NewMemoryStore()
	log := audit.NewMemoryLog()
	hasher := security.NewPasswordHasher()
	passwordHash, err := hasher.Hash("a secure test password")
	if err != nil {
		t.Fatal(err)
	}
	bootstrapHash, err := hasher.HashBootstrapAdminPassword()
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []auth.User{
		{ID: "auditor-1", Username: "auditor", DisplayName: "Auditor", PasswordHash: passwordHash, Enabled: true, CreatedAt: time.Now().UTC()},
		{ID: "viewer-1", Username: "viewer-audit", DisplayName: "Viewer", PasswordHash: passwordHash, Enabled: true, CreatedAt: time.Now().UTC()},
		{ID: "admin-1", Username: "admin-audit", DisplayName: "Administrator", PasswordHash: bootstrapHash, Enabled: true, PasswordChangeRequired: true, CreatedAt: time.Now().UTC()},
	} {
		if err := store.CreateUser(context.Background(), user); err != nil {
			t.Fatal(err)
		}
	}
	authService, err := auth.NewService(store, store, log, hasher, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := rbac.NewAuthorizer()
	for _, role := range rbac.BuiltinRoles() {
		if err := authorizer.RegisterRole(role); err != nil {
			t.Fatal(err)
		}
	}
	for _, binding := range []rbac.Binding{
		{SubjectID: "auditor-1", RoleName: "auditor", Scope: rbac.GlobalScope()},
		{SubjectID: "viewer-1", RoleName: "viewer", Scope: rbac.GlobalScope()},
		{SubjectID: "admin-1", RoleName: "administrator", Scope: rbac.GlobalScope()},
	} {
		if err := authorizer.Bind(binding); err != nil {
			t.Fatal(err)
		}
	}
	server, err := NewServerWithAuditExport(authService, authorizer, log, Config{})
	if err != nil {
		t.Fatal(err)
	}
	return auditEventsFixture{server: server, log: log}
}

func (f auditEventsFixture) login(t *testing.T, username, password string) *http.Cookie {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", result.Code, result.Body.String())
	}
	cookies := result.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies=%d", len(cookies))
	}
	return cookies[0]
}

func (f auditEventsFixture) get(t *testing.T, cookie *http.Cookie, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.AddCookie(cookie)
	result := httptest.NewRecorder()
	f.server.ServeHTTP(result, request)
	return result
}

func TestAuditEventsRequiresPermissionAndCurrentPassword(t *testing.T) {
	fixture := newAuditEventsFixture(t)
	viewer := fixture.login(t, "viewer-audit", "a secure test password")
	viewerResult := fixture.get(t, viewer, "/api/v1/audit/events")
	if viewerResult.Code != http.StatusForbidden || !strings.Contains(viewerResult.Body.String(), "permission_denied") {
		t.Fatalf("viewer status=%d body=%s", viewerResult.Code, viewerResult.Body.String())
	}

	admin := fixture.login(t, "admin-audit", "admin")
	adminResult := fixture.get(t, admin, "/api/v1/audit/events")
	if adminResult.Code != http.StatusForbidden || !strings.Contains(adminResult.Body.String(), "password_change_required") {
		t.Fatalf("bootstrap admin status=%d body=%s", adminResult.Code, adminResult.Body.String())
	}
}

func TestAuditEventsPaginationFilteringAndEvidence(t *testing.T) {
	fixture := newAuditEventsFixture(t)
	cookie := fixture.login(t, "auditor", "a secure test password")
	for index := 1; index <= 4; index++ {
		if err := fixture.log.Append(context.Background(), audit.Event{
			Action: "security.test", Outcome: "success", ActorID: "actor-1", SubjectID: "subject-1",
			Details: map[string]any{"index": index},
		}); err != nil {
			t.Fatal(err)
		}
	}

	firstResult := fixture.get(t, cookie, "/api/v1/audit/events?limit=2&action=security.test&outcome=success&actor_id=actor-1")
	if firstResult.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", firstResult.Code, firstResult.Body.String())
	}
	if firstResult.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", firstResult.Header().Get("Cache-Control"))
	}
	var first auditEventsResponse
	if err := json.Unmarshal(firstResult.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 2 || first.NextCursor == "" {
		t.Fatalf("first page events=%d cursor=%q", len(first.Events), first.NextCursor)
	}
	if first.Events[0].Details["index"] != float64(4) || first.Events[1].Details["index"] != float64(3) {
		t.Fatalf("unexpected first page order: %#v", first.Events)
	}

	secondResult := fixture.get(t, cookie, "/api/v1/audit/events?limit=2&action=security.test&outcome=success&actor_id=actor-1&cursor="+first.NextCursor)
	if secondResult.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", secondResult.Code, secondResult.Body.String())
	}
	var second auditEventsResponse
	if err := json.Unmarshal(secondResult.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != 2 || second.NextCursor != "" {
		t.Fatalf("second page events=%d cursor=%q", len(second.Events), second.NextCursor)
	}
	if second.Events[0].Details["index"] != float64(2) || second.Events[1].Details["index"] != float64(1) {
		t.Fatalf("unexpected second page order: %#v", second.Events)
	}

	foundEvidence := false
	for _, event := range fixture.log.Records() {
		if event.Action == "audit.events_list" && event.Outcome == "success" && event.ActorID == "auditor-1" {
			foundEvidence = true
		}
	}
	if !foundEvidence {
		t.Fatal("successful audit read did not create audit evidence")
	}
}

func TestAuditEventsRejectsAmbiguousAndUnboundedQueries(t *testing.T) {
	fixture := newAuditEventsFixture(t)
	cookie := fixture.login(t, "auditor", "a secure test password")
	for _, target := range []string{
		"/api/v1/audit/events?limit=101",
		"/api/v1/audit/events?limit=1&limit=2",
		"/api/v1/audit/events?cursor=not-base64!",
		"/api/v1/audit/events?contains=login",
	} {
		result := fixture.get(t, cookie, target)
		if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "invalid_audit_query") {
			t.Fatalf("%s status=%d body=%s", target, result.Code, result.Body.String())
		}
	}
}

type failingAuditLogger struct{}

func (failingAuditLogger) Append(context.Context, audit.Event) error {
	return errors.New("audit unavailable")
}

func TestAuditEventsDoesNotReturnDataWhenReadEvidenceCannotBeRecorded(t *testing.T) {
	fixture := newAuditEventsFixture(t)
	cookie := fixture.login(t, "auditor", "a secure test password")
	if err := fixture.log.Append(context.Background(), audit.Event{Action: "sensitive.audit.event", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	fixture.server.audit = failingAuditLogger{}

	result := fixture.get(t, cookie, "/api/v1/audit/events?action=sensitive.audit.event")
	if result.Code != http.StatusServiceUnavailable || !strings.Contains(result.Body.String(), "audit_evidence_unavailable") {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	if strings.Contains(result.Body.String(), "sensitive.audit.event") {
		t.Fatalf("response disclosed audit data after evidence failure: %s", result.Body.String())
	}
}
