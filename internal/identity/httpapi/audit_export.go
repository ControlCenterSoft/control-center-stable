package httpapi

import (
	"net/http"
	"time"

	"control-center/internal/identity/audit"
)

// auditEventsExport prepares the bounded 0.32 Audit CSV export surface. Route
// registration is intentionally separate so this source-only slice can be
// qualified before it becomes reachable from the product API.
func (s *Server) auditEventsExport(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	if s.auditReader == nil {
		writeError(w, r, http.StatusServiceUnavailable, "audit_export_unavailable", "Audit export is temporarily unavailable")
		return
	}
	query, err := parseAuditQuery(r.URL.Query())
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_audit_query", "Invalid audit query")
		return
	}
	page, err := s.auditReader.Read(r.Context(), query)
	if err != nil {
		_ = s.audit.Append(r.Context(), audit.Event{
			Action: "audit.events_export", Outcome: "failed", ActorID: principal.Identity.ID,
			SourceIP: remoteIP(r), Details: map[string]any{"reason": "read_failed"},
		})
		writeError(w, r, http.StatusServiceUnavailable, "audit_export_unavailable", "Audit export is temporarily unavailable")
		return
	}

	payload, manifest, err := audit.BuildCSVExport(page.Entries, time.Now().UTC())
	if err != nil {
		_ = s.audit.Append(r.Context(), audit.Event{
			Action: "audit.events_export", Outcome: "failed", ActorID: principal.Identity.ID,
			SourceIP: remoteIP(r), Details: map[string]any{"reason": "export_build_failed"},
		})
		writeError(w, r, http.StatusServiceUnavailable, "audit_export_unavailable", "Audit export is temporarily unavailable")
		return
	}

	if err := s.audit.Append(r.Context(), audit.Event{
		Action: "audit.events_export", Outcome: "success", ActorID: principal.Identity.ID,
		SourceIP: remoteIP(r), Details: map[string]any{
			"contract_version":             manifest.ContractVersion,
			"limit":                        query.Limit,
			"returned":                     manifest.EventCount,
			"action":                       query.Action,
			"outcome":                      query.Outcome,
			"actor_id":                     query.ActorID,
			"subject_id":                   query.SubjectID,
			"newest_sequence_id":           manifest.NewestSequenceID,
			"oldest_sequence_id":           manifest.OldestSequenceID,
			"source_ip_included":           manifest.SourceIPIncluded,
			"spreadsheet_formula_escaping": manifest.SpreadsheetFormulaEscaping,
		},
	}); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "audit_evidence_unavailable", "Audit export evidence could not be recorded")
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="control-center-audit.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}
