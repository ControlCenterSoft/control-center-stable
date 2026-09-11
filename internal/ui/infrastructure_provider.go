package ui

import "context"

// InfrastructureInventoryProvider is the read-only boundary between
// authoritative topology/inventory projections and the product UI. Providers
// must not mutate nodes, desired state, networking, or enrollment as a side
// effect of serving a snapshot.
type InfrastructureInventoryProvider interface {
	InfrastructureInventory(context.Context) (InfrastructureInventory, error)
}

// InfrastructureInventoryProviderFunc adapts a pure function for composition
// and tests without introducing another stateful registry.
type InfrastructureInventoryProviderFunc func(context.Context) (InfrastructureInventory, error)

func (fn InfrastructureInventoryProviderFunc) InfrastructureInventory(ctx context.Context) (InfrastructureInventory, error) {
	return fn(ctx)
}
