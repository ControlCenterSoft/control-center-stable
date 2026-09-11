package market

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type LifecycleOperation string

const (
	LifecycleInstall LifecycleOperation = "install"
	LifecycleUpgrade LifecycleOperation = "upgrade"
	LifecycleRemove  LifecycleOperation = "remove"
)

type BuiltinManifest struct {
	ID           string               `json:"id"`
	Version      string               `json:"version"`
	Capabilities []string             `json:"capabilities"`
	Platforms    []string             `json:"platforms"`
	Providers    []string             `json:"providers,omitempty"`
	Lifecycle    []LifecycleOperation `json:"lifecycle"`
}

var manifestVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func ValidateBuiltinManifest(manifest BuiltinManifest) error {
	manifest.ID = strings.TrimSpace(manifest.ID)
	manifest.Version = strings.TrimSpace(manifest.Version)
	if manifest.ID == "" || !manifestVersionPattern.MatchString(manifest.Version) {
		return errors.New("manifest id and semantic version are required")
	}
	if len(manifest.Capabilities) == 0 || len(manifest.Platforms) == 0 || len(manifest.Lifecycle) == 0 {
		return errors.New("manifest capabilities, platforms, and lifecycle are required")
	}
	seen := map[LifecycleOperation]bool{}
	for _, operation := range manifest.Lifecycle {
		if operation != LifecycleInstall && operation != LifecycleUpgrade && operation != LifecycleRemove {
			return fmt.Errorf("unsupported lifecycle operation %q", operation)
		}
		if seen[operation] {
			return fmt.Errorf("duplicate lifecycle operation %q", operation)
		}
		seen[operation] = true
	}
	return nil
}

func BuiltinManifests() []BuiltinManifest {
	manifests := []BuiltinManifest{
		{ID: "directory-services", Version: "0.1.0", Capabilities: []string{"identity", "directory", "policy"}, Platforms: []string{"linux"}, Providers: []string{"samba-ad-dc", "freeipa"}, Lifecycle: []LifecycleOperation{LifecycleInstall, LifecycleUpgrade, LifecycleRemove}},
		{ID: "pxe-deployment", Version: "0.1.0", Capabilities: []string{"windows-deployment", "linux-deployment"}, Platforms: []string{"linux"}, Lifecycle: []LifecycleOperation{LifecycleInstall, LifecycleUpgrade, LifecycleRemove}},
		{ID: "software-automation", Version: "0.1.0", Capabilities: []string{"package-management", "configuration"}, Platforms: []string{"linux", "windows"}, Lifecycle: []LifecycleOperation{LifecycleInstall, LifecycleUpgrade, LifecycleRemove}},
		{ID: "endpoint-inventory", Version: "0.1.0", Capabilities: []string{"inventory"}, Platforms: []string{"linux", "windows"}, Lifecycle: []LifecycleOperation{LifecycleInstall, LifecycleUpgrade, LifecycleRemove}},
		{ID: "dns-dhcp", Version: "0.1.0", Capabilities: []string{"dns", "dhcp"}, Platforms: []string{"linux"}, Lifecycle: []LifecycleOperation{LifecycleInstall, LifecycleUpgrade, LifecycleRemove}},
		{ID: "file-services", Version: "0.1.0", Capabilities: []string{"smb", "nfs"}, Platforms: []string{"linux"}, Lifecycle: []LifecycleOperation{LifecycleInstall, LifecycleUpgrade, LifecycleRemove}},
		{ID: "monitoring", Version: "0.1.0", Capabilities: []string{"metrics", "health"}, Platforms: []string{"linux", "windows"}, Lifecycle: []LifecycleOperation{LifecycleInstall, LifecycleUpgrade, LifecycleRemove}},
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].ID < manifests[j].ID })
	return manifests
}

func FindBuiltinManifest(id string) (BuiltinManifest, bool) {
	id = strings.TrimSpace(id)
	for _, manifest := range BuiltinManifests() {
		if manifest.ID == id {
			return manifest, true
		}
	}
	return BuiltinManifest{}, false
}
