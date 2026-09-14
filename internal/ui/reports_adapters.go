package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"control-center/internal/incidents"
)

// BuildHealthOperationalReport converts an already-qualified Health Overview
// into the common bounded report contract. The adapter does not re-infer health
// from provider payloads and never grants mutation authority.
func BuildHealthOperationalReport(now time.Time, overview HealthOverview) (OperationalReport, error) {
	if overview.Schema != HealthOverviewSchemaV1 {
		return OperationalReport{}, fmt.Errorf("unsupported health overview schema %q", overview.Schema)
	}
	if overview.MutationAuthorized {
		return OperationalReport{}, fmt.Errorf("health overview unexpectedly carries mutation authority")
	}

	switch overview.DataState {
	case HealthDataUnavailable:
		if len(overview.Signals) != 0 || len(overview.Resources) != 0 || overview.OverallState != HealthStateUnknown {
			return OperationalReport{}, fmt.Errorf("unavailable health overview contains contradictory evidence")
		}
		return BuildOperationalReport(now, ReportDataUnavailable, nil)
	case HealthDataLoaded:
	default:
		return OperationalReport{}, fmt.Errorf("unsupported health data state %q", overview.DataState)
	}

	inputs := make([]ReportEvidenceInput, 0, len(overview.Signals))
	for _, signal := range overview.Signals {
		observed, err := reportHealthState(signal.ObservedState)
		if err != nil {
			return OperationalReport{}, err
		}
		freshness, err := reportHealthFreshness(signal.Freshness)
		if err != nil {
			return OperationalReport{}, err
		}
		expectedEffective := effectiveReportState(observed, freshness)
		actualEffective, err := reportHealthState(signal.EffectiveState)
		if err != nil {
			return OperationalReport{}, err
		}
		if expectedEffective != actualEffective {
			return OperationalReport{}, fmt.Errorf("health signal %q has inconsistent effective state", signal.ID)
		}

		inputs = append(inputs, ReportEvidenceInput{
			ID:            healthReportEvidenceID(signal),
			Kind:          "health",
			ResourceKind:  signal.ResourceKind,
			ResourceID:    signal.ResourceID,
			ObservedState: observed,
			Freshness:     freshness,
			ObservedAt:    signal.ObservedAt,
			ReasonCode:    "health." + signal.EffectiveState,
			RunbookRef:    signal.RunbookRef,
			EvidenceRefs:  append([]string(nil), signal.EvidenceRefs...),
		})
	}

	report, err := BuildOperationalReport(now, ReportDataLoaded, inputs)
	if err != nil {
		return OperationalReport{}, err
	}
	expectedOverall, err := reportHealthState(overview.OverallState)
	if err != nil {
		return OperationalReport{}, err
	}
	if report.OverallState != expectedOverall {
		return OperationalReport{}, fmt.Errorf("health overview overall state does not match adapted evidence")
	}
	return report, nil
}

// BuildIncidentOperationalReport converts one complete, already-authorized
// incident page into resource-bound report evidence. A partial page is rejected
// because treating a truncated incident set as the overall operational state
// could create false success. Resolved incidents deliberately map to unknown:
// an operator resolution is not proof that the affected resource is healthy.
func BuildIncidentOperationalReport(now time.Time, page incidents.ListPage) (OperationalReport, error) {
	if page.HasMore || page.Next != nil {
		return OperationalReport{}, fmt.Errorf("incident report requires a complete bounded page")
	}

	inputs := make([]ReportEvidenceInput, 0, len(page.Items))
	for _, incident := range page.Items {
		if err := incident.Validate(); err != nil {
			return OperationalReport{}, fmt.Errorf("invalid incident report source: %w", err)
		}
		state := incidentReportState(incident)
		reason := "incident." + string(incident.Status) + "." + string(incident.Severity)
		runbook := ""
		if incident.Runbook != nil {
			runbook = "runbook:" + incident.Runbook.ID + "@" + incident.Runbook.Revision
		}
		incidentDigest := incidentReportDigest(incident)
		incidentRef := "incident:" + incidentDigest

		for index, resource := range incident.AffectedResources {
			inputs = append(inputs, ReportEvidenceInput{
				ID:             incidentReportEvidenceID(incident, resource, index),
				Kind:           "incident",
				ResourceKind:   resource.Kind,
				ResourceID:     resource.ID,
				ObservedState:  state,
				Freshness:      ReportFreshnessCurrent,
				ObservedAt:     incident.LastObservedAt,
				ReasonCode:     reason,
				RunbookRef:     runbook,
				EvidenceRefs:   []string{incidentRef},
				EvidenceDigest: "sha256:" + incidentDigest,
			})
		}
	}
	return BuildOperationalReport(now, ReportDataLoaded, inputs)
}

func reportHealthState(value string) (ReportHealthState, error) {
	switch value {
	case HealthStateHealthy:
		return ReportHealthHealthy, nil
	case HealthStateUnknown:
		return ReportHealthUnknown, nil
	case HealthStateDegraded:
		return ReportHealthDegraded, nil
	case HealthStateUnhealthy:
		return ReportHealthUnhealthy, nil
	default:
		return "", fmt.Errorf("unsupported health state %q", value)
	}
}

func reportHealthFreshness(value string) (ReportFreshness, error) {
	switch value {
	case HealthFreshnessCurrent:
		return ReportFreshnessCurrent, nil
	case HealthFreshnessStale:
		return ReportFreshnessStale, nil
	case HealthFreshnessExpired:
		return ReportFreshnessExpired, nil
	default:
		return "", fmt.Errorf("unsupported health freshness %q", value)
	}
}

func incidentReportState(incident incidents.Incident) ReportHealthState {
	if incident.Status == incidents.StatusResolved {
		return ReportHealthUnknown
	}
	switch incident.Severity {
	case incidents.SeverityCritical:
		return ReportHealthUnhealthy
	case incidents.SeverityWarning:
		return ReportHealthDegraded
	default:
		return ReportHealthUnknown
	}
}

func healthReportEvidenceID(signal HealthSignalView) string {
	parts := []string{signal.ID, signal.ResourceKind, signal.ResourceID, signal.CheckName}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "health:" + hex.EncodeToString(sum[:])
}

func incidentReportDigest(incident incidents.Incident) string {
	parts := []string{
		incident.ObjectID,
		incident.ScopeID,
		strconv.FormatUint(incident.Generation, 10),
		incident.ResourceVersion,
		string(incident.Status),
		string(incident.Severity),
		incident.LastObservedAt.UTC().Format(time.RFC3339Nano),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func incidentReportEvidenceID(incident incidents.Incident, resource incidents.ResourceRef, index int) string {
	parts := []string{incident.ObjectID, resource.Kind, resource.ID, resource.ScopeID, strconv.Itoa(index)}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "incident:" + hex.EncodeToString(sum[:])
}
