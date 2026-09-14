package main

import (
	"bytes"
	"net/http"
	"time"

	"control-center/internal/agent"
	agentapi "control-center/internal/agent/httpapi"
	automationapi "control-center/internal/automation/httpapi"
	"control-center/internal/buildinfo"
	domainapi "control-center/internal/domain/httpapi"
	identityapi "control-center/internal/identity/httpapi"
	"control-center/internal/identity/rbac"
	"control-center/internal/inventory"
	inventoryapi "control-center/internal/inventory/httpapi"
	marketapi "control-center/internal/market/httpapi"
	"control-center/internal/nodelifecycle"
	lifecycleapi "control-center/internal/nodelifecycle/httpapi"
	nodesapi "control-center/internal/nodes/httpapi"
	pxeapi "control-center/internal/pxe/httpapi"
	productui "control-center/internal/ui"
	uiapi "control-center/internal/ui/httpapi"
)

type productHandlerConfig struct {
	lifecycleProjection     nodelifecycle.Projection
	infrastructureInventory productui.InfrastructureInventoryProvider
	changesJobs             productui.ChangesJobsProvider
	resourceReports         productui.OperationalReportProvider
	auditReports            productui.OperationalReportProvider
}

type productHandlerOption func(*productHandlerConfig)

func withNodeLifecycleProjection(projection nodelifecycle.Projection) productHandlerOption {
	return func(config *productHandlerConfig) {
		if projection != nil {
			config.lifecycleProjection = projection
		}
	}
}

// withInfrastructureInventoryProvider wires only a read model. The default
// runtime intentionally leaves the endpoint absent until an authoritative
// provider is configured, rather than exposing transitional registries as if
// they were complete Sites/Nodes/Inventory evidence.
func withInfrastructureInventoryProvider(provider productui.InfrastructureInventoryProvider) productHandlerOption {
	return func(config *productHandlerConfig) {
		if provider != nil {
			config.infrastructureInventory = provider
		}
	}
}

// withChangesJobsProvider wires the bounded operational Changes / Jobs read
// model only when an authoritative persisted-state provider is available. The
// default runtime leaves the endpoint absent instead of presenting synthetic or
// transitional orchestration state as current evidence.
func withChangesJobsProvider(provider productui.ChangesJobsProvider) productHandlerOption {
	return func(config *productHandlerConfig) {
		if provider != nil {
			config.changesJobs = provider
		}
	}
}

// withResourceReportsProvider registers a report source that is already
// constrained to ordinary infrastructure/resource evidence. The route keeps
// the existing global resources.read boundary and cannot be switched by a
// client to an Audit-backed source.
func withResourceReportsProvider(provider productui.OperationalReportProvider) productHandlerOption {
	return func(config *productHandlerConfig) {
		if provider != nil {
			config.resourceReports = provider
		}
	}
}

// withAuditReportsProvider registers a report source containing Audit-derived
// evidence. It is intentionally a distinct option so the route always retains
// the stronger global audit.events.read boundary instead of sharing the
// resources.read route merely because both sources use ui.operational-report.
func withAuditReportsProvider(provider productui.OperationalReportProvider) productHandlerOption {
	return func(config *productHandlerConfig) {
		if provider != nil {
			config.auditReports = provider
		}
	}
}

func newProductHandler(identity *identityapi.Server, options ...productHandlerOption) http.Handler {
	mux := http.NewServeMux()
	guard := func(permission rbac.Permission, handler http.Handler) http.Handler {
		return identity.Authenticate(identity.Require(permission, rbac.GlobalScope())(handler))
	}
	webGuard := func(permission rbac.Permission, handler http.Handler) http.Handler {
		return identity.AuthenticateWeb(identity.RequireWeb(permission, rbac.GlobalScope())(handler))
	}
	config := productHandlerConfig{lifecycleProjection: nodelifecycle.NewEmptyMemoryProjection()}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	agentState := agentapi.StateHandler(agent.NewMemoryRegistry())
	inventoryState := inventoryapi.StateHandler(inventory.NewMemoryRegistry())
	lifecycleState := lifecycleapi.New(config.lifecycleProjection)

	mux.Handle("/api/v1/nodes/enrollment/plan", guard(rbac.PermissionNodeEnrollmentPlan, nodesapi.New()))
	mux.Handle("/api/v1/nodes/{nodeID}/lifecycle", guard(rbac.PermissionNodeLifecycleRead, lifecycleState))
	mux.Handle("/api/v1/nodes/{nodeID}/lifecycle/transitions/plan", guard(rbac.PermissionNodeLifecyclePlan, lifecycleState))
	mux.Handle("/api/v1/automation/plan", guard(rbac.PermissionAutomationPlan, automationapi.New()))
	mux.Handle("/api/v1/pxe/plan", guard(rbac.PermissionPXEPlan, pxeapi.New()))
	marketHandler := guard(rbac.PermissionMarketRead, marketapi.New())
	mux.Handle("/api/v1/market/manifests", marketHandler)
	mux.Handle("/api/v1/market/manifests/", marketHandler)
	mux.Handle("/api/v2/market/manifests", marketHandler)
	mux.Handle("/api/v2/market/manifests/", marketHandler)
	mux.Handle("/api/v1/domain/provider/resolve", guard(rbac.PermissionDomainProviderResolve, domainapi.ProviderHandler()))
	mux.Handle("/api/v1/domain/lifecycle/plan", guard(rbac.PermissionDomainLifecyclePlan, domainapi.LifecyclePlanHandler()))
	mux.Handle("/api/v1/domain/join/validate", guard(rbac.PermissionDomainLifecyclePlan, domainapi.JoinValidationHandler()))
	mux.Handle("/api/v1/domain/readiness/evaluate", guard(rbac.PermissionDomainLifecyclePlan, domainapi.ReadinessHandler()))
	mux.Handle("/api/v1/inventory/normalize", guard(rbac.PermissionInventoryNormalize, inventoryapi.NormalizeHandler()))
	mux.Handle("/api/v1/inventory/reconcile", guard(rbac.PermissionInventoryReconcile, inventoryapi.ReconcileHandler()))
	mux.Handle("/api/v1/inventory/freshness", guard(rbac.PermissionInventoryFreshness, inventoryapi.FreshnessHandler()))
	mux.Handle("/api/v1/inventory/observations", guard(rbac.PermissionInventoryReconcile, inventoryState))
	mux.Handle("/api/v1/inventory/devices", guard(rbac.PermissionInventoryReconcile, inventoryState))
	mux.Handle("/api/v1/inventory/devices/", guard(rbac.PermissionInventoryReconcile, inventoryState))
	mux.Handle("/api/v1/agent/enrollment/normalize", guard(rbac.PermissionAgentEnrollmentNormalize, agentapi.EnrollmentHandler()))
	mux.Handle("/api/v1/agent/heartbeat/evaluate", guard(rbac.PermissionAgentHeartbeatEvaluate, agentapi.HeartbeatHandler()))
	mux.Handle("/api/v1/agent/lease/evaluate", guard(rbac.PermissionAgentLeaseEvaluate, agentapi.LeaseHandler()))
	mux.Handle("/api/v1/agent/enrollments", guard(rbac.PermissionAgentEnrollmentNormalize, agentState))
	mux.Handle("/api/v1/agent/heartbeats", guard(rbac.PermissionAgentHeartbeatEvaluate, agentState))
	mux.Handle("/api/v1/agent/nodes", guard(rbac.PermissionAgentEnrollmentNormalize, agentState))
	mux.Handle("/api/v1/agent/nodes/", guard(rbac.PermissionAgentEnrollmentNormalize, agentState))
	if config.infrastructureInventory != nil {
		mux.Handle("GET /api/v1/ui/infrastructure", guard(rbac.PermissionResourcesRead, uiapi.InfrastructureHandler(config.infrastructureInventory)))
		mux.Handle("GET /infrastructure", guard(rbac.PermissionResourcesRead, infrastructureWebHandler(config.infrastructureInventory)))
	}
	if config.changesJobs != nil {
		mux.Handle("GET /api/v1/ui/changes-jobs", guard(rbac.PermissionJobsRead, uiapi.ChangesJobsHandler(config.changesJobs)))
	}
	if config.resourceReports != nil {
		mux.Handle("GET /api/v1/ui/reports/resources", guard(rbac.PermissionResourcesRead, uiapi.OperationalReportHandler(config.resourceReports)))
		mux.Handle("GET /api/v1/ui/reports/resources/evidence", guard(rbac.PermissionResourcesRead, uiapi.EvidenceDrawerHandler(config.resourceReports)))
		mux.Handle("GET /reports/resources", webGuard(rbac.PermissionResourcesRead, operationalReportWebHandler(config.resourceReports, "Ресурсы / Health")))
	}
	if config.auditReports != nil {
		mux.Handle("GET /api/v1/ui/reports/audit", guard(rbac.PermissionAuditRead, uiapi.OperationalReportHandler(config.auditReports)))
		mux.Handle("GET /api/v1/ui/reports/audit/evidence", guard(rbac.PermissionAuditRead, uiapi.EvidenceDrawerHandler(config.auditReports)))
		mux.Handle("GET /reports/audit", webGuard(rbac.PermissionAuditRead, operationalReportWebHandler(config.auditReports, "Audit")))
	}
	return mux
}

func infrastructureWebHandler(provider productui.InfrastructureInventoryProvider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := identityapi.PrincipalFromContext(r.Context())
		if !ok || provider == nil {
			http.Error(w, "infrastructure inventory unavailable", http.StatusServiceUnavailable)
			return
		}

		view, err := provider.InfrastructureInventory(r.Context())
		status := http.StatusOK
		if err != nil || productui.ValidateInfrastructureInventory(view) != nil {
			view = productui.InfrastructureInventory{
				ContractVersion: productui.InfrastructureInventoryContractVersion,
				State:           productui.InventoryViewUnavailable,
				Sites:           []productui.SiteInventory{},
			}
			status = http.StatusServiceUnavailable
		} else if view.State == productui.InventoryViewUnavailable {
			status = http.StatusServiceUnavailable
		}

		var body bytes.Buffer
		if err := identityapi.RenderInfrastructureInventory(
			&body,
			buildinfo.Version,
			principal.Identity.DisplayName,
			principal.Identity.Username,
			view,
		); err != nil {
			http.Error(w, "infrastructure inventory unavailable", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.WriteHeader(status)
		_, _ = w.Write(body.Bytes())
	})
}

func operationalReportWebHandler(provider productui.OperationalReportProvider, sourceLabel string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := identityapi.PrincipalFromContext(r.Context())
		if !ok || provider == nil {
			http.Error(w, "operational report unavailable", http.StatusServiceUnavailable)
			return
		}

		report, err := provider.OperationalReport(r.Context())
		status := http.StatusOK
		if err != nil || productui.ValidateOperationalReport(report) != nil {
			report, err = productui.BuildOperationalReport(time.Now().UTC(), productui.ReportDataUnavailable, nil)
			if err != nil {
				http.Error(w, "operational report unavailable", http.StatusServiceUnavailable)
				return
			}
			status = http.StatusServiceUnavailable
		} else if report.DataState == productui.ReportDataUnavailable {
			status = http.StatusServiceUnavailable
		}

		var body bytes.Buffer
		if err := identityapi.RenderOperationalReport(
			&body,
			buildinfo.Version,
			principal.Identity.DisplayName,
			principal.Identity.Username,
			sourceLabel,
			report,
		); err != nil {
			http.Error(w, "operational report unavailable", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.WriteHeader(status)
		_, _ = w.Write(body.Bytes())
	})
}
