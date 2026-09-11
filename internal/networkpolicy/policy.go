package networkpolicy

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidZone         = errors.New("invalid network zone")
	ErrForwardingDisabled  = errors.New("inter-zone forwarding disabled")
	ErrEdgeGatewayRequired = errors.New("edge gateway required")
)

// Zone is a first-class network security zone used by Control Center 0.6.
type Zone string

const (
	ZoneWAN        Zone = "WAN"
	ZoneLAN        Zone = "LAN"
	ZoneManagement Zone = "MANAGEMENT"
	ZoneDMZ        Zone = "DMZ"
	ZoneCluster    Zone = "CLUSTER"
	ZoneStorage    Zone = "STORAGE"
	ZoneBackup     Zone = "BACKUP"
)

func (z Zone) Valid() bool {
	switch z {
	case ZoneWAN, ZoneLAN, ZoneManagement, ZoneDMZ, ZoneCluster, ZoneStorage, ZoneBackup:
		return true
	default:
		return false
	}
}

// ForwardingIntent captures the minimum authorization inputs for routing
// between network zones. Inter-zone routing is fail-closed and WAN forwarding
// additionally requires an explicitly assigned Edge Gateway role.
type ForwardingIntent struct {
	Source              Zone `json:"source"`
	Destination         Zone `json:"destination"`
	ExplicitlyEnabled   bool `json:"explicitly_enabled"`
	EdgeGatewayAssigned bool `json:"edge_gateway_assigned"`
}

// AuthorizeForwarding returns nil only when the requested forwarding is
// permitted by the 0.6 safety baseline. Same-zone traffic is not considered
// routed forwarding and therefore does not require an Edge Gateway.
func AuthorizeForwarding(intent ForwardingIntent) error {
	if !intent.Source.Valid() {
		return fmt.Errorf("%w: source %q", ErrInvalidZone, intent.Source)
	}
	if !intent.Destination.Valid() {
		return fmt.Errorf("%w: destination %q", ErrInvalidZone, intent.Destination)
	}
	if intent.Source == intent.Destination {
		return nil
	}
	if !intent.ExplicitlyEnabled {
		return ErrForwardingDisabled
	}
	if (intent.Source == ZoneWAN || intent.Destination == ZoneWAN) && !intent.EdgeGatewayAssigned {
		return ErrEdgeGatewayRequired
	}
	return nil
}
