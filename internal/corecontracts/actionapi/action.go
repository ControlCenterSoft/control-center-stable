// Package actionapi exposes distributed core mutations as a typed
// orchestration action. It performs no host or network mutation.
package actionapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"control-center/internal/corecontracts"
	"control-center/internal/identity/rbac"
	"control-center/internal/orchestration/action"
	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/policy"
)

const ApplyActionName = "core.object.apply"

var applyInputSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":["operation","object_type","object_id","scope_id","owner_scope","document"],
  "properties":{
    "operation":{"enum":["create","replace"]},
    "object_type":{"enum":["scope","site","management-zone","network-zone","network-interface","role-assignment","desired-state","actual-state"]},
    "object_id":{"type":"string","minLength":1,"maxLength":255,"pattern":"^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$"},
    "scope_id":{"type":"string","minLength":1,"maxLength":255,"pattern":"^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$"},
    "owner_scope":{"type":"string","minLength":1,"maxLength":255,"pattern":"^[A-Za-z0-9](?:[A-Za-z0-9._:-]{0,253}[A-Za-z0-9])?$"},
    "document":{"type":"object"},
    "precondition":{
      "type":"object",
      "additionalProperties":false,
      "required":["object_id","resource_version"],
      "properties":{
        "object_id":{"type":"string","minLength":1,"maxLength":255},
        "resource_version":{"type":"string","minLength":1,"maxLength":255},
        "generation":{"type":"integer","minimum":1}
      }
    }
  },
  "allOf":[
    {"if":{"properties":{"operation":{"const":"replace"}},"required":["operation"]},"then":{"required":["precondition"]}},
    {"if":{"properties":{"operation":{"const":"create"}},"required":["operation"]},"then":{"not":{"required":["precondition"]}}}
  ]
}`)

// NewApplyAction creates the high-risk action registered in the embedded
// worker allowlist. High-risk writes require the existing Change approval flow.
func NewApplyAction(repository corecontracts.ObjectRepository) (action.Definition, error) {
	if repository == nil {
		return action.Definition{}, errors.New("distributed core object repository is required")
	}
	return action.NewTyped(
		ApplyActionName,
		string(rbac.PermissionCoreObjectsWrite),
		policy.RiskHigh,
		applyInputSchema,
		func(ctx context.Context, input corecontracts.MutationRequest) (events.Output, error) {
			invocation, ok := action.InvocationFromContext(ctx)
			if !ok {
				return events.Output{}, errors.New("worker invocation context is required")
			}
			key, err := invocation.DownstreamIdempotencyKey("distributed-core-object-store")
			if err != nil {
				return events.Output{}, err
			}
			stored, err := repository.Apply(ctx, input, key)
			if err != nil {
				return events.Output{}, err
			}
			details, err := json.Marshal(stored)
			if err != nil {
				return events.Output{}, fmt.Errorf("encode stored distributed core object: %w", err)
			}
			return events.Output{ActualStates: []events.ActualState{{
				ResourceID: stored.ObjectID,
				Kind:       "core." + string(stored.ObjectType),
				State:      events.StatePresent,
				ObservedAt: stored.UpdatedAt,
				Revision:   stored.ResourceVersion,
				Details:    details,
			}}}, nil
		},
		func(_ context.Context, input corecontracts.MutationRequest, output events.Output) error {
			if len(output.ActualStates) != 1 {
				return errors.New("distributed core action must emit one Actual State")
			}
			actual := output.ActualStates[0]
			if actual.ResourceID != input.ObjectID || actual.Kind != "core."+string(input.ObjectType) || actual.State != events.StatePresent || actual.Revision == "" {
				return errors.New("distributed core action output identity does not match input")
			}
			var stored corecontracts.StoredObject
			if err := json.Unmarshal(actual.Details, &stored); err != nil {
				return fmt.Errorf("decode distributed core action output: %w", err)
			}
			if err := corecontracts.ValidateStoredObjectEnvelope(stored); err != nil {
				return err
			}
			if stored.ObjectID != actual.ResourceID || stored.ObjectType != input.ObjectType || stored.ResourceVersion != actual.Revision || !stored.UpdatedAt.Equal(actual.ObservedAt) {
				return errors.New("distributed core action output metadata is inconsistent")
			}
			wantFingerprint, err := corecontracts.MutationFingerprint(input)
			if err != nil {
				return err
			}
			input.Document = stored.Document
			gotFingerprint, err := corecontracts.MutationFingerprint(input)
			if err != nil {
				return err
			}
			if gotFingerprint != wantFingerprint {
				return errors.New("stored distributed core document does not match input")
			}
			return nil
		},
	), nil
}
