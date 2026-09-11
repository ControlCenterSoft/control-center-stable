package domain

import (
	"fmt"
	"strings"
)

// DirectoryJoinRequest describes a host enrollment into a selected identity provider.
type DirectoryJoinRequest struct {
	Platform   string
	Provider   string
	DomainName string
}

// ValidateDirectoryJoin enforces provider/platform compatibility.
func ValidateDirectoryJoin(request DirectoryJoinRequest) error {
	platform := strings.ToLower(strings.TrimSpace(request.Platform))
	domainName := strings.TrimSpace(request.DomainName)

	if domainName == "" {
		return fmt.Errorf("domain name is required")
	}
	if platform != "windows" && platform != "linux" {
		return fmt.Errorf("unsupported platform %q", request.Platform)
	}
	provider, ok := canonicalProvider(Provider(request.Provider))
	if !ok || provider == ProviderAuto {
		return fmt.Errorf("unsupported provider %q", request.Provider)
	}
	if platform == "windows" && provider != ProviderSamba {
		return fmt.Errorf("windows domain join requires %s", ProviderSamba)
	}
	return nil
}
