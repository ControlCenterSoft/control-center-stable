package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"control-center/internal/identity/audit"
)

// AuditReportBinding binds one already-authorized Audit entry to the exact
// resource identity used by the common operational report contract. The
// binding is intentionally explicit: Audit SubjectID alone does not carry a
// trustworthy resource kind, and the adapter must never guess it.
type AuditReportBinding struct {
	SequenceID   int64
	EventID      string
	EventHash    string
	ResourceKind string
	ResourceID   string
}

// BuildAuditOperationalReport converts one complete bounded Audit page into
// the common read-only report contract. Every entry must have an exact binding
// to its sequence/event/hash and SubjectID. A partial page is rejected so a
// truncated history cannot be presented as the complete Audit state.
//
// Audit evidence is deliberately never Healthy by itself: a successful Audit
// event proves that an action was recorded, not that the resource is healthy.
// Failed/error outcomes are degraded; other supported outcomes remain unknown.
func BuildAuditOperationalReport(now time.Time, page audit.Page, bindings []AuditReportBinding) (OperationalReport, error) {
	if page.HasMore {
		return OperationalReport{}, errors.New("audit report requires a complete bounded page")
	}
	if len(page.Entries) != len(bindings) {
		return OperationalReport{}, errors.New("audit report requires one exact resource binding per entry")
	}
	if len(page.Entries) > MaxReportItems {
		return OperationalReport{}, fmt.Errorf("audit evidence count exceeds %d", MaxReportItems)
	}

	bindingBySequence := make(map[int64]AuditReportBinding, len(bindings))
	for _, binding := range bindings {
		if binding.SequenceID <= 0 {
			return OperationalReport{}, errors.New("audit binding sequence_id must be positive")
		}
		if _, exists := bindingBySequence[binding.SequenceID]; exists {
			return OperationalReport{}, fmt.Errorf("duplicate audit binding sequence_id %d", binding.SequenceID)
		}
		if err := validateReportBoundedText("audit binding event_id", binding.EventID, 1, 128); err != nil {
			return OperationalReport{}, err
		}
		if !validAuditEventHash(binding.EventHash) {
			return OperationalReport{}, errors.New("audit binding event_hash must be 64 lowercase hex")
		}
		if err := validateReportBoundedText("audit binding resource_kind", binding.ResourceKind, 1, 64); err != nil {
			return OperationalReport{}, err
		}
		if err := validateReportBoundedText("audit binding resource_id", binding.ResourceID, 1, MaxReportText); err != nil {
			return OperationalReport{}, err
		}
		bindingBySequence[binding.SequenceID] = binding
	}

	seenEntries := make(map[int64]struct{}, len(page.Entries))
	inputs := make([]ReportEvidenceInput, 0, len(page.Entries))
	for _, entry := range page.Entries {
		if entry.SequenceID <= 0 {
			return OperationalReport{}, errors.New("audit entry sequence_id must be positive")
		}
		if _, exists := seenEntries[entry.SequenceID]; exists {
			return OperationalReport{}, fmt.Errorf("duplicate audit entry sequence_id %d", entry.SequenceID)
		}
		seenEntries[entry.SequenceID] = struct{}{}

		binding, ok := bindingBySequence[entry.SequenceID]
		if !ok {
			return OperationalReport{}, fmt.Errorf("audit entry %d has no exact resource binding", entry.SequenceID)
		}
		event := entry.Event
		if err := validateReportBoundedText("audit event_id", event.ID, 1, 128); err != nil {
			return OperationalReport{}, err
		}
		if err := validateReportBoundedText("audit action", event.Action, 1, 192); err != nil {
			return OperationalReport{}, err
		}
		if err := validateReportBoundedText("audit outcome", event.Outcome, 1, 32); err != nil {
			return OperationalReport{}, err
		}
		if err := validateReportBoundedText("audit subject_id", event.SubjectID, 1, MaxReportText); err != nil {
			return OperationalReport{}, fmt.Errorf("audit entry %d is not resource-bound: %w", entry.SequenceID, err)
		}
		if event.OccurredAt.IsZero() {
			return OperationalReport{}, fmt.Errorf("audit entry %d occurred_at is required", entry.SequenceID)
		}
		if !validAuditEventHash(event.Hash) {
			return OperationalReport{}, fmt.Errorf("audit entry %d hash must be 64 lowercase hex", entry.SequenceID)
		}
		if binding.EventID != event.ID || binding.EventHash != event.Hash {
			return OperationalReport{}, fmt.Errorf("audit entry %d binding identity mismatch", entry.SequenceID)
		}
		if strings.TrimSpace(binding.ResourceID) != strings.TrimSpace(event.SubjectID) {
			return OperationalReport{}, fmt.Errorf("audit entry %d subject/resource binding mismatch", entry.SequenceID)
		}

		state, err := auditOutcomeReportState(event.Outcome)
		if err != nil {
			return OperationalReport{}, fmt.Errorf("audit entry %d: %w", entry.SequenceID, err)
		}
		inputs = append(inputs, ReportEvidenceInput{
			ID:             auditReportEvidenceID(entry, binding),
			Kind:           "audit",
			ResourceKind:   strings.TrimSpace(binding.ResourceKind),
			ResourceID:     strings.TrimSpace(binding.ResourceID),
			ObservedState:  state,
			Freshness:      ReportFreshnessCurrent,
			ObservedAt:     event.OccurredAt,
			ReasonCode:     "audit." + event.Outcome,
			EvidenceRefs:   []string{"audit:" + event.ID},
			EvidenceDigest: "sha256:" + event.Hash,
		})
	}

	return BuildOperationalReport(now, ReportDataLoaded, inputs)
}

func auditOutcomeReportState(outcome string) (ReportHealthState, error) {
	switch strings.TrimSpace(outcome) {
	case "failed", "error":
		return ReportHealthDegraded, nil
	case "success", "denied", "rejected", "blocked", "cancelled":
		return ReportHealthUnknown, nil
	default:
		return "", fmt.Errorf("unsupported audit outcome %q", outcome)
	}
}

func auditReportEvidenceID(entry audit.Entry, binding AuditReportBinding) string {
	parts := []string{
		strconv.FormatInt(entry.SequenceID, 10),
		entry.Event.ID,
		entry.Event.Hash,
		strings.TrimSpace(binding.ResourceKind),
		strings.TrimSpace(binding.ResourceID),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "audit:" + hex.EncodeToString(sum[:])
}

func validAuditEventHash(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
