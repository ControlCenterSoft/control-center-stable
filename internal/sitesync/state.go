package sitesync

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidRecord   = errors.New("invalid site state record")
	ErrWrongAuthority  = errors.New("wrong state authority")
	ErrStaleGeneration = errors.New("stale state generation")
	ErrStateConflict   = errors.New("site state conflict")
)

// StateKind separates desired state, which is distributed top-down, from
// actual state, which is reported bottom-up by a Site Controller.
type StateKind string

const (
	DesiredState StateKind = "desired"
	ActualState  StateKind = "actual"
)

// Authority identifies the only side allowed to originate a record kind.
type Authority string

const (
	GlobalAuthority Authority = "global"
	SiteAuthority   Authority = "site"
)

// Record is a compact synchronization envelope. PayloadHash represents the
// canonical payload without coupling synchronization logic to a concrete
// resource schema.
type Record struct {
	SiteID          string
	ResourceID      string
	Kind            StateKind
	Authority       Authority
	Generation      uint64
	ResourceVersion string
	PayloadHash     string
}

func (r Record) Validate() error {
	if strings.TrimSpace(r.SiteID) == "" || strings.TrimSpace(r.ResourceID) == "" {
		return fmt.Errorf("%w: empty identity", ErrInvalidRecord)
	}
	if r.Generation == 0 || strings.TrimSpace(r.ResourceVersion) == "" || strings.TrimSpace(r.PayloadHash) == "" {
		return fmt.Errorf("%w: incomplete revision", ErrInvalidRecord)
	}
	switch r.Kind {
	case DesiredState:
		if r.Authority != GlobalAuthority {
			return fmt.Errorf("%w: desired state must originate globally", ErrWrongAuthority)
		}
	case ActualState:
		if r.Authority != SiteAuthority {
			return fmt.Errorf("%w: actual state must originate at the site", ErrWrongAuthority)
		}
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidRecord, r.Kind)
	}
	return nil
}

// Direction makes the synchronization direction explicit for callers and
// observability: desired state flows global -> site, actual state site -> global.
func (r Record) Direction() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	if r.Kind == DesiredState {
		return "global-to-site", nil
	}
	return "site-to-global", nil
}

// Reconcile compares one authoritative incoming revision with the currently
// stored revision. Equal revisions are idempotent; same-generation divergence
// is an explicit conflict and must never be silently overwritten.
func Reconcile(current, incoming Record) (Record, bool, error) {
	if err := current.Validate(); err != nil {
		return Record{}, false, err
	}
	if err := incoming.Validate(); err != nil {
		return Record{}, false, err
	}
	if current.SiteID != incoming.SiteID || current.ResourceID != incoming.ResourceID || current.Kind != incoming.Kind {
		return Record{}, false, fmt.Errorf("%w: identity mismatch", ErrInvalidRecord)
	}
	if current.Authority != incoming.Authority {
		return Record{}, false, ErrWrongAuthority
	}
	if incoming.Generation < current.Generation {
		return Record{}, false, ErrStaleGeneration
	}
	if incoming.Generation == current.Generation {
		if incoming.ResourceVersion == current.ResourceVersion && incoming.PayloadHash == current.PayloadHash {
			return current, false, nil
		}
		return Record{}, false, fmt.Errorf(
			"%w: %s/%s generation %d diverged",
			ErrStateConflict,
			current.SiteID,
			current.ResourceID,
			current.Generation,
		)
	}
	return incoming, true, nil
}
