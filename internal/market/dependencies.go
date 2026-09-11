package market

import (
	"fmt"
	"sort"
	"strings"
)

// ModuleDependency describes one market module and its required modules.
type ModuleDependency struct {
	Name      string
	DependsOn []string
}

// ResolveInstallOrder returns a deterministic topological install order.
func ResolveInstallOrder(modules []ModuleDependency) ([]string, error) {
	byName := make(map[string]ModuleDependency, len(modules))
	for _, module := range modules {
		name := strings.TrimSpace(module.Name)
		if name == "" {
			return nil, fmt.Errorf("module name is required")
		}
		if _, exists := byName[name]; exists {
			return nil, fmt.Errorf("duplicate module %q", name)
		}
		module.Name = name
		byName[name] = module
	}

	for _, module := range byName {
		for _, dependency := range module.DependsOn {
			dependency = strings.TrimSpace(dependency)
			if _, exists := byName[dependency]; !exists {
				return nil, fmt.Errorf("module %q depends on unknown module %q", module.Name, dependency)
			}
		}
	}

	state := make(map[string]uint8, len(byName))
	order := make([]string, 0, len(byName))
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("dependency cycle at %q", name)
		case 2:
			return nil
		}
		state[name] = 1
		dependencies := append([]string(nil), byName[name].DependsOn...)
		sort.Strings(dependencies)
		for _, dependency := range dependencies {
			if err := visit(strings.TrimSpace(dependency)); err != nil {
				return err
			}
		}
		state[name] = 2
		order = append(order, name)
		return nil
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}
