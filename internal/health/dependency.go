package health

import (
	"fmt"
	"sort"
	"strings"
)

// ServiceDependencyState describes one service and the services it requires.
type ServiceDependencyState struct {
	Name         string
	Healthy      bool
	Dependencies []string
}

// DependencyReadiness separates services that may accept work from blocked services.
type DependencyReadiness struct {
	Ready   []string
	Blocked []string
}

// EvaluateDependencyReadiness evaluates health and dependency availability deterministically.
func EvaluateDependencyReadiness(services []ServiceDependencyState) (DependencyReadiness, error) {
	byName := make(map[string]ServiceDependencyState, len(services))
	for _, service := range services {
		name := strings.TrimSpace(service.Name)
		if name == "" {
			return DependencyReadiness{}, fmt.Errorf("service name is required")
		}
		if _, exists := byName[name]; exists {
			return DependencyReadiness{}, fmt.Errorf("duplicate service %q", name)
		}
		service.Name = name
		byName[name] = service
	}

	result := DependencyReadiness{}
	for _, service := range byName {
		ready := service.Healthy
		for _, dependency := range service.Dependencies {
			dependency = strings.TrimSpace(dependency)
			state, exists := byName[dependency]
			if !exists {
				return DependencyReadiness{}, fmt.Errorf("service %q depends on unknown service %q", service.Name, dependency)
			}
			if !state.Healthy {
				ready = false
			}
		}
		if ready {
			result.Ready = append(result.Ready, service.Name)
		} else {
			result.Blocked = append(result.Blocked, service.Name)
		}
	}

	sort.Strings(result.Ready)
	sort.Strings(result.Blocked)
	return result, nil
}
