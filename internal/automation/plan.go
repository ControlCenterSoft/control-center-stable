package automation

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Operation string

const (
	InspectService Operation = "service.inspect"
	EnsureService  Operation = "service.ensure"
	EnsurePackage  Operation = "package.ensure"
)

type Target struct {
	ID       string `json:"id"`
	Platform string `json:"platform"`
}

type Request struct {
	Target    Target            `json:"target"`
	Operation Operation         `json:"operation"`
	Arguments map[string]string `json:"arguments"`
}

type Plan struct {
	TargetID string            `json:"targetId"`
	Adapter  string            `json:"adapter"`
	Action   Operation         `json:"action"`
	Inputs   map[string]string `json:"inputs"`
}

func BuildPlan(request Request) (Plan, error) {
	request.Target.ID = strings.TrimSpace(request.Target.ID)
	request.Target.Platform = strings.ToLower(strings.TrimSpace(request.Target.Platform))
	if request.Target.ID == "" {
		return Plan{}, errors.New("target id is required")
	}
	adapter := ""
	switch request.Target.Platform {
	case "linux":
		adapter = "ansible.linux"
	case "windows":
		adapter = "ansible.windows"
	default:
		return Plan{}, fmt.Errorf("unsupported target platform %q", request.Target.Platform)
	}
	allowed := map[Operation]map[string]bool{
		InspectService: {"name": true},
		EnsureService:  {"name": true, "state": true},
		EnsurePackage:  {"name": true, "version": true, "state": true},
	}
	keys, ok := allowed[request.Operation]
	if !ok {
		return Plan{}, fmt.Errorf("unsupported operation %q", request.Operation)
	}
	if len(request.Arguments) == 0 {
		return Plan{}, errors.New("typed operation arguments are required")
	}
	for key := range request.Arguments {
		if !keys[key] {
			return Plan{}, fmt.Errorf("argument %q is not allowed for %s", key, request.Operation)
		}
	}
	if strings.TrimSpace(request.Arguments["name"]) == "" {
		return Plan{}, errors.New("name argument is required")
	}
	inputs := make(map[string]string, len(request.Arguments))
	ordered := make([]string, 0, len(request.Arguments))
	for key := range request.Arguments {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		inputs[key] = strings.TrimSpace(request.Arguments[key])
	}
	return Plan{TargetID: request.Target.ID, Adapter: adapter, Action: request.Operation, Inputs: inputs}, nil
}
