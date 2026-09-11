package domain

import "sort"

// DomainReadiness describes whether a selected identity provider can be deployed.
type DomainReadiness struct {
	Ready    bool
	Blockers []string
}

// EvaluateDomainReadiness validates provider prerequisites without deployment bindings.
func EvaluateDomainReadiness(provider string, dnsReady, timeSyncReady, storageReady bool) DomainReadiness {
	blockers := make([]string, 0)
	canonical, ok := canonicalProvider(Provider(provider))
	if !ok || canonical == ProviderAuto {
		blockers = append(blockers, "unsupported-provider")
	}
	if !dnsReady {
		blockers = append(blockers, "dns-not-ready")
	}
	if !timeSyncReady {
		blockers = append(blockers, "time-sync-not-ready")
	}
	if !storageReady {
		blockers = append(blockers, "storage-not-ready")
	}
	sort.Strings(blockers)
	return DomainReadiness{Ready: len(blockers) == 0, Blockers: blockers}
}
