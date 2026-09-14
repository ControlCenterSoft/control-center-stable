package httpapi

import (
	"html/template"
	"io"
	"time"

	productui "control-center/internal/ui"
)

type operationalReportPageData struct {
	Version     string
	DisplayName string
	Username    string
	SourceLabel string
	Report      productui.OperationalReport
	Unavailable bool
}

var operationalReportTemplate = template.Must(template.New("reports-v1").Funcs(template.FuncMap{
	"reportState": reportStateLabel,
	"freshness":   reportFreshnessLabel,
	"observed":    formatReportTime,
}).Parse(`<!doctype html>
<html lang="ru" data-locale="ru-RU">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Control Center — Reports</title>
<style>
:root{font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color-scheme:light dark;--s1:.35rem;--s2:.65rem;--s3:1rem;--s4:1.5rem;--radius:.8rem;--border:1px solid currentColor}*{box-sizing:border-box}body{margin:0;min-height:100vh;background:Canvas;color:CanvasText}a{color:inherit}.skip{position:absolute;left:-9999px}.skip:focus{left:var(--s3);top:var(--s3);z-index:10;background:Canvas;padding:var(--s2);border:var(--border)}.topbar{display:flex;gap:var(--s3);align-items:center;justify-content:space-between;padding:var(--s3) var(--s4);border-bottom:var(--border)}.brand{display:grid;gap:var(--s1)}.muted,.eyebrow{font-size:.85rem;opacity:.78}.row{display:flex;gap:var(--s2);align-items:center;flex-wrap:wrap}.badge{display:inline-flex;border:var(--border);border-radius:999px;padding:.35rem .65rem;font-size:.85rem}.content{padding:var(--s4);max-width:86rem;width:100%;margin:0 auto}.notice,.evidence{border:var(--border);border-radius:var(--radius);padding:var(--s3)}.grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:var(--s3);margin-top:var(--s4)}.evidence h2{font-size:1.05rem;margin-top:0}.facts{display:grid;grid-template-columns:max-content 1fr;gap:var(--s2) var(--s3);margin:0}.facts dt{font-weight:700}.facts dd{margin:0;overflow-wrap:anywhere}details{margin-top:var(--s3);padding-top:var(--s2);border-top:var(--border)}summary{cursor:pointer;font-weight:700}.footer{margin-top:var(--s4);padding-top:var(--s3);border-top:var(--border);display:flex;gap:var(--s3);justify-content:space-between;flex-wrap:wrap}button{font:inherit;padding:.55rem .8rem;border:var(--border);border-radius:.55rem;background:Canvas;color:CanvasText;cursor:pointer}@media(max-width:780px){.grid{grid-template-columns:1fr}.topbar{align-items:flex-start;flex-direction:column}.content{padding:var(--s3)}}
</style>
</head>
<body>
<a class="skip" href="#main-content">К основному содержимому</a>
<header class="topbar"><div class="brand"><span class="eyebrow">Control Center</span><strong>Reports / Evidence</strong></div><div class="row"><span class="badge">Версия {{.Version}}</span><span class="badge">Пользователь: {{.DisplayName}}</span></div></header>
<main class="content" id="main-content">
<h1>Операционный отчёт: {{.SourceLabel}}</h1>
<p class="muted">Read-only evidence. Состояние отображается текстом и не кодируется только цветом. Запись события или запуск операции не считаются подтверждением Healthy/Success.</p>
{{if .Unavailable}}
<section class="notice" role="status"><strong>Данные отчёта недоступны</strong><p>Authoritative source не подтвердил текущее состояние. Отсутствие evidence не подменяется Healthy.</p></section>
{{else}}
<div class="row" aria-label="Сводка отчёта"><span class="badge">Общее состояние: {{reportState .Report.OverallState}}</span><span class="badge">Evidence: {{len .Report.Evidence}}</span><span class="badge">Сформировано: {{observed .Report.GeneratedAt}}</span></div>
{{if .Report.Evidence}}<div class="grid">{{range .Report.Evidence}}
<article class="evidence"><h2>{{.ResourceKind}} / {{.ResourceID}}</h2><dl class="facts"><dt>Состояние</dt><dd>{{reportState .EffectiveState}}</dd><dt>Наблюдалось</dt><dd>{{reportState .ObservedState}}</dd><dt>Актуальность</dt><dd>{{freshness .Freshness}}</dd><dt>Время evidence</dt><dd>{{observed .ObservedAt}}</dd><dt>Источник</dt><dd>{{.Kind}}</dd>{{if .ReasonCode}}<dt>Причина</dt><dd>{{.ReasonCode}}</dd>{{end}}{{if .RunbookRef}}<dt>Runbook</dt><dd>{{.RunbookRef}}</dd>{{end}}</dl><details><summary>Evidence Drawer</summary><dl class="facts"><dt>Evidence ID</dt><dd>{{.ID}}</dd>{{if .EvidenceDigest}}<dt>Digest</dt><dd>{{.EvidenceDigest}}</dd>{{end}}<dt>Mutation authority</dt><dd>нет</dd>{{if .EvidenceRefs}}<dt>Ссылки evidence</dt><dd>{{range $i,$ref := .EvidenceRefs}}{{if $i}} · {{end}}{{$ref}}{{end}}</dd>{{end}}</dl></details></article>
{{end}}</div>{{else}}<section class="notice" role="status"><strong>Подтверждённых записей нет</strong><p>Источник загружен и вернул пустой bounded набор evidence. Это не означает Healthy для неизвестных ресурсов.</p></section>{{end}}
{{end}}
<footer class="footer"><span class="muted">Учётная запись: {{.Username}} · Язык: русский (ru-RU)</span><form method="post" action="/web/logout"><button type="submit">Выйти</button></form></footer>
</main>
</body>
</html>`))

func renderOperationalReport(w io.Writer, version, displayName, username, sourceLabel string, report productui.OperationalReport) error {
	return operationalReportTemplate.Execute(w, operationalReportPageData{
		Version: version, DisplayName: displayName, Username: username,
		SourceLabel: sourceLabel, Report: report,
		Unavailable: report.DataState == productui.ReportDataUnavailable,
	})
}

func reportStateLabel(state productui.ReportHealthState) string {
	switch state {
	case productui.ReportHealthHealthy:
		return "Healthy / подтверждено"
	case productui.ReportHealthDegraded:
		return "Degraded / требуется внимание"
	case productui.ReportHealthUnhealthy:
		return "Unhealthy / проблема подтверждена"
	default:
		return "Unknown / нет достаточного подтверждения"
	}
}

func reportFreshnessLabel(value productui.ReportFreshness) string {
	switch value {
	case productui.ReportFreshnessCurrent:
		return "current / актуально"
	case productui.ReportFreshnessStale:
		return "stale / устарело"
	case productui.ReportFreshnessExpired:
		return "expired / просрочено"
	default:
		return "unavailable / недоступно"
	}
}

func formatReportTime(value time.Time) string {
	if value.IsZero() {
		return "нет подтверждённого времени"
	}
	return value.UTC().Format(time.RFC3339)
}
