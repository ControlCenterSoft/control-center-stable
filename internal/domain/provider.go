package domain

import (
	"errors"
	"strings"
)

type Provider string

const (
	ProviderAuto    Provider = "auto"
	ProviderSamba   Provider = "samba-ad-dc"
	ProviderFreeIPA Provider = "freeipa"
)

const legacyProviderSamba Provider = "samba-ad"

var ErrIncompatibleProvider = errors.New("domain provider is incompatible with requirements")

type Requirements struct {
	WindowsDomainJoin bool
	GroupPolicy       bool
}

func canonicalProvider(value Provider) (Provider, bool) {
	switch Provider(strings.ToLower(strings.TrimSpace(string(value)))) {
	case "", ProviderAuto:
		return ProviderAuto, true
	case ProviderSamba, legacyProviderSamba:
		return ProviderSamba, true
	case ProviderFreeIPA:
		return ProviderFreeIPA, true
	default:
		return "", false
	}
}

func ResolveProvider(preferred Provider, requirements Requirements) (Provider, error) {
	canonical, ok := canonicalProvider(preferred)
	if !ok {
		return "", ErrIncompatibleProvider
	}
	switch canonical {
	case ProviderAuto:
		if requirements.WindowsDomainJoin || requirements.GroupPolicy {
			return ProviderSamba, nil
		}
		return ProviderFreeIPA, nil
	case ProviderSamba:
		return ProviderSamba, nil
	case ProviderFreeIPA:
		if requirements.WindowsDomainJoin || requirements.GroupPolicy {
			return "", ErrIncompatibleProvider
		}
		return ProviderFreeIPA, nil
	default:
		return "", ErrIncompatibleProvider
	}
}
