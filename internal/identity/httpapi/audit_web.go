package httpapi

import (
	"bytes"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"control-center/internal/identity/audit"
)

type auditWebEvent struct {
	OccurredAt string
	Action     string
	Outcome    string
	ActorID    string
	SubjectID  string
	EventID    string
}

type auditWebPageData struct {
	DisplayName string
	Username    string
	Events      []auditWebEvent
	Action      string
	Outcome     string
	ActorID     string
	SubjectID   string
	Limit       int
	NextURL     string
	ExportURL   string
}

var auditWebTemplate = template.Must(template.New("audit-web").Parse(`<!doctype html>
<html lang="ru" data-locale="ru-RU">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Control Center — Audit</title>
<style>
:root{font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color-scheme:light dark;--s1:.35rem;--s2:.65rem;--s3:1rem;--s4:1.5rem;--radius:.8rem;--border:1px solid currentColor}*{box-sizing:border-box}body{margin:0;min-height:100vh;background:Canvas;color:CanvasText}a{color:inherit}.skip{position:absolute;left:-9999px}.skip:focus{left:var(--s3);top:var(--s3);z-index:10;background:Canvas;padding:var(--s2);border:var(--border)}.topbar{display:flex;gap:var(--s3);align-items:center;justify-content:space-between;padding:var(--s3) var(--s4);border-bottom:var(--border)}.brand{display:grid;gap:var(--s1)}.muted,.eyebrow{font-size:.85rem;opacity:.78}.content{padding:var(--s4);max-width:92rem;width:100%;margin:0 auto}.filters{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:var(--s2);padding:var(--s3);border:var(--border);border-radius:var(--radius)}label{display:grid;gap:var(--s1);font-weight:650}input,select,button{font:inherit;padding:.55rem .65rem;border:var(--border);border-radius:.55rem;background:Canvas;color:CanvasText}.actions{display:flex;gap:var(--s2);align-items:end;flex-wrap:wrap}.table-wrap{overflow-x:auto;margin-top:var(--s4);border:var(--border);border-radius:var(--radius)}table{width:100%;border-collapse:collapse;min-width:58rem}th,td{text-align:left;padding:.65rem;border-bottom:var(--border);vertical-align:top;overflow-wrap:anywhere}th{font-size:.85rem}.badge{display:inline-flex;border:var(--border);border-radius:999px;padding:.25rem .55rem;font-size:.85rem}.pager,.footer{display:flex;gap:var(--s3);align-items:center;justify-content:space-between;flex-wrap:wrap;margin-top:var(--s3)}.footer{padding-top:var(--s3);border-top:var(--border)}@media(max-width:900px){.filters{grid-template-columns:1fr 1fr}.topbar{align-items:flex-start;flex-direction:column}.content{padding:var(--s3)}}@media(max-width:560px){.filters{grid-template-columns:1fr}.table-wrap{border:0}table,thead,tbody,tr,th,td{display:block;min-width:0}thead{position:absolute;left:-9999px}tr{border:var(--border);border-radius:var(--radius);padding:var(--s2);margin-bottom:var(--s2)}td{border:0;padding:.3rem 0}td::before{content:attr(data-label) ": ";font-weight:700}}
</style>
</head>
<body>
<a class="skip" href="#main-content">К основному содержимому</a>
<header class="topbar"><div class="brand"><span class="eyebrow">Control Center</span><strong>Audit</strong></div><span class="badge">Пользователь: {{.DisplayName}}</span></header>
<main class="content" id="main-content">
<h1>Журнал Audit</h1>
<p class="muted">Read-only просмотр. Фильтры используют точное совпадение. Показана только ограниченная страница событий; значения секретов редактируются на стороне Audit storage.</p>
<form class="filters" method="get" action="/audit">
<label>Action<input name="action" value="{{.Action}}" maxlength="192" autocomplete="off"></label>
<label>Outcome<input name="outcome" value="{{.Outcome}}" maxlength="32" autocomplete="off"></label>
<label>Actor ID<input name="actor_id" value="{{.ActorID}}" maxlength="128" autocomplete="off"></label>
<label>Subject ID<input name="subject_id" value="{{.SubjectID}}" maxlength="256" autocomplete="off"></label>
<label>Лимит<select name="limit"><option value="10" {{if eq .Limit 10}}selected{{end}}>10</option><option value="25" {{if eq .Limit 25}}selected{{end}}>25</option><option value="50" {{if eq .Limit 50}}selected{{end}}>50</option><option value="100" {{if eq .Limit 100}}selected{{end}}>100</option></select></label>
<div class="actions"><button type="submit">Применить</button><a href="/audit">Сбросить</a><a href="{{.ExportURL}}">Экспорт текущей страницы CSV</a></div>
</form>
{{if .Events}}
<div class="table-wrap"><table><thead><tr><th>Время</th><th>Action</th><th>Outcome</th><th>Actor</th><th>Subject</th><th>Event ID</th></tr></thead><tbody>{{range .Events}}<tr><td data-label="Время">{{.OccurredAt}}</td><td data-label="Action">{{.Action}}</td><td data-label="Outcome">{{.Outcome}}</td><td data-label="Actor">{{if .ActorID}}{{.ActorID}}{{else}}—{{end}}</td><td data-label="Subject">{{if .SubjectID}}{{.SubjectID}}{{else}}—{{end}}</td><td data-label="Event ID">{{.EventID}}</td></tr>{{end}}</tbody></table></div>
{{else}}<p role="status">По текущему bounded-запросу событий нет.</p>{{end}}
<div class="pager">{{if .NextURL}}<a href="{{.NextURL}}">Следующая страница</a>{{else}}<span class="muted">Следующей страницы нет</span>{{end}}<span class="muted">Данные не кешируются</span></div>
<footer class="footer"><span class="muted">Учётная запись: {{.Username}}</span><form method="post" action="/web/logout"><button type="submit">Выйти</button></form></footer>
</main>
</body>
</html>`))

func (s *Server) webAudit(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	if s.auditReader == nil {
		http.Error(w, "Audit unavailable", http.StatusServiceUnavailable)
		return
	}
	query, err := parseAuditQuery(r.URL.Query())
	if err != nil {
		http.Error(w, "Invalid audit query", http.StatusBadRequest)
		return
	}
	page, err := s.auditReader.Read(r.Context(), query)
	if err != nil {
		_ = s.audit.Append(r.Context(), audit.Event{
			Action: "audit.events_web", Outcome: "failed", ActorID: principal.Identity.ID,
			SourceIP: remoteIP(r), Details: map[string]any{"reason": "read_failed"},
		})
		http.Error(w, "Audit unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.audit.Append(r.Context(), audit.Event{
		Action: "audit.events_web", Outcome: "success", ActorID: principal.Identity.ID,
		SourceIP: remoteIP(r), Details: map[string]any{
			"limit": query.Limit, "returned": len(page.Entries), "action": query.Action,
			"outcome": query.Outcome, "actor_id": query.ActorID, "subject_id": query.SubjectID,
		},
	}); err != nil {
		http.Error(w, "Audit evidence unavailable", http.StatusServiceUnavailable)
		return
	}

	events := make([]auditWebEvent, 0, len(page.Entries))
	for _, entry := range page.Entries {
		event := entry.Event
		events = append(events, auditWebEvent{
			OccurredAt: event.OccurredAt.UTC().Format(time.RFC3339),
			Action:     event.Action,
			Outcome:    event.Outcome,
			ActorID:    event.ActorID,
			SubjectID:  event.SubjectID,
			EventID:    event.ID,
		})
	}
	values := url.Values{}
	if query.Action != "" {
		values.Set("action", query.Action)
	}
	if query.Outcome != "" {
		values.Set("outcome", query.Outcome)
	}
	if query.ActorID != "" {
		values.Set("actor_id", query.ActorID)
	}
	if query.SubjectID != "" {
		values.Set("subject_id", query.SubjectID)
	}
	values.Set("limit", strconv.Itoa(query.Limit))
	exportValues := cloneURLValues(values)
	if rawCursor := r.URL.Query().Get("cursor"); rawCursor != "" {
		exportValues.Set("cursor", rawCursor)
	}
	nextURL := ""
	if page.HasMore && len(page.Entries) > 0 {
		nextValues := cloneURLValues(values)
		nextValues.Set("cursor", encodeAuditCursor(page.Entries[len(page.Entries)-1].SequenceID))
		nextURL = "/audit?" + nextValues.Encode()
	}

	var body bytes.Buffer
	if err := auditWebTemplate.Execute(&body, auditWebPageData{
		DisplayName: principal.Identity.DisplayName,
		Username:    principal.Identity.Username,
		Events:      events,
		Action:      query.Action,
		Outcome:     query.Outcome,
		ActorID:     query.ActorID,
		SubjectID:   query.SubjectID,
		Limit:       query.Limit,
		NextURL:     nextURL,
		ExportURL:   "/api/v1/audit/events/export?" + exportValues.Encode(),
	}); err != nil {
		http.Error(w, "Audit unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

func cloneURLValues(input url.Values) url.Values {
	result := make(url.Values, len(input))
	for key, values := range input {
		result[key] = append([]string(nil), values...)
	}
	return result
}
