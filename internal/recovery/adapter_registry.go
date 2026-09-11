package recovery

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrAdapterRegistryUnavailable = errors.New("recovery adapter registry is unavailable")
	ErrInvalidAdapterDescriptor   = errors.New("invalid recovery adapter descriptor")
	ErrAdapterAlreadyRegistered   = errors.New("recovery adapter is already registered")
	ErrAdapterNotRegistered       = errors.New("recovery adapter is not registered")
	ErrAdapterCapabilityMismatch  = errors.New("recovery adapter capability mismatch")
)

// AdapterDescriptor is the complete executable-free registration record for a
// recovery adapter. It deliberately has no callback, endpoint, credential,
// environment, command, or arbitrary configuration field.
type AdapterDescriptor struct {
	AdapterID    string               `json:"adapter_id"`
	Version      string               `json:"version"`
	ProviderIDs  []string             `json:"provider_ids"`
	Capabilities []ProviderCapability `json:"capabilities"`
}

// AdapterRegistry is an allowlist for recovery provider metadata. Resolution
// proves only that a provider/adapter/version/capability tuple was registered;
// it never contacts or executes the provider.
type AdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]AdapterDescriptor
}

func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{adapters: make(map[string]AdapterDescriptor)}
}

func (r *AdapterRegistry) Register(descriptor AdapterDescriptor) error {
	if r == nil {
		return ErrAdapterRegistryUnavailable
	}
	canonical, err := canonicalAdapterDescriptor(descriptor)
	if err != nil {
		return err
	}

	key := adapterRegistryKey(canonical.AdapterID, canonical.Version)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapters == nil {
		r.adapters = make(map[string]AdapterDescriptor)
	}
	if _, exists := r.adapters[key]; exists {
		return fmt.Errorf("%w: %s@%s", ErrAdapterAlreadyRegistered, canonical.AdapterID, canonical.Version)
	}
	r.adapters[key] = canonical
	return nil
}

// Resolve returns a detached descriptor only when the complete provider
// declaration is a subset of the registered allowlist and all capabilities
// required by the caller are declared on both records.
func (r *AdapterRegistry) Resolve(provider ProviderMetadata, required ...ProviderCapability) (AdapterDescriptor, error) {
	if r == nil {
		return AdapterDescriptor{}, ErrAdapterRegistryUnavailable
	}
	if err := ValidateProviderMetadata(provider); err != nil {
		return AdapterDescriptor{}, fmt.Errorf("%w: %v", ErrInvalidAdapterDescriptor, err)
	}
	requiredSet, err := canonicalCapabilities("required capabilities", required, false)
	if err != nil {
		return AdapterDescriptor{}, err
	}

	r.mu.RLock()
	descriptor, exists := r.adapters[adapterRegistryKey(provider.AdapterID, provider.Version)]
	r.mu.RUnlock()
	if !exists {
		return AdapterDescriptor{}, fmt.Errorf("%w: %s@%s", ErrAdapterNotRegistered, provider.AdapterID, provider.Version)
	}
	if !containsString(descriptor.ProviderIDs, provider.ProviderID) {
		return AdapterDescriptor{}, fmt.Errorf("%w: provider %s is not allowed by %s@%s", ErrAdapterNotRegistered, provider.ProviderID, provider.AdapterID, provider.Version)
	}
	for _, capability := range provider.Capabilities {
		if !containsCapability(descriptor.Capabilities, capability) {
			return AdapterDescriptor{}, fmt.Errorf("%w: provider declares unregistered %s", ErrAdapterCapabilityMismatch, capability)
		}
	}
	for _, capability := range requiredSet {
		if !providerHas(provider, capability) || !containsCapability(descriptor.Capabilities, capability) {
			return AdapterDescriptor{}, fmt.Errorf("%w: required %s", ErrAdapterCapabilityMismatch, capability)
		}
	}
	return cloneAdapterDescriptor(descriptor), nil
}

func (r *AdapterRegistry) List() []AdapterDescriptor {
	if r == nil {
		return []AdapterDescriptor{}
	}
	r.mu.RLock()
	result := make([]AdapterDescriptor, 0, len(r.adapters))
	for _, descriptor := range r.adapters {
		result = append(result, cloneAdapterDescriptor(descriptor))
	}
	r.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].AdapterID != result[j].AdapterID {
			return result[i].AdapterID < result[j].AdapterID
		}
		return result[i].Version < result[j].Version
	})
	return result
}

func canonicalAdapterDescriptor(descriptor AdapterDescriptor) (AdapterDescriptor, error) {
	if !identifierPattern.MatchString(descriptor.AdapterID) {
		return AdapterDescriptor{}, fmt.Errorf("%w: adapter_id must be a canonical identifier", ErrInvalidAdapterDescriptor)
	}
	if !semanticVersionPattern.MatchString(descriptor.Version) {
		return AdapterDescriptor{}, fmt.Errorf("%w: version must be semantic", ErrInvalidAdapterDescriptor)
	}
	if len(descriptor.ProviderIDs) == 0 {
		return AdapterDescriptor{}, fmt.Errorf("%w: provider_ids must not be empty", ErrInvalidAdapterDescriptor)
	}
	providers := append([]string(nil), descriptor.ProviderIDs...)
	seenProviders := make(map[string]struct{}, len(providers))
	for _, providerID := range providers {
		if !identifierPattern.MatchString(providerID) {
			return AdapterDescriptor{}, fmt.Errorf("%w: provider_ids contains an invalid identifier", ErrInvalidAdapterDescriptor)
		}
		if _, exists := seenProviders[providerID]; exists {
			return AdapterDescriptor{}, fmt.Errorf("%w: provider_ids contains duplicate %s", ErrInvalidAdapterDescriptor, providerID)
		}
		seenProviders[providerID] = struct{}{}
	}
	sort.Strings(providers)

	capabilities, err := canonicalCapabilities("capabilities", descriptor.Capabilities, true)
	if err != nil {
		return AdapterDescriptor{}, err
	}
	descriptor.ProviderIDs = providers
	descriptor.Capabilities = capabilities
	return descriptor, nil
}

func canonicalCapabilities(field string, values []ProviderCapability, required bool) ([]ProviderCapability, error) {
	if required && len(values) == 0 {
		return nil, fmt.Errorf("%w: %s must not be empty", ErrInvalidAdapterDescriptor, field)
	}
	result := append([]ProviderCapability(nil), values...)
	seen := make(map[ProviderCapability]struct{}, len(result))
	for _, capability := range result {
		if !validProviderCapability(capability) {
			return nil, fmt.Errorf("%w: %s contains unsupported %s", ErrInvalidAdapterDescriptor, field, capability)
		}
		if _, exists := seen[capability]; exists {
			return nil, fmt.Errorf("%w: %s contains duplicate %s", ErrInvalidAdapterDescriptor, field, capability)
		}
		seen[capability] = struct{}{}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	if len(result) == 0 {
		return []ProviderCapability{}, nil
	}
	return result, nil
}

func adapterRegistryKey(adapterID, version string) string {
	return adapterID + "\x00" + version
}

func cloneAdapterDescriptor(descriptor AdapterDescriptor) AdapterDescriptor {
	descriptor.ProviderIDs = cloneSlice(descriptor.ProviderIDs)
	descriptor.Capabilities = cloneSlice(descriptor.Capabilities)
	return descriptor
}

func containsString(values []string, want string) bool {
	index := sort.SearchStrings(values, want)
	return index < len(values) && values[index] == want
}

func containsCapability(values []ProviderCapability, want ProviderCapability) bool {
	index := sort.Search(len(values), func(index int) bool { return values[index] >= want })
	return index < len(values) && values[index] == want
}
