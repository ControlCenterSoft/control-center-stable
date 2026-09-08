package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxActualStates = 256
	maxHealthItems  = 256
	maxAuditEvents  = 1024
	maxIdentitySize = 256
	maxMessageSize  = 4096
	maxDetailsSize  = 64 << 10
)

var ErrInvalidOutput = errors.New("invalid action output")

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

// Validate checks the common persistence boundary for both successful and
// failed action output. It intentionally permits partial state on failures,
// while rejecting ambiguous, unbounded, or structurally invalid evidence.
func (o Output) Validate() error {
	if len(o.ActualStates) > maxActualStates {
		return invalid("actualStates exceeds the item limit")
	}
	if len(o.Health) > maxHealthItems {
		return invalid("health exceeds the item limit")
	}
	if len(o.AuditEvents) > maxAuditEvents {
		return invalid("auditEvents exceeds the item limit")
	}

	actualIDs := make(map[string]struct{}, len(o.ActualStates))
	for index, state := range o.ActualStates {
		if err := validateText(state.ResourceID, maxIdentitySize, true); err != nil {
			return invalid("actualStates[%d].resourceId is invalid", index)
		}
		if _, duplicate := actualIDs[state.ResourceID]; duplicate {
			return invalid("actualStates contains a duplicate resourceId")
		}
		actualIDs[state.ResourceID] = struct{}{}
		if err := validateText(state.Kind, maxIdentitySize, true); err != nil {
			return invalid("actualStates[%d].kind is invalid", index)
		}
		switch state.State {
		case StatePresent, StateAbsent, StateUnknown:
		default:
			return invalid("actualStates[%d].state is invalid", index)
		}
		if state.ObservedAt.IsZero() {
			return invalid("actualStates[%d].observedAt is required", index)
		}
		if err := validateText(state.Revision, maxIdentitySize, false); err != nil {
			return invalid("actualStates[%d].revision is invalid", index)
		}
		if err := validateJSONObject(state.Details); err != nil {
			return invalid("actualStates[%d].details is invalid", index)
		}
	}

	healthIDs := make(map[string]struct{}, len(o.Health))
	for index, health := range o.Health {
		if err := validateText(health.ResourceID, maxIdentitySize, true); err != nil {
			return invalid("health[%d].resourceId is invalid", index)
		}
		if _, duplicate := healthIDs[health.ResourceID]; duplicate {
			return invalid("health contains a duplicate resourceId")
		}
		healthIDs[health.ResourceID] = struct{}{}
		switch health.Status {
		case HealthHealthy, HealthDegraded, HealthFailed, HealthUnknown:
		default:
			return invalid("health[%d].status is invalid", index)
		}
		if health.CheckedAt.IsZero() {
			return invalid("health[%d].checkedAt is required", index)
		}
		if err := validateText(health.Message, maxMessageSize, false); err != nil {
			return invalid("health[%d].message is invalid", index)
		}
	}

	auditIDs := make(map[string]struct{}, len(o.AuditEvents))
	for index, event := range o.AuditEvents {
		if err := validateText(event.ID, maxIdentitySize, true); err != nil {
			return invalid("auditEvents[%d].id is invalid", index)
		}
		if _, duplicate := auditIDs[event.ID]; duplicate {
			return invalid("auditEvents contains a duplicate id")
		}
		auditIDs[event.ID] = struct{}{}
		if event.OccurredAt.IsZero() {
			return invalid("auditEvents[%d].occurredAt is required", index)
		}
		for _, field := range []struct {
			name     string
			value    string
			required bool
		}{
			{name: "actor", value: event.Actor, required: true},
			{name: "action", value: event.Action, required: true},
			{name: "resourceId", value: event.ResourceID},
			{name: "outcome", value: event.Outcome, required: true},
			{name: "correlationId", value: event.CorrelationID, required: true},
		} {
			if err := validateText(field.value, maxIdentitySize, field.required); err != nil {
				return invalid("auditEvents[%d].%s is invalid", index, field.name)
			}
		}
		if err := validateJSONObject(event.Details); err != nil {
			return invalid("auditEvents[%d].details is invalid", index)
		}
	}
	return nil
}

// ValidateSuccessful additionally requires every observed resource to have one
// matching health observation and vice versa. Successful jobs must never
// publish a partial or falsely healthy view.
func (o Output) ValidateSuccessful() error {
	if err := o.Validate(); err != nil {
		return err
	}
	actualIDs := make(map[string]struct{}, len(o.ActualStates))
	for _, state := range o.ActualStates {
		actualIDs[state.ResourceID] = struct{}{}
	}
	healthIDs := make(map[string]struct{}, len(o.Health))
	for _, health := range o.Health {
		healthIDs[health.ResourceID] = struct{}{}
	}
	for resourceID := range actualIDs {
		if _, ok := healthIDs[resourceID]; !ok {
			return invalid("successful output is missing matching health")
		}
	}
	for resourceID := range healthIDs {
		if _, ok := actualIDs[resourceID]; !ok {
			return invalid("successful output is missing matching actual state")
		}
	}
	return nil
}

// Canonical returns an ownership-safe representation with non-nil JSON arrays,
// UTC timestamps, and copied raw JSON. It must be called only after validation.
func (o Output) Canonical() Output {
	result := Output{
		ActualStates: make([]ActualState, len(o.ActualStates)),
		Health:       make([]Health, len(o.Health)),
		AuditEvents:  make([]AuditEvent, len(o.AuditEvents)),
	}
	copy(result.ActualStates, o.ActualStates)
	copy(result.Health, o.Health)
	copy(result.AuditEvents, o.AuditEvents)
	for index := range result.ActualStates {
		result.ActualStates[index].ObservedAt = result.ActualStates[index].ObservedAt.UTC()
		result.ActualStates[index].Details = append(json.RawMessage(nil), result.ActualStates[index].Details...)
	}
	for index := range result.Health {
		result.Health[index].CheckedAt = result.Health[index].CheckedAt.UTC()
	}
	for index := range result.AuditEvents {
		result.AuditEvents[index].OccurredAt = result.AuditEvents[index].OccurredAt.UTC()
		result.AuditEvents[index].Details = append(json.RawMessage(nil), result.AuditEvents[index].Details...)
	}
	return result
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOutput, fmt.Sprintf(format, args...))
}

func validateText(value string, maximum int, required bool) error {
	if required && value == "" {
		return errors.New("required")
	}
	if value == "" {
		return nil
	}
	if len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return errors.New("invalid text")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return errors.New("control character")
		}
	}
	return nil
}

func validateJSONObject(value json.RawMessage) error {
	if len(value) == 0 {
		return nil
	}
	if len(value) > maxDetailsSize {
		return errors.New("details too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return errors.New("details must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("details contains trailing data")
	}
	return nil
}
