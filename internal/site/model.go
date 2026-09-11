package site

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidSite    = errors.New("invalid site")
	ErrDuplicateSite  = errors.New("duplicate site")
	ErrMissingParent  = errors.New("missing parent site")
	ErrHierarchyCycle = errors.New("site hierarchy cycle")
)

// ID is the stable identity of a site in the distributed Control Center hierarchy.
type ID string

// Site describes the minimum hierarchy contract required before introducing a
// Site Controller runtime. ParentID is empty only for a root site.
type Site struct {
	ID        ID
	Name      string
	ParentID  ID
	Delegated bool
}

// Validate checks the local invariants of one Site value.
func (s Site) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidSite)
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("%w: empty name for %q", ErrInvalidSite, s.ID)
	}
	if s.ParentID != "" && s.ParentID == s.ID {
		return fmt.Errorf("%w: %q cannot be its own parent", ErrHierarchyCycle, s.ID)
	}
	return nil
}

// ValidateHierarchy verifies that every parent exists, identities are unique,
// and the resulting site graph is acyclic. It deliberately permits multiple
// root sites so disconnected administrative trees can be modelled explicitly.
func ValidateHierarchy(sites []Site) error {
	byID := make(map[ID]Site, len(sites))
	for _, candidate := range sites {
		if err := candidate.Validate(); err != nil {
			return err
		}
		if _, exists := byID[candidate.ID]; exists {
			return fmt.Errorf("%w: %q", ErrDuplicateSite, candidate.ID)
		}
		byID[candidate.ID] = candidate
	}

	for _, candidate := range sites {
		if candidate.ParentID == "" {
			continue
		}
		if _, exists := byID[candidate.ParentID]; !exists {
			return fmt.Errorf("%w: site %q references %q", ErrMissingParent, candidate.ID, candidate.ParentID)
		}
	}

	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[ID]int, len(byID))
	var visit func(ID) error
	visit = func(id ID) error {
		switch state[id] {
		case visiting:
			return fmt.Errorf("%w: %q", ErrHierarchyCycle, id)
		case visited:
			return nil
		}
		state[id] = visiting
		parent := byID[id].ParentID
		if parent != "" {
			if err := visit(parent); err != nil {
				return err
			}
		}
		state[id] = visited
		return nil
	}

	for id := range byID {
		if state[id] == unvisited {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}

// Lineage returns the path from the root site to id after validating the full
// hierarchy. This is useful for deterministic scope inheritance and conflict
// reporting without embedding policy decisions in callers.
func Lineage(id ID, sites []Site) ([]ID, error) {
	if err := ValidateHierarchy(sites); err != nil {
		return nil, err
	}
	byID := make(map[ID]Site, len(sites))
	for _, candidate := range sites {
		byID[candidate.ID] = candidate
	}
	current, exists := byID[id]
	if !exists {
		return nil, fmt.Errorf("%w: unknown site %q", ErrInvalidSite, id)
	}

	lineage := []ID{current.ID}
	for current.ParentID != "" {
		current = byID[current.ParentID]
		lineage = append(lineage, current.ID)
	}
	for left, right := 0, len(lineage)-1; left < right; left, right = left+1, right-1 {
		lineage[left], lineage[right] = lineage[right], lineage[left]
	}
	return lineage, nil
}
