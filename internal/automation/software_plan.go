package automation

import (
	"errors"
	"sort"
	"strings"
)

type Platform string

const (
	PlatformWindows Platform = "windows"
	PlatformLinux   Platform = "linux"
)

var ErrInvalidSoftwarePlan = errors.New("invalid software plan")

type PackageSpec struct {
	Name    string
	Version string
	Source  string
}

type SoftwarePlan struct {
	Platform Platform
	Packages []PackageSpec
}

func NormalizeSoftwarePlan(plan SoftwarePlan) (SoftwarePlan, error) {
	if plan.Platform != PlatformWindows && plan.Platform != PlatformLinux {
		return SoftwarePlan{}, ErrInvalidSoftwarePlan
	}

	seen := make(map[string]struct{}, len(plan.Packages))
	normalized := make([]PackageSpec, 0, len(plan.Packages))
	for _, pkg := range plan.Packages {
		pkg.Name = strings.TrimSpace(pkg.Name)
		pkg.Version = strings.TrimSpace(pkg.Version)
		pkg.Source = strings.ToLower(strings.TrimSpace(pkg.Source))
		if pkg.Name == "" || !sourceAllowed(plan.Platform, pkg.Source) {
			return SoftwarePlan{}, ErrInvalidSoftwarePlan
		}
		key := strings.ToLower(pkg.Name)
		if _, exists := seen[key]; exists {
			return SoftwarePlan{}, ErrInvalidSoftwarePlan
		}
		seen[key] = struct{}{}
		normalized = append(normalized, pkg)
	}

	sort.Slice(normalized, func(i, j int) bool {
		return strings.ToLower(normalized[i].Name) < strings.ToLower(normalized[j].Name)
	})
	return SoftwarePlan{Platform: plan.Platform, Packages: normalized}, nil
}

func sourceAllowed(platform Platform, source string) bool {
	switch platform {
	case PlatformWindows:
		return source == "ansible" || source == "winget" || source == "msi" || source == "powershell"
	case PlatformLinux:
		return source == "ansible" || source == "apt" || source == "dnf" || source == "snap"
	default:
		return false
	}
}
