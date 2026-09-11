package main

import (
	"net/http"

	"control-center/internal/agent"
	agentapi "control-center/internal/agent/httpapi"
	automationapi "control-center/internal/automation/httpapi"
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
)

type productHandlerConfig struct {
	lifecycleProjection nodelifecycle.Projection
}

type productHandlerOption func(*productHandlerConfig)

func withNodeLifecycleProjection(projection nodelifecycle.Projection) productHandlerOption {
	return func(config *productHandlerConfig) {
		if projection != nil {
			config.lifecycleProjection = projection
		}
	}
}

func newProductHandler(identity *identityapi.Server, options ...productHandlerOption) http.Handler {
	mux := http.NewServeMux()
	guard := func(permission rbac.Permission, handler http.Handler) http.Handler {
		return identity.Authenticate(identity.Require(permission, rbac.GlobalScope())(handler))
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
	return mux
}
