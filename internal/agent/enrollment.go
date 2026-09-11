package agent

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"control-center/internal/corecontracts"
)

const (
	EnrollmentContractV1 = "agent.enrollment/v1"
	EnrollmentContractV2 = "agent.enrollment/v2"

	maxCapabilities = 64
	maxRoles        = 16
)

var (
	ErrInvalidEnrollment = errors.New("invalid agent enrollment request")
	identifierPattern    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._:-]*[A-Za-z0-9])?$`)
)

// EnrollmentRequest is the declarative agent enrollment/inventory contract.
// A missing contract_version is the backwards-compatible 0.3.x v1 shape.
// Extended fields are accepted only with an explicit v2 contract version.
type EnrollmentRequest struct {
	ContractVersion      string                   `json:"contract_version,omitempty"`
	NodeID               string                   `json:"node_id"`
	Hostname             string                   `json:"hostname"`
	Capabilities         []string                 `json:"capabilities,omitempty"`
	Roles                []corecontracts.NodeRole `json:"roles,omitempty"`
	SiteID               string                   `json:"site_id,omitempty"`
	ManagementZoneID     string                   `json:"management_zone_id,omitempty"`
	CollectedAt          *time.Time               `json:"collected_at,omitempty"`
	Hardware             *HardwareInventory       `json:"hardware,omitempty"`
	NetworkInterfaces    []NetworkInterface       `json:"network_interfaces,omitempty"`
	Identity             *IdentityMetadata        `json:"identity,omitempty"`
	CapacityObservations []CapacityObservation    `json:"capacity_observations,omitempty"`
}

// EnrollmentEffects makes the normalization boundary's lack of side effects
// explicit to API consumers. In particular, reported WAN/LAN interfaces and an
// edge-gateway role never turn on forwarding, routing, NAT, or firewall rules.
type EnrollmentEffects struct {
	PersistsEnrollment bool `json:"persists_enrollment"`
	NetworkMutation    bool `json:"network_mutation"`
}

// EnrollmentNormalization is the canonical request plus deterministic
// admission preconditions. A future mutating enrollment flow must require
// Preconditions.Ready instead of treating successful parsing as admission.
type EnrollmentNormalization struct {
	EnrollmentRequest
	Preconditions EnrollmentPreconditions `json:"preconditions"`
	Effects       EnrollmentEffects       `json:"effects"`
}

// NormalizeEnrollment preserves the original v1 normalization API while also
// validating and canonicalizing the explicit v2 contract.
func NormalizeEnrollment(request EnrollmentRequest) (EnrollmentRequest, error) {
	request.ContractVersion = strings.ToLower(strings.TrimSpace(request.ContractVersion))
	request.NodeID = strings.TrimSpace(request.NodeID)
	request.Hostname = strings.TrimSpace(request.Hostname)
	if request.NodeID == "" || request.Hostname == "" {
		return EnrollmentRequest{}, invalidEnrollment("node_id and hostname are required")
	}
	if err := validateBoundedText("node_id", request.NodeID, 128); err != nil {
		return EnrollmentRequest{}, err
	}
	if err := validateBoundedText("hostname", request.Hostname, 253); err != nil {
		return EnrollmentRequest{}, err
	}

	switch request.ContractVersion {
	case "", EnrollmentContractV1:
		if hasV2EnrollmentFields(request) {
			return EnrollmentRequest{}, invalidEnrollment("extended enrollment fields require contract_version %q", EnrollmentContractV2)
		}
		capabilities, err := normalizeCapabilities(request.Capabilities, false)
		if err != nil {
			return EnrollmentRequest{}, err
		}
		request.Capabilities = capabilities
		return request, nil
	case EnrollmentContractV2:
		return normalizeEnrollmentV2(request)
	default:
		return EnrollmentRequest{}, invalidEnrollment("unsupported contract_version %q", request.ContractVersion)
	}
}

// NormalizeEnrollmentContract returns the non-mutating normalized API result.
func NormalizeEnrollmentContract(request EnrollmentRequest) (EnrollmentNormalization, error) {
	normalized, err := NormalizeEnrollment(request)
	if err != nil {
		return EnrollmentNormalization{}, err
	}
	return EnrollmentNormalization{
		EnrollmentRequest: normalized,
		Preconditions:     evaluateNormalizedEnrollmentPreconditions(normalized),
		Effects:           EnrollmentEffects{},
	}, nil
}

func normalizeEnrollmentV2(request EnrollmentRequest) (EnrollmentRequest, error) {
	if err := validateIdentifier("node_id", request.NodeID, 128); err != nil {
		return EnrollmentRequest{}, err
	}
	request.Hostname = strings.ToLower(request.Hostname)
	if err := validateHostname(request.Hostname); err != nil {
		return EnrollmentRequest{}, err
	}
	if err := validateIdentifier("site_id", request.SiteID, 128); err != nil {
		return EnrollmentRequest{}, err
	}
	if err := validateIdentifier("management_zone_id", request.ManagementZoneID, 128); err != nil {
		return EnrollmentRequest{}, err
	}
	if request.CollectedAt == nil || request.CollectedAt.IsZero() {
		return EnrollmentRequest{}, invalidEnrollment("collected_at is required for v2")
	}
	collectedAt := request.CollectedAt.UTC()
	request.CollectedAt = &collectedAt

	capabilities, err := normalizeCapabilities(request.Capabilities, true)
	if err != nil {
		return EnrollmentRequest{}, err
	}
	request.Capabilities = capabilities
	roles, err := normalizeRoles(request.Roles)
	if err != nil {
		return EnrollmentRequest{}, err
	}
	request.Roles = roles

	if request.Hardware == nil {
		return EnrollmentRequest{}, invalidEnrollment("hardware inventory is required for v2")
	}
	hardware, err := normalizeHardware(*request.Hardware)
	if err != nil {
		return EnrollmentRequest{}, err
	}
	request.Hardware = &hardware

	interfaces, err := normalizeNetworkInterfaces(request.NetworkInterfaces)
	if err != nil {
		return EnrollmentRequest{}, err
	}
	request.NetworkInterfaces = interfaces

	if request.Identity == nil {
		return EnrollmentRequest{}, invalidEnrollment("identity metadata is required for v2")
	}
	identity, err := normalizeIdentity(*request.Identity)
	if err != nil {
		return EnrollmentRequest{}, err
	}
	request.Identity = &identity

	observations, err := normalizeCapacityObservations(
		request.CapacityObservations,
		request.NodeID,
		hardware,
		interfaces,
		collectedAt,
	)
	if err != nil {
		return EnrollmentRequest{}, err
	}
	request.CapacityObservations = observations
	return request, nil
}

func normalizeCapabilities(values []string, strict bool) ([]string, error) {
	if len(values) > maxCapabilities {
		return nil, invalidEnrollment("capabilities exceeds maximum of %d", maxCapabilities)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			if strict {
				return nil, invalidEnrollment("capabilities contains an empty value")
			}
			continue
		}
		if err := validateBoundedText("capability", value, 64); err != nil {
			return nil, err
		}
		if strict && !identifierPattern.MatchString(value) {
			return nil, invalidEnrollment("capability %q is not a valid identifier", value)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeRoles(values []corecontracts.NodeRole) ([]corecontracts.NodeRole, error) {
	if len(values) == 0 {
		return nil, invalidEnrollment("at least one role is required for v2")
	}
	if len(values) > maxRoles {
		return nil, invalidEnrollment("roles exceeds maximum of %d", maxRoles)
	}
	seen := make(map[corecontracts.NodeRole]struct{}, len(values))
	result := make([]corecontracts.NodeRole, 0, len(values))
	for _, value := range values {
		role := corecontracts.NodeRole(strings.ToLower(strings.TrimSpace(string(value))))
		if !role.Valid() {
			return nil, invalidEnrollment("unsupported node role %q", value)
		}
		if _, exists := seen[role]; exists {
			continue
		}
		seen[role] = struct{}{}
		result = append(result, role)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func hasV2EnrollmentFields(request EnrollmentRequest) bool {
	return len(request.Roles) != 0 || strings.TrimSpace(request.SiteID) != "" ||
		strings.TrimSpace(request.ManagementZoneID) != "" || request.CollectedAt != nil ||
		request.Hardware != nil || len(request.NetworkInterfaces) != 0 || request.Identity != nil ||
		len(request.CapacityObservations) != 0
}

func validateIdentifier(field, value string, maximum int) error {
	if err := validateBoundedText(field, value, maximum); err != nil {
		return err
	}
	if !identifierPattern.MatchString(value) {
		return invalidEnrollment("%s is not a valid identifier", field)
	}
	return nil
}

func validateBoundedText(field, value string, maximum int) error {
	if value == "" {
		return invalidEnrollment("%s is required", field)
	}
	if len(value) > maximum {
		return invalidEnrollment("%s exceeds maximum length of %d", field, maximum)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return invalidEnrollment("%s contains a control character", field)
		}
	}
	return nil
}

func validateHostname(hostname string) error {
	if len(hostname) > 253 {
		return invalidEnrollment("hostname exceeds maximum length of 253")
	}
	for _, label := range strings.Split(hostname, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return invalidEnrollment("hostname %q is invalid", hostname)
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return invalidEnrollment("hostname %q is invalid", hostname)
			}
		}
	}
	return nil
}

func invalidEnrollment(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidEnrollment, fmt.Sprintf(format, arguments...))
}
