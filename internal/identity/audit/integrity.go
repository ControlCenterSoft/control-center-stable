package audit

import (
	"context"
	"fmt"
)

// IntegrityReport identifies the exact prefix of the append-only audit chain
// that was verified. HeadHash is empty only when the chain contains no events.
type IntegrityReport struct {
	EventsChecked int    `json:"events_checked"`
	HeadHash      string `json:"head_hash"`
}

// IntegrityChecker verifies audit evidence without returning event payloads.
// Implementations must fail closed when any event or predecessor link cannot be
// validated.
type IntegrityChecker interface {
	InspectChain(context.Context) (IntegrityReport, error)
}

func (l *MemoryLog) InspectChain(ctx context.Context) (IntegrityReport, error) {
	if l == nil {
		return IntegrityReport{}, fmt.Errorf("audit log is required")
	}
	if err := ctx.Err(); err != nil {
		return IntegrityReport{}, err
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	previousHash := ""
	for offset, event := range l.records {
		if err := ctx.Err(); err != nil {
			return IntegrityReport{}, err
		}
		if err := Verify(event, previousHash); err != nil {
			return IntegrityReport{}, fmt.Errorf("verify audit event at offset %d: %w", offset, err)
		}
		previousHash = event.Hash
	}

	return IntegrityReport{EventsChecked: len(l.records), HeadHash: previousHash}, nil
}
