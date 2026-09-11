package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"control-center/internal/corecontracts"
)

const EnrollmentAdmissionContractV1 = "agent.enrollment-admission/v1"

var ErrEnrollmentAdmissionRejected = errors.New("agent enrollment admission rejected")

// EnrollmentAdmissionRequest joins the one-time bootstrap proof, the observed
// enrollment contract and the desired role assignments. It contains no token
// secret and is safe to pass to the audited orchestration boundary.
type EnrollmentAdmissionRequest struct {
	Grant       BootstrapGrant                 `json:"grant"`
	Enrollment  EnrollmentRequest              `json:"enrollment"`
	Assignments []corecontracts.RoleAssignment `json:"assignments"`
}

// EnrollmentAdmissionPlan is a deterministic, side-effect-free instruction
// for the Change/Job/Audit layer. Building it never persists enrollment,
// assigns roles or mutates host/network state.
type EnrollmentAdmissionPlan struct {
	ContractVersion        string    `json:"contract_version"`
	PlanID                 string    `json:"plan_id"`
	NodeID                 string    `json:"node_id"`
	ScopeID                string    `json:"scope_id"`
	Transport              string    `json:"transport"`
	AssignmentIDs          []string  `json:"assignment_ids"`
	RequiredActions        []string  `json:"required_actions"`
	RequiresApprovedChange bool      `json:"requires_approved_change"`
	RequiresDurableJob     bool      `json:"requires_durable_job"`
	RequiresAudit          bool      `json:"requires_audit"`
	PersistsEnrollment     bool      `json:"persists_enrollment"`
	MutatesRoles           bool      `json:"mutates_roles"`
	HostMutation           bool      `json:"host_mutation"`
	NetworkMutation        bool      `json:"network_mutation"`
	BootstrapConsumedAt    time.Time `json:"bootstrap_consumed_at"`
	EnrollmentCollectedAt  time.Time `json:"enrollment_collected_at"`
}

// BuildEnrollmentAdmissionPlan validates all cross-contract bindings and
// returns an orchestration plan. The executor must revalidate the same
// invariants atomically with PostgreSQL writes.
func BuildEnrollmentAdmissionPlan(
	request EnrollmentAdmissionRequest,
	topology corecontracts.Topology,
) (EnrollmentAdmissionPlan, error) {
	grant := request.Grant
	if strings.TrimSpace(grant.TokenID) == "" || grant.ConsumedAt.IsZero() ||
		!grant.Transport.Valid() {
		return EnrollmentAdmissionPlan{}, rejectedAdmission("consumed bootstrap grant is required")
	}

	normalized, err := NormalizeEnrollmentContract(request.Enrollment)
	if err != nil {
		return EnrollmentAdmissionPlan{}, rejectedAdmission("enrollment contract: %v", err)
	}
	if !normalized.Preconditions.Ready {
		return EnrollmentAdmissionPlan{}, rejectedAdmission("enrollment preconditions are not ready")
	}
	if grant.NodeID != normalized.NodeID {
		return EnrollmentAdmissionPlan{}, rejectedAdmission("bootstrap node does not match enrollment node")
	}
	if normalized.CollectedAt == nil || normalized.CollectedAt.After(grant.ConsumedAt) {
		return EnrollmentAdmissionPlan{}, rejectedAdmission("enrollment evidence must exist before bootstrap consumption")
	}
	if len(request.Assignments) == 0 {
		return EnrollmentAdmissionPlan{}, rejectedAdmission("at least one role assignment is required")
	}
	if err := corecontracts.ValidateRoleAssignments(request.Assignments, topology); err != nil {
		return EnrollmentAdmissionPlan{}, rejectedAdmission("role assignments: %v", err)
	}

	wantedRoles := make(map[corecontracts.NodeRole]struct{}, len(normalized.Roles))
	for _, role := range normalized.Roles {
		wantedRoles[role] = struct{}{}
	}
	assignedRoles := make(map[corecontracts.NodeRole]struct{}, len(request.Assignments))
	assignmentIDs := make([]string, 0, len(request.Assignments))
	canonicalAssignments := append([]corecontracts.RoleAssignment(nil), request.Assignments...)
	for _, assignment := range request.Assignments {
		if assignment.TargetNodeID != grant.NodeID {
			return EnrollmentAdmissionPlan{}, rejectedAdmission("assignment %q targets another node", assignment.ObjectID)
		}
		if assignment.ScopeID != grant.ScopeID {
			return EnrollmentAdmissionPlan{}, rejectedAdmission("assignment %q is outside bootstrap scope", assignment.ObjectID)
		}
		if assignment.SiteID != normalized.SiteID ||
			assignment.ManagementZoneID != normalized.ManagementZoneID {
			return EnrollmentAdmissionPlan{}, rejectedAdmission("assignment %q topology binding differs from enrollment", assignment.ObjectID)
		}
		if _, wanted := wantedRoles[assignment.Role]; !wanted {
			return EnrollmentAdmissionPlan{}, rejectedAdmission("assignment %q adds an unobserved role", assignment.ObjectID)
		}
		if _, duplicate := assignedRoles[assignment.Role]; duplicate {
			return EnrollmentAdmissionPlan{}, rejectedAdmission("role %q is assigned more than once", assignment.Role)
		}
		assignedRoles[assignment.Role] = struct{}{}
		assignmentIDs = append(assignmentIDs, assignment.ObjectID)
	}
	if len(assignedRoles) != len(wantedRoles) {
		return EnrollmentAdmissionPlan{}, rejectedAdmission("role assignments do not cover enrollment roles")
	}
	sort.Strings(assignmentIDs)
	sort.Slice(canonicalAssignments, func(i, j int) bool {
		return canonicalAssignments[i].ObjectID < canonicalAssignments[j].ObjectID
	})

	fingerprintInput := struct {
		TokenID     string                         `json:"token_id"`
		NodeID      string                         `json:"node_id"`
		ScopeID     string                         `json:"scope_id"`
		Transport   string                         `json:"transport"`
		Enrollment  EnrollmentRequest              `json:"enrollment"`
		Assignments []corecontracts.RoleAssignment `json:"assignments"`
	}{
		TokenID: grant.TokenID, NodeID: grant.NodeID, ScopeID: grant.ScopeID,
		Transport: string(grant.Transport), Enrollment: normalized.EnrollmentRequest,
		Assignments: canonicalAssignments,
	}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return EnrollmentAdmissionPlan{}, rejectedAdmission("fingerprint: %v", err)
	}
	digest := sha256.Sum256(encoded)

	return EnrollmentAdmissionPlan{
		ContractVersion:        EnrollmentAdmissionContractV1,
		PlanID:                 "eap-" + hex.EncodeToString(digest[:])[:24],
		NodeID:                 grant.NodeID,
		ScopeID:                grant.ScopeID,
		Transport:              string(grant.Transport),
		AssignmentIDs:          assignmentIDs,
		RequiredActions:        []string{"agent.enrollment.commit", "core.role-assignment.create"},
		RequiresApprovedChange: true,
		RequiresDurableJob:     true,
		RequiresAudit:          true,
		BootstrapConsumedAt:    grant.ConsumedAt.UTC(),
		EnrollmentCollectedAt:  normalized.CollectedAt.UTC(),
	}, nil
}

func rejectedAdmission(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrEnrollmentAdmissionRejected, fmt.Sprintf(format, values...))
}
