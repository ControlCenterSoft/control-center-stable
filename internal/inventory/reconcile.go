package inventory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// DeviceObservation is one inventory observation from a discovery source.
type DeviceObservation struct {
	DeviceID string
	Source   string
	Hostname string
	SeenAt   time.Time
}

// ReconciledDevice is the latest device view plus all contributing sources.
type ReconciledDevice struct {
	DeviceID string
	Latest   DeviceObservation
	Sources  []string
}

// ReconcileObservations merges observations by device identity deterministically.
func ReconcileObservations(observations []DeviceObservation) ([]ReconciledDevice, error) {
	type aggregate struct {
		latest  DeviceObservation
		sources map[string]struct{}
	}
	byID := map[string]*aggregate{}

	for _, observation := range observations {
		observation.DeviceID = strings.TrimSpace(observation.DeviceID)
		observation.Source = strings.TrimSpace(observation.Source)
		if observation.DeviceID == "" {
			return nil, fmt.Errorf("device id is required")
		}
		if observation.Source == "" {
			return nil, fmt.Errorf("source is required")
		}
		if observation.SeenAt.IsZero() {
			return nil, fmt.Errorf("seen time is required for %q", observation.DeviceID)
		}

		current, exists := byID[observation.DeviceID]
		if !exists {
			current = &aggregate{latest: observation, sources: map[string]struct{}{}}
			byID[observation.DeviceID] = current
		}
		current.sources[observation.Source] = struct{}{}
		if observation.SeenAt.After(current.latest.SeenAt) ||
			(observation.SeenAt.Equal(current.latest.SeenAt) && observation.Source < current.latest.Source) {
			current.latest = observation
		}
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := make([]ReconciledDevice, 0, len(ids))
	for _, id := range ids {
		current := byID[id]
		sources := make([]string, 0, len(current.sources))
		for source := range current.sources {
			sources = append(sources, source)
		}
		sort.Strings(sources)
		result = append(result, ReconciledDevice{DeviceID: id, Latest: current.latest, Sources: sources})
	}
	return result, nil
}
