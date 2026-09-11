package ui

import "fmt"

// ValidateInfrastructureInventory rejects inconsistent provider output before
// it reaches an API or HTML surface. In particular, unavailable evidence must
// never carry apparently authoritative zero/non-zero counts or timestamps.
func ValidateInfrastructureInventory(view InfrastructureInventory) error {
	if view.ContractVersion != InfrastructureInventoryContractVersion {
		return fmt.Errorf("%w: unsupported contract_version %q", ErrInvalidInfrastructureInventory, view.ContractVersion)
	}
	if !validInventoryViewState(view.State) {
		return fmt.Errorf("%w: unsupported inventory state %q", ErrInvalidInfrastructureInventory, view.State)
	}
	if view.State == InventoryViewUnavailable {
		if view.GeneratedAt != nil || view.SiteCount != 0 || view.NodeCount != 0 || len(view.Sites) != 0 {
			return fmt.Errorf("%w: unavailable inventory must not carry authoritative data", ErrInvalidInfrastructureInventory)
		}
		return nil
	}
	if view.GeneratedAt == nil || view.GeneratedAt.IsZero() {
		return fmt.Errorf("%w: generated_at is required for loaded inventory", ErrInvalidInfrastructureInventory)
	}
	if view.SiteCount != len(view.Sites) {
		return fmt.Errorf("%w: site_count=%d does not match sites=%d", ErrInvalidInfrastructureInventory, view.SiteCount, len(view.Sites))
	}

	siteIDs := make(map[string]struct{}, len(view.Sites))
	nodeIDs := make(map[string]struct{}, view.NodeCount)
	totalNodes := 0
	for _, site := range view.Sites {
		if site.ID == "" || site.ScopeID == "" || site.Name == "" {
			return fmt.Errorf("%w: site identity, name and scope are required", ErrInvalidInfrastructureInventory)
		}
		if _, exists := siteIDs[site.ID]; exists {
			return fmt.Errorf("%w: duplicate site %q", ErrInvalidInfrastructureInventory, site.ID)
		}
		siteIDs[site.ID] = struct{}{}
		if site.NodeCount != len(site.Nodes) {
			return fmt.Errorf("%w: site %q node_count=%d does not match nodes=%d", ErrInvalidInfrastructureInventory, site.ID, site.NodeCount, len(site.Nodes))
		}
		for _, node := range site.Nodes {
			totalNodes++
			if node.ID == "" || node.Hostname == "" || node.SiteID != site.ID || node.CollectedAt.IsZero() {
				return fmt.Errorf("%w: node %q has inconsistent identity or observation evidence", ErrInvalidInfrastructureInventory, node.ID)
			}
			if _, exists := nodeIDs[node.ID]; exists {
				return fmt.Errorf("%w: duplicate node %q", ErrInvalidInfrastructureInventory, node.ID)
			}
			nodeIDs[node.ID] = struct{}{}
			if node.Freshness != InventoryViewCurrent && node.Freshness != InventoryViewStale && node.Freshness != InventoryViewExpired {
				return fmt.Errorf("%w: node %q has unsupported freshness %q", ErrInvalidInfrastructureInventory, node.ID, node.Freshness)
			}
			for field, evidence := range map[string]EvidenceAvailability{
				"desired_state": node.DesiredState,
				"actual_state":  node.ActualState,
				"version_skew":  node.VersionSkew,
			} {
				if evidence != EvidenceUnavailable && evidence != EvidenceAvailable {
					return fmt.Errorf("%w: node %q has unsupported %s availability %q", ErrInvalidInfrastructureInventory, node.ID, field, evidence)
				}
			}
			if node.Connectivity.Interfaces < 0 || node.Connectivity.Up < 0 || node.Connectivity.Down < 0 || node.Connectivity.Unknown < 0 ||
				node.Connectivity.Interfaces != node.Connectivity.Up+node.Connectivity.Down+node.Connectivity.Unknown {
				return fmt.Errorf("%w: node %q has inconsistent connectivity counters", ErrInvalidInfrastructureInventory, node.ID)
			}
		}
	}
	if view.NodeCount != totalNodes {
		return fmt.Errorf("%w: node_count=%d does not match nodes=%d", ErrInvalidInfrastructureInventory, view.NodeCount, totalNodes)
	}
	return nil
}

func validInventoryViewState(state InventoryViewState) bool {
	switch state {
	case InventoryViewUnavailable, InventoryViewCurrent, InventoryViewStale, InventoryViewExpired:
		return true
	default:
		return false
	}
}
