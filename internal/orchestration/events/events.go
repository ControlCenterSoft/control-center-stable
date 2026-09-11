package events

import (
	"encoding/json"
	"time"
)

type ResourceState string

const (
	StatePresent ResourceState = "present"
	StateAbsent  ResourceState = "absent"
	StateUnknown ResourceState = "unknown"
)

type ActualState struct {
	ResourceID string          `json:"resourceId"`
	Kind       string          `json:"kind"`
	State      ResourceState   `json:"state"`
	ObservedAt time.Time       `json:"observedAt"`
	Revision   string          `json:"revision,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
}
type HealthStatus string

const (
	HealthHealthy  HealthStatus = "healthy"
	HealthDegraded HealthStatus = "degraded"
	HealthFailed   HealthStatus = "failed"
	HealthUnknown  HealthStatus = "unknown"
)

type Health struct {
	ResourceID string       `json:"resourceId"`
	Status     HealthStatus `json:"status"`
	CheckedAt  time.Time    `json:"checkedAt"`
	Message    string       `json:"message,omitempty"`
}
type AuditEvent struct {
	ID            string          `json:"id"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Actor         string          `json:"actor"`
	Action        string          `json:"action"`
	ResourceID    string          `json:"resourceId,omitempty"`
	Outcome       string          `json:"outcome"`
	CorrelationID string          `json:"correlationId"`
	Details       json.RawMessage `json:"details,omitempty"`
}
type Output struct {
	ActualStates []ActualState `json:"actualStates"`
	Health       []Health      `json:"health"`
	AuditEvents  []AuditEvent  `json:"auditEvents"`
}
