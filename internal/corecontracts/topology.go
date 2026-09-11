package corecontracts

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

var ErrInvalidTopology = errors.New("invalid distributed topology")

// ScopeKind identifies a policy and ownership boundary. Scope nesting, rather
// than a fixed Regional Controller role, represents arbitrary organization
// hierarchies.
type ScopeKind string

const (
	ScopeGlobal     ScopeKind = "global"
	ScopeManagement ScopeKind = "management"
	ScopeSite       ScopeKind = "site"
	ScopeResource   ScopeKind = "resource"
)

// DelegatedAuthority is an authority that a parent explicitly grants to a
// child scope. An empty delegation is deny-by-default.
type DelegatedAuthority string

const (
	DelegateRBAC          DelegatedAuthority = "rbac"
	DelegatePolicy        DelegatedAuthority = "policy"
	DelegateConfiguration DelegatedAuthority = "configuration"
	DelegateDesiredState  DelegatedAuthority = "desired-state"
	DelegateApplications  DelegatedAuthority = "applications"
)

// Scope is a node in the management hierarchy. DelegatedAuthorities records
// grants made by ParentID to this scope; grants are not inherited implicitly.
type Scope struct {
	ID                   string               `json:"id"`
	Kind                 ScopeKind            `json:"kind"`
	Name                 string               `json:"name"`
	ParentID             string               `json:"parent_id,omitempty"`
	DelegatedAuthorities []DelegatedAuthority `json:"delegated_authorities,omitempty"`
}

// Site binds a physical or isolated location to exactly one site scope.
type Site struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	ScopeID string `json:"scope_id"`
}

// ManagementZone is a logical policy/RBAC/operational boundary. It is not a
// network forwarding policy and assigning it never enables routing or NAT.
type ManagementZone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	ScopeID string `json:"scope_id"`
	SiteID  string `json:"site_id,omitempty"`
}

// Topology is an immutable validated index used by the other contracts.
type Topology struct {
	rootID string
	scopes map[string]Scope
	sites  map[string]Site
	zones  map[string]ManagementZone
}

// NewTopology validates a complete connected scope hierarchy and its site and
// management-zone bindings. Exactly one global root is required.
func NewTopology(scopes []Scope, sites []Site, zones []ManagementZone) (Topology, error) {
	topology := Topology{
		scopes: make(map[string]Scope, len(scopes)),
		sites:  make(map[string]Site, len(sites)),
		zones:  make(map[string]ManagementZone, len(zones)),
	}
	for _, scope := range scopes {
		if err := validateScope(scope); err != nil {
			return Topology{}, err
		}
		if _, exists := topology.scopes[scope.ID]; exists {
			return Topology{}, fmt.Errorf("%w: duplicate scope %q", ErrInvalidTopology, scope.ID)
		}
		if scope.Kind == ScopeGlobal {
			if topology.rootID != "" {
				return Topology{}, fmt.Errorf("%w: multiple global scopes %q and %q", ErrInvalidTopology, topology.rootID, scope.ID)
			}
			topology.rootID = scope.ID
		}
		topology.scopes[scope.ID] = cloneScope(scope)
	}
	if topology.rootID == "" {
		return Topology{}, fmt.Errorf("%w: exactly one global scope is required", ErrInvalidTopology)
	}
	for _, scope := range topology.scopes {
		if scope.Kind == ScopeGlobal {
			continue
		}
		if _, exists := topology.scopes[scope.ParentID]; !exists {
			return Topology{}, fmt.Errorf("%w: scope %q has unknown parent %q", ErrInvalidTopology, scope.ID, scope.ParentID)
		}
	}
	for id := range topology.scopes {
		if err := topology.validatePathToRoot(id); err != nil {
			return Topology{}, err
		}
	}

	siteByScope := make(map[string]string, len(sites))
	for _, site := range sites {
		if err := validateSite(site, topology); err != nil {
			return Topology{}, err
		}
		if _, exists := topology.sites[site.ID]; exists {
			return Topology{}, fmt.Errorf("%w: duplicate site %q", ErrInvalidTopology, site.ID)
		}
		if other, exists := siteByScope[site.ScopeID]; exists {
			return Topology{}, fmt.Errorf("%w: sites %q and %q share scope %q", ErrInvalidTopology, other, site.ID, site.ScopeID)
		}
		siteByScope[site.ScopeID] = site.ID
		topology.sites[site.ID] = site
	}
	for _, zone := range zones {
		if err := validateManagementZone(zone, topology); err != nil {
			return Topology{}, err
		}
		if _, exists := topology.zones[zone.ID]; exists {
			return Topology{}, fmt.Errorf("%w: duplicate management zone %q", ErrInvalidTopology, zone.ID)
		}
		topology.zones[zone.ID] = zone
	}
	return topology, nil
}

// RootID returns the global root scope.
func (t Topology) RootID() string { return t.rootID }

// Scope returns a defensive copy of a scope.
func (t Topology) Scope(id string) (Scope, bool) {
	scope, ok := t.scopes[id]
	return cloneScope(scope), ok
}

// Site returns a site by logical identity.
func (t Topology) Site(id string) (Site, bool) {
	site, ok := t.sites[id]
	return site, ok
}

// ManagementZone returns a management zone by logical identity.
func (t Topology) ManagementZone(id string) (ManagementZone, bool) {
	zone, ok := t.zones[id]
	return zone, ok
}

// Contains reports whether descendant is ancestor itself or is nested under
// ancestor. Invalid/unknown IDs return false.
func (t Topology) Contains(ancestor, descendant string) bool {
	if ancestor == "" || descendant == "" {
		return false
	}
	if _, exists := t.scopes[ancestor]; !exists {
		return false
	}
	if _, exists := t.scopes[descendant]; !exists {
		return false
	}
	for current := descendant; current != ""; {
		if current == ancestor {
			return true
		}
		scope, ok := t.scopes[current]
		if !ok || scope.Kind == ScopeGlobal {
			return false
		}
		current = scope.ParentID
	}
	return false
}

// IsDelegated reports only direct explicit grants. Parent grants are never
// silently inherited by grandchildren.
func (t Topology) IsDelegated(scopeID string, authority DelegatedAuthority) bool {
	scope, ok := t.scopes[scopeID]
	if !ok {
		return false
	}
	for _, granted := range scope.DelegatedAuthorities {
		if granted == authority {
			return true
		}
	}
	return false
}

func validateScope(scope Scope) error {
	if err := validateIdentifier("scope.id", scope.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTopology, err)
	}
	if err := validateDisplayName("scope.name", scope.Name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTopology, err)
	}
	switch scope.Kind {
	case ScopeGlobal:
		if scope.ParentID != "" {
			return fmt.Errorf("%w: global scope %q cannot have a parent", ErrInvalidTopology, scope.ID)
		}
	case ScopeManagement, ScopeSite, ScopeResource:
		if err := validateIdentifier("scope.parent_id", scope.ParentID); err != nil {
			return fmt.Errorf("%w: scope %q: %v", ErrInvalidTopology, scope.ID, err)
		}
		if scope.ParentID == scope.ID {
			return fmt.Errorf("%w: scope %q is its own parent", ErrInvalidTopology, scope.ID)
		}
	default:
		return fmt.Errorf("%w: scope %q has unknown kind %q", ErrInvalidTopology, scope.ID, scope.Kind)
	}
	seen := make(map[DelegatedAuthority]struct{}, len(scope.DelegatedAuthorities))
	for _, authority := range scope.DelegatedAuthorities {
		if !validDelegatedAuthority(authority) {
			return fmt.Errorf("%w: scope %q has unknown delegated authority %q", ErrInvalidTopology, scope.ID, authority)
		}
		if _, exists := seen[authority]; exists {
			return fmt.Errorf("%w: scope %q repeats delegated authority %q", ErrInvalidTopology, scope.ID, authority)
		}
		seen[authority] = struct{}{}
	}
	if scope.Kind == ScopeGlobal && len(scope.DelegatedAuthorities) != 0 {
		return fmt.Errorf("%w: global scope %q cannot receive delegated authority", ErrInvalidTopology, scope.ID)
	}
	return nil
}

func validDelegatedAuthority(authority DelegatedAuthority) bool {
	switch authority {
	case DelegateRBAC, DelegatePolicy, DelegateConfiguration, DelegateDesiredState, DelegateApplications:
		return true
	default:
		return false
	}
}

func validateSite(site Site, topology Topology) error {
	if err := validateIdentifier("site.id", site.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTopology, err)
	}
	if err := validateDisplayName("site.name", site.Name); err != nil {
		return fmt.Errorf("%w: site %q: %v", ErrInvalidTopology, site.ID, err)
	}
	scope, exists := topology.scopes[site.ScopeID]
	if !exists || scope.Kind != ScopeSite {
		return fmt.Errorf("%w: site %q must reference a site scope", ErrInvalidTopology, site.ID)
	}
	return nil
}

func validateManagementZone(zone ManagementZone, topology Topology) error {
	if err := validateIdentifier("management_zone.id", zone.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTopology, err)
	}
	if err := validateDisplayName("management_zone.name", zone.Name); err != nil {
		return fmt.Errorf("%w: management zone %q: %v", ErrInvalidTopology, zone.ID, err)
	}
	if _, exists := topology.scopes[zone.ScopeID]; !exists {
		return fmt.Errorf("%w: management zone %q has unknown scope %q", ErrInvalidTopology, zone.ID, zone.ScopeID)
	}
	if zone.SiteID == "" {
		return nil
	}
	site, exists := topology.sites[zone.SiteID]
	if !exists {
		return fmt.Errorf("%w: management zone %q has unknown site %q", ErrInvalidTopology, zone.ID, zone.SiteID)
	}
	if !topology.Contains(site.ScopeID, zone.ScopeID) {
		return fmt.Errorf("%w: management zone %q scope %q is outside site %q", ErrInvalidTopology, zone.ID, zone.ScopeID, zone.SiteID)
	}
	return nil
}

func (t Topology) validatePathToRoot(id string) error {
	visited := make(map[string]struct{})
	current := id
	for {
		if _, exists := visited[current]; exists {
			return fmt.Errorf("%w: scope hierarchy contains a cycle at %q", ErrInvalidTopology, current)
		}
		visited[current] = struct{}{}
		scope := t.scopes[current]
		if scope.Kind == ScopeGlobal {
			if scope.ID != t.rootID {
				return fmt.Errorf("%w: scope %q does not resolve to global root %q", ErrInvalidTopology, id, t.rootID)
			}
			return nil
		}
		current = scope.ParentID
	}
}

func cloneScope(scope Scope) Scope {
	scope.DelegatedAuthorities = append([]DelegatedAuthority(nil), scope.DelegatedAuthorities...)
	return scope
}

func validateDisplayName(field, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is required and must not have surrounding whitespace", field)
	}
	if len(value) > maxIdentifierLength {
		return fmt.Errorf("%s exceeds %d bytes", field, maxIdentifierLength)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", field)
		}
	}
	return nil
}
