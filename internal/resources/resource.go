package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("resource not found")

type Resource struct {
	ID             string            `json:"id"`
	OrganizationID string            `json:"organization_id"`
	Kind           string            `json:"kind"`
	Name           string            `json:"name"`
	Status         string            `json:"status"`
	Labels         map[string]string `json:"labels,omitempty"`
	ObservedState  json.RawMessage   `json:"observed_state,omitempty"`
	Revision       int64             `json:"revision"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type Filter struct {
	OrganizationID string
	Kind           string
}
type Reader interface {
	List(context.Context, Filter) ([]Resource, error)
	Get(context.Context, string) (Resource, error)
	Ready(context.Context) error
}
type MemoryRegistry struct {
	mu        sync.RWMutex
	resources map[string]Resource
}

func NewMemoryRegistry(initial []Resource) (*MemoryRegistry, error) {
	registry := &MemoryRegistry{resources: make(map[string]Resource, len(initial))}
	for _, resource := range initial {
		if err := validate(resource); err != nil {
			return nil, err
		}
		if _, exists := registry.resources[resource.ID]; exists {
			return nil, fmt.Errorf("duplicate resource id %q", resource.ID)
		}
		registry.resources[resource.ID] = clone(resource)
	}
	return registry, nil
}
func (r *MemoryRegistry) List(ctx context.Context, filter Filter) ([]Resource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Resource, 0, len(r.resources))
	for _, resource := range r.resources {
		if filter.OrganizationID != "" && resource.OrganizationID != filter.OrganizationID {
			continue
		}
		if filter.Kind != "" && resource.Kind != filter.Kind {
			continue
		}
		result = append(result, clone(resource))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind == result[j].Kind {
			return result[i].ID < result[j].ID
		}
		return result[i].Kind < result[j].Kind
	})
	return result, nil
}
func (r *MemoryRegistry) Get(ctx context.Context, id string) (Resource, error) {
	if err := ctx.Err(); err != nil {
		return Resource{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	resource, ok := r.resources[id]
	if !ok {
		return Resource{}, ErrNotFound
	}
	return clone(resource), nil
}
func (r *MemoryRegistry) Ready(ctx context.Context) error { return ctx.Err() }
func LoadSnapshot(path string) ([]Resource, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read resource snapshot: %w", err)
	}
	var snapshot []Resource
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode resource snapshot: %w", err)
	}
	return snapshot, nil
}
func validate(resource Resource) error {
	if strings.TrimSpace(resource.ID) == "" {
		return errors.New("resource id must not be empty")
	}
	if strings.TrimSpace(resource.OrganizationID) == "" {
		return fmt.Errorf("resource %q organization_id must not be empty", resource.ID)
	}
	if strings.TrimSpace(resource.Kind) == "" {
		return fmt.Errorf("resource %q kind must not be empty", resource.ID)
	}
	if strings.TrimSpace(resource.Name) == "" {
		return fmt.Errorf("resource %q name must not be empty", resource.ID)
	}
	if resource.Revision < 1 {
		return fmt.Errorf("resource %q revision must be at least 1", resource.ID)
	}
	return nil
}
func clone(resource Resource) Resource {
	copyOfResource := resource
	if resource.Labels != nil {
		copyOfResource.Labels = make(map[string]string, len(resource.Labels))
		for key, value := range resource.Labels {
			copyOfResource.Labels[key] = value
		}
	}
	if resource.ObservedState != nil {
		copyOfResource.ObservedState = append(json.RawMessage(nil), resource.ObservedState...)
	}
	return copyOfResource
}
