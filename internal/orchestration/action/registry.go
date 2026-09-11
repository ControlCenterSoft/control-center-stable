package action

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"

	"control-center/internal/orchestration/events"
	"control-center/internal/orchestration/policy"
)

type invocationContextKey struct{}

type Invocation struct {
	JobID          string `json:"jobId"`
	ChangeID       string `json:"changeId"`
	ActionName     string `json:"actionName"`
	IdempotencyKey string `json:"idempotencyKey"`
	Attempt        int    `json:"attempt"`
}

func (i Invocation) DownstreamIdempotencyKey(operation string) (string, error) {
	if i.JobID == "" || i.ChangeID == "" || i.ActionName == "" || i.IdempotencyKey == "" || operation == "" {
		return "", errors.New("complete invocation and downstream operation are required")
	}
	sum := sha256.Sum256([]byte(i.IdempotencyKey + "\x00" + i.ActionName + "\x00" + operation))
	return "cc-" + hex.EncodeToString(sum[:]), nil
}

func WithInvocation(ctx context.Context, invocation Invocation) context.Context {
	return context.WithValue(ctx, invocationContextKey{}, invocation)
}

func InvocationFromContext(ctx context.Context) (Invocation, bool) {
	invocation, ok := ctx.Value(invocationContextKey{}).(Invocation)
	return invocation, ok
}

var (
	ErrNotRegistered     = errors.New("action is not registered")
	ErrAlreadyRegistered = errors.New("action is already registered")
	ErrInvalidInput      = errors.New("invalid action input")
)

type Definition struct {
	Name        string          `json:"name"`
	Permission  string          `json:"permission"`
	Risk        policy.Risk     `json:"risk"`
	InputSchema json.RawMessage `json:"inputSchema"`
	execute     func(context.Context, json.RawMessage) (events.Output, error)
	verify      func(context.Context, json.RawMessage, events.Output) error
}

func (d Definition) Execute(ctx context.Context, input json.RawMessage) (events.Output, error) {
	if d.execute == nil {
		return events.Output{}, errors.New("action executor is not configured")
	}
	return d.execute(ctx, input)
}

func (d Definition) Verify(ctx context.Context, input json.RawMessage, output events.Output) error {
	if d.verify == nil {
		return errors.New("action verifier is not configured")
	}
	return d.verify(ctx, input, output)
}

type Registry struct {
	mu      sync.RWMutex
	actions map[string]Definition
}

func NewRegistry() *Registry { return &Registry{actions: make(map[string]Definition)} }

func (r *Registry) Register(definition Definition) error {
	if definition.Name == "" || definition.Permission == "" || !definition.Risk.Valid() {
		return errors.New("action name, permission, and valid risk are required")
	}
	if len(definition.InputSchema) == 0 || !json.Valid(definition.InputSchema) {
		return errors.New("action input schema must be valid JSON")
	}
	if definition.execute == nil || definition.verify == nil {
		return errors.New("action executor and verifier are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.actions[definition.Name]; exists {
		return fmt.Errorf("%w: %s", ErrAlreadyRegistered, definition.Name)
	}
	definition.InputSchema = append(json.RawMessage(nil), definition.InputSchema...)
	r.actions[definition.Name] = definition
	return nil
}

func (r *Registry) Resolve(name string) (Definition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	definition, ok := r.actions[name]
	if !ok {
		return Definition{}, fmt.Errorf("%w: %s", ErrNotRegistered, name)
	}
	definition.InputSchema = append(json.RawMessage(nil), definition.InputSchema...)
	return definition, nil
}

func (r *Registry) List() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Definition, 0, len(r.actions))
	for _, definition := range r.actions {
		definition.InputSchema = append(json.RawMessage(nil), definition.InputSchema...)
		result = append(result, definition)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func NewTyped[T any](name, permission string, risk policy.Risk, inputSchema json.RawMessage, execute func(context.Context, T) (events.Output, error), verify func(context.Context, T, events.Output) error) Definition {
	decode := func(raw json.RawMessage) (T, error) {
		var input T
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return input, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return input, fmt.Errorf("%w: trailing JSON value", ErrInvalidInput)
		}
		return input, nil
	}
	return Definition{
		Name: name, Permission: permission, Risk: risk, InputSchema: inputSchema,
		execute: func(ctx context.Context, raw json.RawMessage) (events.Output, error) {
			input, err := decode(raw)
			if err != nil {
				return events.Output{}, err
			}
			return execute(ctx, input)
		},
		verify: func(ctx context.Context, raw json.RawMessage, output events.Output) error {
			input, err := decode(raw)
			if err != nil {
				return err
			}
			return verify(ctx, input, output)
		},
	}
}
