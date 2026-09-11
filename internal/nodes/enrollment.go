package nodes

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Capability string

const (
	CapabilityDirectoryServices Capability = "directory-services"
	CapabilityPXE               Capability = "pxe"
	CapabilityAutomation        Capability = "automation"
	CapabilityInventory         Capability = "inventory"
	CapabilityMonitoring        Capability = "monitoring"
)

type EnrollmentRequest struct {
	NodeID       string       `json:"nodeId"`
	DisplayName  string       `json:"displayName"`
	OSFamily     string       `json:"osFamily"`
	Architecture string       `json:"architecture"`
	Capabilities []Capability `json:"capabilities"`
}

type EnrollmentStep struct {
	Order  int    `json:"order"`
	Action string `json:"action"`
}

type EnrollmentPlan struct {
	NodeID       string           `json:"nodeId"`
	OSFamily     string           `json:"osFamily"`
	Architecture string           `json:"architecture"`
	Capabilities []Capability     `json:"capabilities"`
	Steps        []EnrollmentStep `json:"steps"`
}

func PlanEnrollment(request EnrollmentRequest) (EnrollmentPlan, error) {
	request.NodeID = strings.TrimSpace(request.NodeID)
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	request.OSFamily = strings.ToLower(strings.TrimSpace(request.OSFamily))
	request.Architecture = strings.ToLower(strings.TrimSpace(request.Architecture))
	if request.NodeID == "" || request.DisplayName == "" {
		return EnrollmentPlan{}, errors.New("node id and display name are required")
	}
	if request.OSFamily != "linux" && request.OSFamily != "windows" {
		return EnrollmentPlan{}, fmt.Errorf("unsupported OS family %q", request.OSFamily)
	}
	if request.Architecture != "amd64" && request.Architecture != "arm64" {
		return EnrollmentPlan{}, fmt.Errorf("unsupported architecture %q", request.Architecture)
	}
	if request.OSFamily == "windows" && request.Architecture != "amd64" {
		return EnrollmentPlan{}, errors.New("windows enrollment currently requires amd64")
	}

	capabilities, err := normalizeCapabilities(request.Capabilities)
	if err != nil {
		return EnrollmentPlan{}, err
	}
	steps := []EnrollmentStep{
		{Order: 1, Action: "validate-node-identity"},
		{Order: 2, Action: "validate-platform-compatibility"},
		{Order: 3, Action: "record-capabilities"},
		{Order: 4, Action: "create-enrollment-intent"},
	}
	return EnrollmentPlan{NodeID: request.NodeID, OSFamily: request.OSFamily, Architecture: request.Architecture, Capabilities: capabilities, Steps: steps}, nil
}

func normalizeCapabilities(values []Capability) ([]Capability, error) {
	allowed := map[Capability]bool{
		CapabilityDirectoryServices: true,
		CapabilityPXE:               true,
		CapabilityAutomation:        true,
		CapabilityInventory:         true,
		CapabilityMonitoring:        true,
	}
	seen := make(map[Capability]bool, len(values))
	result := make([]Capability, 0, len(values))
	for _, capability := range values {
		if !allowed[capability] {
			return nil, fmt.Errorf("unsupported capability %q", capability)
		}
		if !seen[capability] {
			seen[capability] = true
			result = append(result, capability)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}
