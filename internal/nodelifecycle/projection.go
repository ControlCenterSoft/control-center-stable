package nodelifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrProjectionNotFound    = errors.New("node lifecycle projection not found")
	ErrProjectionUnavailable = errors.New("node lifecycle projection unavailable")
)

// Projection is the read-only lifecycle view used by query and planning APIs.
// It deliberately has no write method: state changes require a durable,
// audited Change/Job and an atomic persistence adapter.
type Projection interface {
	Get(context.Context, string) (NodeLifecycle, error)
}

// MemoryProjection is an immutable snapshot safe for concurrent reads.
type MemoryProjection struct {
	items map[string]NodeLifecycle
}

// NewMemoryProjection validates and copies an immutable lifecycle snapshot.
func NewMemoryProjection(initial []NodeLifecycle) (*MemoryProjection, error) {
	projection := &MemoryProjection{items: make(map[string]NodeLifecycle, len(initial))}
	for _, lifecycle := range initial {
		if err := Validate(lifecycle); err != nil {
			return nil, fmt.Errorf("initialize lifecycle projection: %w", err)
		}
		if _, exists := projection.items[lifecycle.ObjectID]; exists {
			return nil, fmt.Errorf("initialize lifecycle projection: duplicate node %q", lifecycle.ObjectID)
		}
		projection.items[lifecycle.ObjectID] = lifecycle
	}
	return projection, nil
}

// NewEmptyMemoryProjection returns a valid projection with no inferred node
// state. The system must not fabricate lifecycle state from incomplete legacy
// data merely to make a read endpoint non-empty.
func NewEmptyMemoryProjection() *MemoryProjection {
	return &MemoryProjection{items: make(map[string]NodeLifecycle)}
}

// Get returns a value copy of a lifecycle object.
func (p *MemoryProjection) Get(ctx context.Context, nodeID string) (NodeLifecycle, error) {
	if err := ctx.Err(); err != nil {
		return NodeLifecycle{}, err
	}
	if p == nil || p.items == nil {
		return NodeLifecycle{}, ErrProjectionUnavailable
	}
	if nodeID == "" || strings.TrimSpace(nodeID) != nodeID {
		return NodeLifecycle{}, ErrProjectionNotFound
	}
	lifecycle, exists := p.items[nodeID]
	if !exists {
		return NodeLifecycle{}, ErrProjectionNotFound
	}
	return lifecycle, nil
}
