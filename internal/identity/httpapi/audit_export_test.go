package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"control-center/internal/identity/audit"
)

func TestAuditEventsExportIsPermissionBoundPrivateAndAudited(t *testing.T) {
	fixture := newAuditEventsFixture(t)
	cookie := fixture.login(t, "auditor", "a secure test password")
	if err := fixture.log.Append(context.Background(), audit.Event{
		Action: "security.export-test", Outcome: "failed", ActorID: "=SUM(1,1)", SubjectID: "node-1",
		SourceIP: "203.0.113.88", Details: map[string]any{"api_token": "top-secret", "message": "visible"},
	}); err != nil {
		t.Fatal(err)
	}

	result := fixture.get(t, cookie, "/api/v1/audit/events/export?action=security.export-test&limit=10")
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	if result.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("content-type=%q", result.Header().Get("Content-Type"))
	}
	if result.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", result.Header().Get("Cache-Control"))
	}
	body := result.Body.String()
	if strings.Contains(body, "203.0.113.88") || strings.Contains(body, "top-secret") {
		t.Fatalf("export leaked sensitive data: %s", body)
	}
	if !strings.Contains(body, "[REDACTED]") || !strings.Contains(body, "'=SUM(1,1)") {
		t.Fatalf("export did not preserve redaction/formula safety: %s", body)
	}

	foundEvidence := false
	for _, event := range fixture.log.Records() {
		if event.Action == "audit.events_export" && event.Outcome == "success" && event.ActorID == "auditor-1" {
			foundEvidence = true
			if event.Details["source_ip_included"] != false {
				t.Fatalf("unexpected export evidence: %#v", event.Details)
			}
		}
	}
	if !foundEvidence {
		t.Fatal("successful audit export did not create audit evidence")
	}
}

func TestAuditEventsExportRouteRequiresPermissionAndCurrentPassword(t *testing.T) {
	fixture := newAuditEventsFixture(t)

	viewer := fixture.login(t, "viewer-audit", "a secure test password")
	viewerResult := fixture.get(t, viewer, "/api/v1/audit/events/export")
	if viewerResult.Code != http.StatusForbidden || !strings.Contains(viewerResult.Body.String(), "permission_denied") {
		t.Fatalf("viewer status=%d body=%s", viewerResult.Code, viewerResult.Body.String())
	}

	admin := fixture.login(t, "admin-audit", "admin")
	adminResult := fixture.get(t, admin, "/api/v1/audit/events/export")
	if adminResult.Code != http.StatusForbidden || !strings.Contains(adminResult.Body.String(), "password_change_required") {
		t.Fatalf("bootstrap admin status=%d body=%s", adminResult.Code, adminResult.Body.String())
	}
}

func TestAuditEventsExportDoesNotDiscloseBytesWhenEvidenceCannotBeRecorded(t *testing.T) {
	fixture := newAuditEventsFixture(t)
	cookie := fixture.login(t, "auditor", "a secure test password")
	if err := fixture.log.Append(context.Background(), audit.Event{
		Action: "security.export-sensitive", Outcome: "success", Details: map[string]any{"message": "must-not-leak"},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.server.audit = failingAuditLogger{}

	result := fixture.get(t, cookie, "/api/v1/audit/events/export?action=security.export-sensitive")
	if result.Code != http.StatusServiceUnavailable || !strings.Contains(result.Body.String(), "audit_evidence_unavailable") {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	if strings.Contains(result.Body.String(), "must-not-leak") || result.Header().Get("Content-Type") == "text/csv; charset=utf-8" {
		t.Fatalf("response disclosed export after evidence failure: headers=%v body=%s", result.Header(), result.Body.String())
	}
}

func TestAuditEventsExportRejectsAmbiguousQuery(t *testing.T) {
	fixture := newAuditEventsFixture(t)
	cookie := fixture.login(t, "auditor", "a secure test password")
	result := fixture.get(t, cookie, "/api/v1/audit/events/export?limit=10&limit=20")
	if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "invalid_audit_query") {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
}
