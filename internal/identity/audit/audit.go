package audit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	DefaultReadLimit = 50
	MaxReadLimit     = 100
)

type Event struct {
	ID            string         `json:"id"`
	OccurredAt    time.Time      `json:"occurred_at"`
	Action        string         `json:"action"`
	Outcome       string         `json:"outcome"`
	ActorID       string         `json:"actor_id,omitempty"`
	SubjectID     string         `json:"subject_id,omitempty"`
	SourceIP      string         `json:"source_ip,omitempty"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
	PreviousHash  string         `json:"previous_hash,omitempty"`
	Hash          string         `json:"hash"`
}

type Query struct {
	Limit            int
	BeforeSequenceID int64
	Action           string
	Outcome          string
	ActorID          string
	SubjectID        string
}

type Entry struct {
	SequenceID int64
	Event      Event
}

type Page struct {
	Entries []Entry
	HasMore bool
}

type Logger interface {
	Append(context.Context, Event) error
}

type Reader interface {
	Read(context.Context, Query) (Page, error)
}

type MemoryLog struct {
	mu      sync.RWMutex
	records []Event
}

func NewMemoryLog() *MemoryLog { return &MemoryLog{} }
func (l *MemoryLog) Append(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	previousHash := ""
	if len(l.records) > 0 {
		previousHash = l.records[len(l.records)-1].Hash
	}
	event, err := Prepare(event, previousHash)
	if err != nil {
		return err
	}
	l.records = append(l.records, event)
	return nil
}

func (l *MemoryLog) Read(ctx context.Context, query Query) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	query, err := NormalizeQuery(query)
	if err != nil {
		return Page{}, err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()

	entries := make([]Entry, 0, min(query.Limit+1, len(l.records)))
	for index := len(l.records) - 1; index >= 0 && len(entries) <= query.Limit; index-- {
		sequenceID := int64(index + 1)
		if query.BeforeSequenceID > 0 && sequenceID >= query.BeforeSequenceID {
			continue
		}
		event := l.records[index]
		if !queryMatches(event, query) {
			continue
		}
		event.Details = Redact(event.Details)
		entries = append(entries, Entry{SequenceID: sequenceID, Event: event})
	}

	hasMore := len(entries) > query.Limit
	if hasMore {
		entries = entries[:query.Limit]
	}
	return Page{Entries: entries, HasMore: hasMore}, nil
}

func NormalizeQuery(query Query) (Query, error) {
	if query.Limit == 0 {
		query.Limit = DefaultReadLimit
	}
	if query.Limit < 1 || query.Limit > MaxReadLimit {
		return Query{}, fmt.Errorf("audit read limit must be between 1 and %d", MaxReadLimit)
	}
	if query.BeforeSequenceID < 0 {
		return Query{}, fmt.Errorf("audit cursor must be non-negative")
	}
	query.Action = strings.TrimSpace(query.Action)
	query.Outcome = strings.TrimSpace(query.Outcome)
	query.ActorID = strings.TrimSpace(query.ActorID)
	query.SubjectID = strings.TrimSpace(query.SubjectID)
	if len(query.Action) > 192 {
		return Query{}, fmt.Errorf("audit action filter is too long")
	}
	if len(query.Outcome) > 32 {
		return Query{}, fmt.Errorf("audit outcome filter is too long")
	}
	if len(query.ActorID) > 128 || len(query.SubjectID) > 256 {
		return Query{}, fmt.Errorf("audit identity filter is too long")
	}
	return query, nil
}

func queryMatches(event Event, query Query) bool {
	return (query.Action == "" || event.Action == query.Action) &&
		(query.Outcome == "" || event.Outcome == query.Outcome) &&
		(query.ActorID == "" || event.ActorID == query.ActorID) &&
		(query.SubjectID == "" || event.SubjectID == query.SubjectID)
}

func Prepare(event Event, previousHash string) (Event, error) {
	if strings.TrimSpace(event.Action) == "" || strings.TrimSpace(event.Outcome) == "" {
		return Event{}, fmt.Errorf("audit action and outcome are required")
	}
	if event.ID == "" {
		event.ID = randomID()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC().Truncate(time.Microsecond)
	} else {
		event.OccurredAt = event.OccurredAt.UTC().Truncate(time.Microsecond)
	}
	event.Details = Redact(event.Details)
	event.PreviousHash = previousHash
	event.Hash = hashEvent(event)
	return event, nil
}
func Verify(event Event, expectedPreviousHash string) error {
	if event.OccurredAt.IsZero() {
		return fmt.Errorf("audit timestamp is required")
	}
	canonicalTimestamp := event.OccurredAt.UTC().Truncate(time.Microsecond)
	if event.OccurredAt.Location() != time.UTC || event.OccurredAt.Nanosecond()%1_000 != 0 {
		return fmt.Errorf("audit timestamp is not canonical PostgreSQL microsecond UTC")
	}
	if !event.OccurredAt.Equal(canonicalTimestamp) {
		return fmt.Errorf("audit timestamp is not canonical")
	}
	if event.PreviousHash != expectedPreviousHash {
		return fmt.Errorf("audit previous hash mismatch")
	}
	if event.Hash == "" {
		return fmt.Errorf("audit event hash mismatch")
	}
	if hashEvent(event) == event.Hash {
		return nil
	}
	// Releases before 0.25 generated audit event IDs as 32 hex characters.
	// PostgreSQL stores the value as uuid and returns the same UUID with hyphens,
	// so reconstruct that historical representation only for hash verification.
	if legacyID, ok := legacyUnhyphenatedUUID(event.ID); ok {
		legacy := event
		legacy.ID = legacyID
		if hashEvent(legacy) == event.Hash {
			return nil
		}
	}
	return fmt.Errorf("audit event hash mismatch")
}
func (l *MemoryLog) Records() []Event {
	l.mu.RLock()
	defer l.mu.RUnlock()
	result := make([]Event, len(l.records))
	copy(result, l.records)
	return result
}

var sensitiveKey = regexp.MustCompile(`(?i)(password|passwd|secret|token|authorization|cookie|api[-_]?key|private[-_]?key|credential)`)
var bearerValue = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]+`)

func Redact(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		if sensitiveKey.MatchString(key) {
			result[key] = "[REDACTED]"
			continue
		}
		result[key] = redactValue(value)
	}
	return result
}
func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return Redact(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = redactValue(typed[i])
		}
		return out
	case string:
		return bearerValue.ReplaceAllString(typed, "Bearer [REDACTED]")
	default:
		return typed
	}
}
func hashEvent(event Event) string {
	event.Hash = ""
	payload, _ := json.Marshal(event)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("operating system random source unavailable")
	}
	encoded := hex.EncodeToString(b)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}
func legacyUnhyphenatedUUID(value string) (string, bool) {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", false
	}
	compact := strings.ReplaceAll(value, "-", "")
	if len(compact) != 32 {
		return "", false
	}
	if _, err := hex.DecodeString(compact); err != nil {
		return "", false
	}
	return compact, true
}
