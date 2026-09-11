package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"control-center/internal/identity/audit"
)

type failingIntegrityEvidenceLog struct {
	*audit.MemoryLog
}

func (l *failingIntegrityEvidenceLog) Append(ctx context.Context, event audit.Event) error {
	if event.Action == "audit.integrity_check" {
		return errors.New("synthetic audit evidence failure")
	}
	return l.MemoryLog.Append(ctx, event)
}

func TestAuditIntegrityEndpointFailsClosedWhenSuccessEvidenceCannotBePersisted(t *testing.T) {
	f := newAuditIntegrityFixture(t)
	cookie := f.login(t, "auditor", "a secure test password")

	f.server.audit = &failingIntegrityEvidenceLog{MemoryLog: f.log}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/integrity", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	f.server.ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), "audit_unavailable") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "synthetic audit evidence failure") {
		t.Fatalf("response leaked audit persistence error: %s", res.Body.String())
	}
	for _, event := range f.log.Records() {
		if event.Action == "audit.integrity_check" {
			t.Fatalf("failed integrity evidence was unexpectedly persisted: %#v", event)
		}
	}
}

func TestMemoryAuditIntegrityEmptyChainReportIsExplicitlyEmpty(t *testing.T) {
	log := audit.NewMemoryLog()
	report, err := log.InspectChain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.EventsChecked != 0 || report.HeadHash != "" {
		t.Fatalf("empty report=%#v, want zero events and empty head hash", report)
	}
}
