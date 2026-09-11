package health

import "sort"

type State string

const (
	StateHealthy   State = "healthy"
	StateUnknown   State = "unknown"
	StateDegraded  State = "degraded"
	StateUnhealthy State = "unhealthy"
)

type Check struct {
	Name    string
	State   State
	Message string
}

type Summary struct {
	State  State
	Checks []Check
}

func Aggregate(checks []Check) Summary {
	if len(checks) == 0 {
		return Summary{State: StateUnknown}
	}

	ordered := append([]Check(nil), checks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })

	worst := StateHealthy
	for _, check := range ordered {
		if severity(check.State) > severity(worst) {
			worst = check.State
		}
	}
	return Summary{State: worst, Checks: ordered}
}

func severity(state State) int {
	switch state {
	case StateHealthy:
		return 0
	case StateUnknown:
		return 1
	case StateDegraded:
		return 2
	case StateUnhealthy:
		return 3
	default:
		return 1
	}
}
