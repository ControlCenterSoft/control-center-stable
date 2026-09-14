package httpapi

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"control-center/internal/identity/audit"
)

func TestAuditWebRequiresBrowserAuthPermissionAndCurrentPassword(t *testing.T) {
	fixture := newAuditEventsFixture(t)

	anonymous := httptest.NewRecorder()
	fixture.server.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/audit", nil))
	if anonymous.Code != http.StatusSeeOther || anonymous.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous status=%d location=%q", anonymous.Code, anonymous.Header().Get("Location"))
	}

	viewer := fixture.login(t, "viewer-audit", "a secure test password")
	viewerResult := fixture.get(t, viewer, "/audit")
	if viewerResult.Code != http.StatusForbidden || strings.Contains(viewerResult.Body.String(), "permission_denied") {
		t.Fatalf("viewer status=%d body=%s", viewerResult.Code, viewerResult.Body.String())
	}

	admin := fixture.login(t, "admin-audit", "admin")
	adminResult := fixture.get(t, admin, "/audit")
	if adminResult.Code != http.StatusSeeOther || adminResult.Header().Get("Location") != "/password/change" {
		t.Fatalf("bootstrap admin status=%d location=%q body=%s", adminResult.Code, adminResult.Header().Get("Location"), adminResult.Body.String())
	}

	auditor := fixture.login(t, "auditor", "a secure test password")
	auditorResult := fixture.get(t, auditor, "/audit")
	if auditorResult.Code != http.StatusOK {
		t.Fatalf("auditor status=%d body=%s", auditorResult.Code, auditorResult.Body.String())
	}
	if auditorResult.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("content-type=%q", auditorResult.Header().Get("Content-Type"))
	}
	if auditorResult.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", auditorResult.Header().Get("Cache-Control"))
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := auditorResult.Header().Get(header); got != want {
			t.Fatalf("%s=%q want=%q", header, got, want)
		}
	}
}

func TestAuditWebFiltersPaginatesAndLinksBoundedExport(t *testing.T) {
	fixture := newAuditEventsFixture(t)
	cookie := fixture.login(t, "auditor", "a secure test password")
	for index := 1; index <= 3; index++ {
		if err := fixture.log.Append(context.Background(), audit.Event{
			Action: "security.web-test", Outcome: "success", ActorID: "actor-1", SubjectID: "subject-1",
			SourceIP: "203.0.113.88", Details: map[string]any{"message": "must-not-render", "index": index},
		}); err != nil {
			t.Fatal(err)
		}
	}

	result := fixture.get(t, cookie, "/audit?limit=2&action=security.web-test&outcome=success&actor_id=actor-1")
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	body := html.UnescapeString(result.Body.String())
	for _, want := range []string{
		"Журнал Audit",
		"security.web-test",
		"subject-1",
		"Следующая страница",
		"cursor=",
		"/api/v1/audit/events/export?",
		"action=security.web-test",
		"actor_id=actor-1",
		"limit=2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{"203.0.113.88", "must-not-render"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("body leaked %q: %s", forbidden, body)
		}
	}

	foundEvidence := false
	for _, event := range fixture.log.Records() {
		if event.Action == "audit.events_web" && event.Outcome == "success" && event.ActorID == "auditor-1" {
			foundEvidence = true
		}
	}
	if !foundEvidence {
		t.Fatal("successful Audit browser read did not create audit evidence")
	}
}

func TestAuditWebDoesNotDiscloseDataWhenEvidenceCannotBeRecorded(t *testing.T) {
	fixture := newAuditEventsFixture(t)
	cookie := fixture.login(t, "auditor", "a secure test password")
	if err := fixture.log.Append(context.Background(), audit.Event{
		Action: "sensitive.audit.web", Outcome: "success", SubjectID: "must-not-leak-subject",
	}); err != nil {
		t.Fatal(err)
	}
	fixture.server.audit = failingAuditLogger{}

	result := fixture.get(t, cookie, "/audit?action=sensitive.audit.web")
	if result.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	if strings.Contains(result.Body.String(), "sensitive.audit.web") || strings.Contains(result.Body.String(), "must-not-leak-subject") {
		t.Fatalf("response disclosed Audit data after evidence failure: %s", result.Body.String())
	}
}

func TestAuditWebRejectsAmbiguousOrUnsupportedQuery(t *testing.T) {
	fixture := newAuditEventsFixture(t)
	cookie := fixture.login(t, "auditor", "a secure test password")
	for _, target := range []string{
		"/audit?limit=10&limit=20",
		"/audit?contains=login",
		"/audit?cursor=not-base64!",
	} {
		result := fixture.get(t, cookie, target)
		if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "Invalid audit query") {
			t.Fatalf("%s status=%d body=%s", target, result.Code, result.Body.String())
		}
	}
}
