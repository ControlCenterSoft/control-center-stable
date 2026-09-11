package acceptance

import (
	"errors"
	"sort"
)

var ErrInvalidScenario = errors.New("invalid acceptance scenario")

type Scenario struct {
	Name     string
	Platform string
	Tags     []string
}

func Matrix(scenarios []Scenario) ([]Scenario, error) {
	result := make([]Scenario, 0, len(scenarios))
	seen := make(map[string]struct{}, len(scenarios))
	for _, scenario := range scenarios {
		if scenario.Name == "" || scenario.Platform == "" {
			return nil, ErrInvalidScenario
		}
		key := scenario.Platform + "\x00" + scenario.Name
		if _, ok := seen[key]; ok {
			return nil, ErrInvalidScenario
		}
		seen[key] = struct{}{}
		clone := scenario
		clone.Tags = append([]string(nil), scenario.Tags...)
		sort.Strings(clone.Tags)
		result = append(result, clone)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Platform == result[j].Platform {
			return result[i].Name < result[j].Name
		}
		return result[i].Platform < result[j].Platform
	})
	return result, nil
}
