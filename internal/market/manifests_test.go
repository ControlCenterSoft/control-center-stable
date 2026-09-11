package market

import "testing"

func TestBuiltinManifestsAreValidAndSorted(t *testing.T) {
	manifests := BuiltinManifests()
	if len(manifests) < 7 {
		t.Fatalf("expected builtin manifests, got %d", len(manifests))
	}
	for index, manifest := range manifests {
		if err := ValidateBuiltinManifest(manifest); err != nil {
			t.Fatalf("manifest %q invalid: %v", manifest.ID, err)
		}
		if index > 0 && manifests[index-1].ID >= manifest.ID {
			t.Fatalf("manifests are not sorted: %q before %q", manifests[index-1].ID, manifest.ID)
		}
	}
}

func TestDirectoryServicesManifestExposesBothProviders(t *testing.T) {
	manifest, ok := FindBuiltinManifest("directory-services")
	if !ok {
		t.Fatal("directory-services manifest missing")
	}
	if len(manifest.Providers) != 2 || manifest.Providers[0] != "samba-ad-dc" || manifest.Providers[1] != "freeipa" {
		t.Fatalf("providers = %#v", manifest.Providers)
	}
}

func TestValidateBuiltinManifestRejectsInvalidLifecycle(t *testing.T) {
	manifest := BuiltinManifest{ID: "example", Version: "1.0.0", Capabilities: []string{"example"}, Platforms: []string{"linux"}, Lifecycle: []LifecycleOperation{LifecycleInstall, LifecycleInstall}}
	if err := ValidateBuiltinManifest(manifest); err == nil {
		t.Fatal("duplicate lifecycle operation accepted")
	}
}
