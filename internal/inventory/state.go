package inventory

import (
	"sort"
	"strings"
	"sync"
)

type MemoryRegistry struct {
	mu      sync.RWMutex
	devices map[string]ReconciledDevice
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{devices: make(map[string]ReconciledDevice)}
}

func (r *MemoryRegistry) Ingest(observations []DeviceObservation) ([]ReconciledDevice, error) {
	reconciled, err := ReconcileObservations(observations)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, incoming := range reconciled {
		current, ok := r.devices[incoming.DeviceID]
		if !ok {
			r.devices[incoming.DeviceID] = cloneReconciled(incoming)
			continue
		}
		current.Sources = mergeStrings(current.Sources, incoming.Sources)
		if incoming.Latest.SeenAt.After(current.Latest.SeenAt) ||
			(incoming.Latest.SeenAt.Equal(current.Latest.SeenAt) && incoming.Latest.Source < current.Latest.Source) {
			current.Latest = incoming.Latest
		}
		r.devices[incoming.DeviceID] = current
	}
	return r.listLocked(), nil
}

func (r *MemoryRegistry) List() []ReconciledDevice {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.listLocked()
}

func (r *MemoryRegistry) Get(deviceID string) (ReconciledDevice, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.devices[strings.TrimSpace(deviceID)]
	if !ok {
		return ReconciledDevice{}, false
	}
	return cloneReconciled(item), true
}

func (r *MemoryRegistry) listLocked() []ReconciledDevice {
	ids := make([]string, 0, len(r.devices))
	for id := range r.devices {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	items := make([]ReconciledDevice, 0, len(ids))
	for _, id := range ids {
		items = append(items, cloneReconciled(r.devices[id]))
	}
	return items
}

func cloneReconciled(item ReconciledDevice) ReconciledDevice {
	item.Sources = append([]string(nil), item.Sources...)
	return item
}

func mergeStrings(left, right []string) []string {
	set := make(map[string]struct{}, len(left)+len(right))
	for _, value := range append(append([]string(nil), left...), right...) {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
