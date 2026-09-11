package networkpolicy

import (
	"errors"
	"testing"
)

func TestAuthorizeForwardingFailsClosedAcrossWAN(t *testing.T) {
	tests := []struct {
		name   string
		intent ForwardingIntent
		want   error
	}{
		{
			name:   "not explicitly enabled",
			intent: ForwardingIntent{Source: ZoneWAN, Destination: ZoneLAN, EdgeGatewayAssigned: true},
			want:   ErrForwardingDisabled,
		},
		{
			name:   "edge gateway not assigned",
			intent: ForwardingIntent{Source: ZoneWAN, Destination: ZoneLAN, ExplicitlyEnabled: true},
			want:   ErrEdgeGatewayRequired,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := AuthorizeForwarding(tc.intent); !errors.Is(err, tc.want) {
				t.Fatalf("AuthorizeForwarding() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAuthorizeForwardingAllowsExplicitEdgeGatewayRouting(t *testing.T) {
	intent := ForwardingIntent{
		Source:              ZoneWAN,
		Destination:         ZoneLAN,
		ExplicitlyEnabled:   true,
		EdgeGatewayAssigned: true,
	}
	if err := AuthorizeForwarding(intent); err != nil {
		t.Fatalf("AuthorizeForwarding() error = %v", err)
	}
}

func TestAuthorizeForwardingAllowsExplicitInternalInterZoneRouting(t *testing.T) {
	intent := ForwardingIntent{
		Source:            ZoneLAN,
		Destination:       ZoneManagement,
		ExplicitlyEnabled: true,
	}
	if err := AuthorizeForwarding(intent); err != nil {
		t.Fatalf("AuthorizeForwarding() error = %v", err)
	}
}

func TestAuthorizeForwardingRejectsUnknownZone(t *testing.T) {
	err := AuthorizeForwarding(ForwardingIntent{Source: Zone("UNKNOWN"), Destination: ZoneLAN})
	if !errors.Is(err, ErrInvalidZone) {
		t.Fatalf("AuthorizeForwarding() error = %v, want ErrInvalidZone", err)
	}
}
