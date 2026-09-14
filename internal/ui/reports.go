package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	OperationalReportSchema = "ui.operational-report/v1"
	EvidenceDrawerSchema    = "ui.evidence-drawer/v1"

	MaxReportItems        = 1000
	MaxReportEvidenceRefs = 32
	MaxReportText         = 256
	MaxReportReasonCode   = 96
)

type ReportDataState string

const (
	ReportDataLoaded      ReportDataState = "loaded"
	ReportDataUnavailable ReportDataState = "unavailable"
)

type ReportHealthState string

const (
	ReportHealthHealthy   ReportHealthState = "healthy"
	ReportHealthUnknown   ReportHealthState = "unknown"
	ReportHealthDegraded  ReportHealthState = "degraded"
	ReportHealthUnhealthy ReportHealthState = "unhealthy"
)

type ReportFreshness string

const (
	ReportFreshnessCurrent     ReportFreshness = "current"
	ReportFreshnessStale       ReportFreshness = "stale"
	ReportFreshnessExpired     ReportFreshness = "expired"
	ReportFreshnessUnavailable ReportFreshness = "unavailable"
)

type ReportEvidenceInput struct {
	ID             string
	Kind           string
	ResourceKind   string
	ResourceID     string
	ObservedState  ReportHealthState
	Freshness      ReportFreshness
	ObservedAt     time.Time
	ReasonCode     string
	RunbookRef     string
	EvidenceRefs   []string
	EvidenceDigest string
}

type ReportEvidence struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	ResourceKind   string            `json:"resource_kind"`
	ResourceID     string            `json:"resource_id"`
	ObservedState  ReportHealthState `json:"observed_state"`
	EffectiveState ReportHealthState `json:"effective_state"`
	Freshness      ReportFreshness   `json:"freshness"`
	ObservedAt     time.Time         `json:"observed_at"`
	ReasonCode     string            `json:"reason_code,omitempty"`
	RunbookRef     string            `json:"runbook_ref,omitempty"`
	EvidenceRefs   []string          `json:"evidence_refs,omitempty"`
	EvidenceDigest string            `json:"evidence_digest,omitempty"`
}

type OperationalReport struct {
	Schema             string            `json:"schema"`
	DataState          ReportDataState   `json:"data_state"`
	OverallState       ReportHealthState `json:"overall_state"`
	GeneratedAt        time.Time         `json:"generated_at"`
	Evidence           []ReportEvidence  `json:"evidence"`
	EvidenceDigest     string            `json:"evidence_digest"`
	MutationAuthorized bool              `json:"mutation_authorized"`
}

type EvidenceDrawer struct {
	Schema             string            `json:"schema"`
	DataState          ReportDataState   `json:"data_state"`
	ResourceKind       string            `json:"resource_kind"`
	ResourceID         string            `json:"resource_id"`
	OverallState       ReportHealthState `json:"overall_state"`
	Evidence           []ReportEvidence  `json:"evidence"`
	MutationAuthorized bool              `json:"mutation_authorized"`
}

func BuildOperationalReport(now time.Time, dataState ReportDataState, input []ReportEvidenceInput) (OperationalReport, error) {
	if now.IsZero() {
		return OperationalReport{}, errors.New("generated_at is required")
	}
	if dataState != ReportDataLoaded && dataState != ReportDataUnavailable {
		return OperationalReport{}, fmt.Errorf("unsupported data state %q", dataState)
	}
	if len(input) > MaxReportItems {
		return OperationalReport{}, fmt.Errorf("evidence count exceeds %d", MaxReportItems)
	}
	if dataState == ReportDataUnavailable && len(input) != 0 {
		return OperationalReport{}, errors.New("unavailable report cannot contain evidence")
	}

	report := OperationalReport{
		Schema:             OperationalReportSchema,
		DataState:          dataState,
		OverallState:       ReportHealthUnknown,
		GeneratedAt:        now.UTC(),
		MutationAuthorized: false,
	}
	if dataState == ReportDataUnavailable || len(input) == 0 {
		report.Evidence = []ReportEvidence{}
		report.EvidenceDigest = digestReportEvidence(report.Evidence)
		return report, nil
	}

	seen := make(map[string]struct{}, len(input))
	report.Evidence = make([]ReportEvidence, 0, len(input))
	for _, item := range input {
		built, err := buildReportEvidence(now, item)
		if err != nil {
			return OperationalReport{}, err
		}
		if _, exists := seen[built.ID]; exists {
			return OperationalReport{}, fmt.Errorf("duplicate evidence id %q", built.ID)
		}
		seen[built.ID] = struct{}{}
		report.Evidence = append(report.Evidence, built)
	}

	sort.Slice(report.Evidence, func(i, j int) bool {
		left, right := report.Evidence[i], report.Evidence[j]
		if reportStateRank(left.EffectiveState) != reportStateRank(right.EffectiveState) {
			return reportStateRank(left.EffectiveState) > reportStateRank(right.EffectiveState)
		}
		if left.ResourceKind != right.ResourceKind {
			return left.ResourceKind < right.ResourceKind
		}
		if left.ResourceID != right.ResourceID {
			return left.ResourceID < right.ResourceID
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.ID < right.ID
	})

	report.OverallState = ReportHealthHealthy
	for _, item := range report.Evidence {
		if reportStateRank(item.EffectiveState) > reportStateRank(report.OverallState) {
			report.OverallState = item.EffectiveState
		}
	}
	report.EvidenceDigest = digestReportEvidence(report.Evidence)
	return report, nil
}

func BuildEvidenceDrawer(report OperationalReport, resourceKind, resourceID string) (EvidenceDrawer, error) {
	if report.Schema != OperationalReportSchema {
		return EvidenceDrawer{}, errors.New("unsupported report schema")
	}
	if report.MutationAuthorized {
		return EvidenceDrawer{}, errors.New("report unexpectedly carries mutation authority")
	}
	if err := validateReportBoundedText("resource_kind", resourceKind, 1, 64); err != nil {
		return EvidenceDrawer{}, err
	}
	if err := validateReportBoundedText("resource_id", resourceID, 1, MaxReportText); err != nil {
		return EvidenceDrawer{}, err
	}

	drawer := EvidenceDrawer{
		Schema:             EvidenceDrawerSchema,
		DataState:          report.DataState,
		ResourceKind:       strings.TrimSpace(resourceKind),
		ResourceID:         strings.TrimSpace(resourceID),
		OverallState:       ReportHealthUnknown,
		Evidence:           []ReportEvidence{},
		MutationAuthorized: false,
	}
	if report.DataState == ReportDataUnavailable {
		return drawer, nil
	}
	if report.DataState != ReportDataLoaded {
		return EvidenceDrawer{}, errors.New("unsupported report data state")
	}

	for _, item := range report.Evidence {
		if item.ResourceKind == drawer.ResourceKind && item.ResourceID == drawer.ResourceID {
			drawer.Evidence = append(drawer.Evidence, item)
		}
	}
	if len(drawer.Evidence) == 0 {
		return drawer, nil
	}

	drawer.OverallState = ReportHealthHealthy
	for _, item := range drawer.Evidence {
		if reportStateRank(item.EffectiveState) > reportStateRank(drawer.OverallState) {
			drawer.OverallState = item.EffectiveState
		}
	}
	return drawer, nil
}

func buildReportEvidence(now time.Time, input ReportEvidenceInput) (ReportEvidence, error) {
	if err := validateReportBoundedText("id", input.ID, 1, 128); err != nil {
		return ReportEvidence{}, err
	}
	if err := validateReportBoundedText("kind", input.Kind, 1, 64); err != nil {
		return ReportEvidence{}, err
	}
	if err := validateReportBoundedText("resource_kind", input.ResourceKind, 1, 64); err != nil {
		return ReportEvidence{}, err
	}
	if err := validateReportBoundedText("resource_id", input.ResourceID, 1, MaxReportText); err != nil {
		return ReportEvidence{}, err
	}
	if !validReportState(input.ObservedState) {
		return ReportEvidence{}, fmt.Errorf("unsupported observed state %q", input.ObservedState)
	}
	if !validReportFreshness(input.Freshness) {
		return ReportEvidence{}, fmt.Errorf("unsupported freshness %q", input.Freshness)
	}
	if input.ObservedAt.IsZero() {
		return ReportEvidence{}, errors.New("observed_at is required")
	}
	if input.ObservedAt.After(now) {
		return ReportEvidence{}, errors.New("observed_at cannot be in the future")
	}
	if input.ReasonCode != "" {
		if err := validateReportReasonCode(input.ReasonCode); err != nil {
			return ReportEvidence{}, err
		}
	}
	if input.RunbookRef != "" {
		if err := validateReportBoundedText("runbook_ref", input.RunbookRef, 1, MaxReportText); err != nil {
			return ReportEvidence{}, err
		}
	}
	if len(input.EvidenceRefs) > MaxReportEvidenceRefs {
		return ReportEvidence{}, fmt.Errorf("evidence_refs exceeds %d", MaxReportEvidenceRefs)
	}
	refs := make([]string, 0, len(input.EvidenceRefs))
	refSeen := make(map[string]struct{}, len(input.EvidenceRefs))
	for _, ref := range input.EvidenceRefs {
		if err := validateReportBoundedText("evidence_ref", ref, 1, MaxReportText); err != nil {
			return ReportEvidence{}, err
		}
		ref = strings.TrimSpace(ref)
		if _, exists := refSeen[ref]; exists {
			return ReportEvidence{}, fmt.Errorf("duplicate evidence_ref %q", ref)
		}
		refSeen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	if input.EvidenceDigest != "" && !validReportSHA256Digest(input.EvidenceDigest) {
		return ReportEvidence{}, errors.New("evidence_digest must be sha256:<64 lowercase hex>")
	}

	return ReportEvidence{
		ID:             strings.TrimSpace(input.ID),
		Kind:           strings.TrimSpace(input.Kind),
		ResourceKind:   strings.TrimSpace(input.ResourceKind),
		ResourceID:     strings.TrimSpace(input.ResourceID),
		ObservedState:  input.ObservedState,
		EffectiveState: effectiveReportState(input.ObservedState, input.Freshness),
		Freshness:      input.Freshness,
		ObservedAt:     input.ObservedAt.UTC(),
		ReasonCode:     input.ReasonCode,
		RunbookRef:     strings.TrimSpace(input.RunbookRef),
		EvidenceRefs:   refs,
		EvidenceDigest: input.EvidenceDigest,
	}, nil
}

func effectiveReportState(observed ReportHealthState, freshness ReportFreshness) ReportHealthState {
	if observed == ReportHealthUnhealthy || observed == ReportHealthDegraded || observed == ReportHealthUnknown {
		return observed
	}
	switch freshness {
	case ReportFreshnessCurrent:
		return ReportHealthHealthy
	case ReportFreshnessStale:
		return ReportHealthDegraded
	case ReportFreshnessExpired, ReportFreshnessUnavailable:
		return ReportHealthUnknown
	default:
		return ReportHealthUnknown
	}
}

func reportStateRank(state ReportHealthState) int {
	switch state {
	case ReportHealthUnhealthy:
		return 4
	case ReportHealthDegraded:
		return 3
	case ReportHealthUnknown:
		return 2
	case ReportHealthHealthy:
		return 1
	default:
		return 5
	}
}

func validReportState(state ReportHealthState) bool {
	switch state {
	case ReportHealthHealthy, ReportHealthUnknown, ReportHealthDegraded, ReportHealthUnhealthy:
		return true
	default:
		return false
	}
}

func validReportFreshness(freshness ReportFreshness) bool {
	switch freshness {
	case ReportFreshnessCurrent, ReportFreshnessStale, ReportFreshnessExpired, ReportFreshnessUnavailable:
		return true
	default:
		return false
	}
}

func validateReportReasonCode(value string) error {
	if len(value) == 0 || len(value) > MaxReportReasonCode {
		return errors.New("reason_code is outside allowed length")
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("reason_code contains unsupported character at offset %d", i)
	}
	return nil
}

func validateReportBoundedText(field, value string, minLen, maxLen int) error {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < minLen || len(trimmed) > maxLen {
		return fmt.Errorf("%s is outside allowed length", field)
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", field)
		}
	}
	return nil
}

func validReportSHA256Digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	encoded := strings.TrimPrefix(value, "sha256:")
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size {
		return false
	}
	return strings.ToLower(encoded) == encoded
}

func digestReportEvidence(evidence []ReportEvidence) string {
	raw, err := json.Marshal(evidence)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
