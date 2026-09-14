package httpapi

import (
	"strings"
	"testing"
	"time"

	productui "control-center/internal/ui"
)

func TestRenderOperationalReportShowsTextualStateAndEvidenceDrawer(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	report, err := productui.BuildOperationalReport(now, productui.ReportDataLoaded, []productui.ReportEvidenceInput{
		{
			ID: "health-node-1-api", Kind: "health", ResourceKind: "node", ResourceID: "node-1",
			ObservedState: productui.ReportHealthHealthy, Freshness: productui.ReportFreshnessStale,
			ObservedAt: now.Add(-time.Hour), ReasonCode: "health.stale", RunbookRef: "runbook:node-health@1",
			EvidenceRefs: []string{"health:node-1:api"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := renderOperationalReport(&output, "0.31.0-dev", "Администратор", "admin", "Resources", report); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, required := range []string{
		`lang="ru"`, `href="#main-content"`, `id="main-content"`,
		"Операционный отчёт: Resources", "Degraded / требуется внимание",
		"stale / устарело", "Evidence Drawer", "Mutation authority", "нет",
		"health.stale", "runbook:node-health@1", "Учётная запись: admin",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("rendered Reports page missing %q", required)
		}
	}
	if strings.Contains(html, "Mutation authority</dt><dd>да") {
		t.Fatal("Reports page must never claim mutation authority")
	}
}

func TestRenderOperationalReportDoesNotPresentUnavailableAsHealthy(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	report, err := productui.BuildOperationalReport(now, productui.ReportDataUnavailable, nil)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := renderOperationalReport(&output, "0.31.0-dev", "Auditor", "auditor", "Audit", report); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	if !strings.Contains(html, "Данные отчёта недоступны") || !strings.Contains(html, "Отсутствие evidence не подменяется Healthy") {
		t.Fatal("unavailable report must render explicit fail-closed message")
	}
	if strings.Contains(html, "Healthy / подтверждено") {
		t.Fatal("unavailable report must not render Healthy status")
	}
}

func TestRenderOperationalReportEscapesProviderText(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	report, err := productui.BuildOperationalReport(now, productui.ReportDataLoaded, []productui.ReportEvidenceInput{
		{
			ID: "evidence-1", Kind: "health", ResourceKind: "node", ResourceID: `<script>alert(1)</script>`,
			ObservedState: productui.ReportHealthUnknown, Freshness: productui.ReportFreshnessCurrent,
			ObservedAt: now, ReasonCode: "health.unknown",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := renderOperationalReport(&output, "0.31.0-dev", `<b>actor</b>`, "actor", "Resources", report); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	if strings.Contains(html, `<script>alert(1)</script>`) || strings.Contains(html, `<b>actor</b>`) {
		t.Fatal("provider/user text must be HTML-escaped")
	}
	if !strings.Contains(html, `&lt;script&gt;alert(1)&lt;/script&gt;`) || !strings.Contains(html, `&lt;b&gt;actor&lt;/b&gt;`) {
		t.Fatal("expected escaped provider/user text")
	}
}
