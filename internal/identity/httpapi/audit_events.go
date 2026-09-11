package httpapi

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"control-center/internal/identity/audit"
)

const auditCursorVersion = "v1:"

type auditEventsResponse struct {
	Events     []audit.Event `json:"events"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

func (s *Server) auditEvents(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	if s.auditReader == nil {
		writeError(w, r, http.StatusServiceUnavailable, "audit_read_unavailable", "Audit events are temporarily unavailable")
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
			Action: "audit.events_list", Outcome: "failed", ActorID: principal.Identity.ID,
			SourceIP: remoteIP(r), Details: map[string]any{"reason": "read_failed"},
		})
		writeError(w, r, http.StatusServiceUnavailable, "audit_read_unavailable", "Audit events are temporarily unavailable")
		return
	}

	events := make([]audit.Event, len(page.Entries))
	for index, entry := range page.Entries {
		events[index] = entry.Event
	}
	nextCursor := ""
	if page.HasMore && len(page.Entries) > 0 {
		nextCursor = encodeAuditCursor(page.Entries[len(page.Entries)-1].SequenceID)
	}
	if err := s.audit.Append(r.Context(), audit.Event{
		Action: "audit.events_list", Outcome: "success", ActorID: principal.Identity.ID,
		SourceIP: remoteIP(r), Details: map[string]any{
			"limit": query.Limit, "returned": len(events), "action": query.Action,
			"outcome": query.Outcome, "actor_id": query.ActorID, "subject_id": query.SubjectID,
		},
	}); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "audit_evidence_unavailable", "Audit read evidence could not be recorded")
		return
	}
	writeJSON(w, http.StatusOK, auditEventsResponse{Events: events, NextCursor: nextCursor})
}

func parseAuditQuery(values url.Values) (audit.Query, error) {
	allowed := map[string]struct{}{
		"limit": {}, "cursor": {}, "action": {}, "outcome": {}, "actor_id": {}, "subject_id": {},
	}
	for key, entries := range values {
		if _, ok := allowed[key]; !ok || len(entries) != 1 {
			return audit.Query{}, fmt.Errorf("unsupported or repeated audit query parameter %q", key)
		}
	}

	query := audit.Query{
		Action:    values.Get("action"),
		Outcome:   values.Get("outcome"),
		ActorID:   values.Get("actor_id"),
		SubjectID: values.Get("subject_id"),
	}
	if rawLimit := strings.TrimSpace(values.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return audit.Query{}, fmt.Errorf("invalid audit limit: %w", err)
		}
		query.Limit = limit
	}
	if rawCursor := strings.TrimSpace(values.Get("cursor")); rawCursor != "" {
		sequenceID, err := decodeAuditCursor(rawCursor)
		if err != nil {
			return audit.Query{}, err
		}
		query.BeforeSequenceID = sequenceID
	}
	return audit.NormalizeQuery(query)
}

func encodeAuditCursor(sequenceID int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(auditCursorVersion + strconv.FormatInt(sequenceID, 10)))
}

func decodeAuditCursor(cursor string) (int64, error) {
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("decode audit cursor: %w", err)
	}
	text := string(payload)
	if !strings.HasPrefix(text, auditCursorVersion) {
		return 0, fmt.Errorf("unsupported audit cursor version")
	}
	sequenceID, err := strconv.ParseInt(strings.TrimPrefix(text, auditCursorVersion), 10, 64)
	if err != nil || sequenceID <= 0 {
		return 0, fmt.Errorf("invalid audit cursor sequence")
	}
	return sequenceID, nil
}
