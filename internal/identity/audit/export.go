package audit

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	CSVExportContractVersion = "audit.export.csv/v1"
	CSVExportMaxEvents       = MaxReadLimit
)

var ErrInvalidExport = errors.New("invalid audit export")

// CSVExportManifest is evidence about a bounded audit export. Source IP is
// deliberately excluded from the export payload to minimize personal data in
// files that can leave the Control Center trust boundary.
type CSVExportManifest struct {
	ContractVersion            string    `json:"contract_version"`
	GeneratedAt                time.Time `json:"generated_at"`
	EventCount                 int       `json:"event_count"`
	NewestSequenceID           int64     `json:"newest_sequence_id,omitempty"`
	OldestSequenceID           int64     `json:"oldest_sequence_id,omitempty"`
	SourceIPIncluded           bool      `json:"source_ip_included"`
	SpreadsheetFormulaEscaping bool      `json:"spreadsheet_formula_escaping"`
}

// BuildCSVExport renders one already-authorized, already-bounded audit page as
// deterministic UTF-8 CSV. The function does not read Audit storage, bypass
// RBAC, widen the query, or grant download authority. Callers must record their
// own privileged export Audit event before releasing the bytes to a client.
//
// The export is intentionally limited to MaxReadLimit events, re-redacts
// structured details defensively, omits source_ip, rejects malformed ordering,
// and neutralizes spreadsheet formula prefixes in user-controlled text cells.
func BuildCSVExport(entries []Entry, generatedAt time.Time) ([]byte, CSVExportManifest, error) {
	manifest := CSVExportManifest{
		ContractVersion:            CSVExportContractVersion,
		SourceIPIncluded:           false,
		SpreadsheetFormulaEscaping: true,
	}
	if generatedAt.IsZero() {
		return nil, CSVExportManifest{}, fmt.Errorf("%w: generated_at is required", ErrInvalidExport)
	}
	if len(entries) > CSVExportMaxEvents {
		return nil, CSVExportManifest{}, fmt.Errorf("%w: event count %d exceeds maximum %d", ErrInvalidExport, len(entries), CSVExportMaxEvents)
	}
	manifest.GeneratedAt = generatedAt.UTC()
	manifest.EventCount = len(entries)

	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	if err := writer.Write([]string{
		"sequence_id",
		"event_id",
		"occurred_at",
		"action",
		"outcome",
		"actor_id",
		"subject_id",
		"correlation_id",
		"details_json",
		"previous_hash",
		"hash",
	}); err != nil {
		return nil, CSVExportManifest{}, fmt.Errorf("%w: write header: %v", ErrInvalidExport, err)
	}

	var previousSequenceID int64
	seenSequenceIDs := make(map[int64]struct{}, len(entries))
	for index, entry := range entries {
		if entry.SequenceID <= 0 {
			return nil, CSVExportManifest{}, fmt.Errorf("%w: entry %d has invalid sequence id", ErrInvalidExport, index)
		}
		if _, exists := seenSequenceIDs[entry.SequenceID]; exists {
			return nil, CSVExportManifest{}, fmt.Errorf("%w: duplicate sequence id %d", ErrInvalidExport, entry.SequenceID)
		}
		seenSequenceIDs[entry.SequenceID] = struct{}{}
		if previousSequenceID != 0 && entry.SequenceID >= previousSequenceID {
			return nil, CSVExportManifest{}, fmt.Errorf("%w: entries must be ordered newest-first", ErrInvalidExport)
		}
		previousSequenceID = entry.SequenceID

		event := entry.Event
		if err := validateExportEvent(event); err != nil {
			return nil, CSVExportManifest{}, fmt.Errorf("%w: sequence %d: %v", ErrInvalidExport, entry.SequenceID, err)
		}
		details, err := json.Marshal(redactExportDetails(event.Details))
		if err != nil {
			return nil, CSVExportManifest{}, fmt.Errorf("%w: sequence %d details: %v", ErrInvalidExport, entry.SequenceID, err)
		}
		row := []string{
			strconv.FormatInt(entry.SequenceID, 10),
			escapeSpreadsheetFormula(event.ID),
			event.OccurredAt.UTC().Format(time.RFC3339Nano),
			escapeSpreadsheetFormula(event.Action),
			escapeSpreadsheetFormula(event.Outcome),
			escapeSpreadsheetFormula(event.ActorID),
			escapeSpreadsheetFormula(event.SubjectID),
			escapeSpreadsheetFormula(event.CorrelationID),
			string(details),
			event.PreviousHash,
			event.Hash,
		}
		if err := writer.Write(row); err != nil {
			return nil, CSVExportManifest{}, fmt.Errorf("%w: write sequence %d: %v", ErrInvalidExport, entry.SequenceID, err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, CSVExportManifest{}, fmt.Errorf("%w: flush csv: %v", ErrInvalidExport, err)
	}
	if len(entries) > 0 {
		manifest.NewestSequenceID = entries[0].SequenceID
		manifest.OldestSequenceID = entries[len(entries)-1].SequenceID
	}
	return output.Bytes(), manifest, nil
}

func validateExportEvent(event Event) error {
	if event.ID == "" || event.ID != strings.TrimSpace(event.ID) {
		return errors.New("canonical event id is required")
	}
	if event.Action == "" || event.Action != strings.TrimSpace(event.Action) {
		return errors.New("canonical action is required")
	}
	if event.Outcome == "" || event.Outcome != strings.TrimSpace(event.Outcome) {
		return errors.New("canonical outcome is required")
	}
	if event.OccurredAt.IsZero() {
		return errors.New("occurred_at is required")
	}
	if event.ActorID != strings.TrimSpace(event.ActorID) || event.SubjectID != strings.TrimSpace(event.SubjectID) || event.CorrelationID != strings.TrimSpace(event.CorrelationID) {
		return errors.New("identity fields must be canonical")
	}
	if event.PreviousHash != "" && !isLowerHexDigest(event.PreviousHash) {
		return errors.New("previous_hash must be a lowercase sha256 digest")
	}
	if !isLowerHexDigest(event.Hash) {
		return errors.New("hash must be a lowercase sha256 digest")
	}
	return nil
}

func isLowerHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func redactExportDetails(input map[string]any) map[string]any {
	redacted := Redact(input)
	return redactExportPIIMap(redacted)
}

func redactExportPIIMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	for key, value := range input {
		normalized := strings.ToLower(strings.TrimSpace(key))
		normalized = strings.NewReplacer("_", "", "-", "", ".", "").Replace(normalized)
		switch normalized {
		case "sourceip", "remoteip", "clientip", "ipaddress":
			input[key] = "[REDACTED]"
			continue
		}
		input[key] = redactExportPIIValue(value)
	}
	return input
}

func redactExportPIIValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return redactExportPIIMap(typed)
	case []any:
		for index := range typed {
			typed[index] = redactExportPIIValue(typed[index])
		}
		return typed
	default:
		return typed
	}
}

func escapeSpreadsheetFormula(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}
