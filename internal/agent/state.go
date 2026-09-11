package agent

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnknownNode                      = errors.New("unknown agent node")
	ErrInvalidHeartbeat                 = errors.New("invalid agent heartbeat")
	ErrEnrollmentPersistenceUnsupported = errors.New("enrollment contract is not supported by the transitional registry")
)

type NodeState struct {
	Enrollment    EnrollmentRequest `json:"enrollment"`
	LastHeartbeat time.Time         `json:"last_heartbeat,omitempty"`
}

type MemoryRegistry struct {
	mu    sync.RWMutex
	nodes map[string]NodeState
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{nodes: make(map[string]NodeState)}
}

func (r *MemoryRegistry) Enroll(request EnrollmentRequest) (NodeState, error) {
	normalized, err := NormalizeEnrollment(request)
	if err != nil {
		return NodeState{}, err
	}
	if normalized.ContractVersion == EnrollmentContractV2 {
		return NodeState{}, ErrEnrollmentPersistenceUnsupported
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.nodes[normalized.NodeID]
	current.Enrollment = cloneEnrollment(normalized)
	r.nodes[normalized.NodeID] = current
	return cloneNodeState(current), nil
}

func (r *MemoryRegistry) Heartbeat(nodeID string, seenAt time.Time) (NodeState, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || seenAt.IsZero() {
		return NodeState{}, ErrInvalidHeartbeat
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.nodes[nodeID]
	if !ok {
		return NodeState{}, ErrUnknownNode
	}
	if !current.LastHeartbeat.IsZero() && seenAt.Before(current.LastHeartbeat) {
		return NodeState{}, ErrInvalidHeartbeat
	}
	current.LastHeartbeat = seenAt
	r.nodes[nodeID] = current
	return cloneNodeState(current), nil
}

func (r *MemoryRegistry) Get(nodeID string) (NodeState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.nodes[strings.TrimSpace(nodeID)]
	if !ok {
		return NodeState{}, false
	}
	return cloneNodeState(state), true
}

func (r *MemoryRegistry) List() []NodeState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	items := make([]NodeState, 0, len(ids))
	for _, id := range ids {
		items = append(items, cloneNodeState(r.nodes[id]))
	}
	return items
}

func cloneEnrollment(request EnrollmentRequest) EnrollmentRequest {
	request.Capabilities = append([]string(nil), request.Capabilities...)
	return request
}

func cloneNodeState(state NodeState) NodeState {
	state.Enrollment = cloneEnrollment(state.Enrollment)
	return state
}
