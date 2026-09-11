package automation

import (
	"errors"
	"testing"
)

func TestNormalizeSoftwarePlanWindows(t *testing.T) {
	plan, err := NormalizeSoftwarePlan(SoftwarePlan{
		Platform: PlatformWindows,
		Packages: []PackageSpec{
			{Name: "7zip", Version: "24.08", Source: "winget"},
			{Name: "Agent", Version: "1.0", Source: "Ansible"},
		},
	})
	if err != nil {
		t.Fatalf("NormalizeSoftwarePlan() error = %v", err)
	}
	if len(plan.Packages) != 2 || plan.Packages[0].Name != "7zip" || plan.Packages[1].Name != "Agent" {
		t.Fatalf("packages = %#v", plan.Packages)
	}
}

func TestNormalizeSoftwarePlanLinux(t *testing.T) {
	_, err := NormalizeSoftwarePlan(SoftwarePlan{
		Platform: PlatformLinux,
		Packages: []PackageSpec{{Name: "nginx", Source: "apt"}},
	})
	if err != nil {
		t.Fatalf("NormalizeSoftwarePlan() error = %v", err)
	}
}

func TestNormalizeSoftwarePlanRejectsWrongSource(t *testing.T) {
	_, err := NormalizeSoftwarePlan(SoftwarePlan{
		Platform: PlatformLinux,
		Packages: []PackageSpec{{Name: "tool", Source: "winget"}},
	})
	if !errors.Is(err, ErrInvalidSoftwarePlan) {
		t.Fatalf("error = %v, want ErrInvalidSoftwarePlan", err)
	}
}

func TestNormalizeSoftwarePlanRejectsDuplicatesCaseInsensitive(t *testing.T) {
	_, err := NormalizeSoftwarePlan(SoftwarePlan{
		Platform: PlatformWindows,
		Packages: []PackageSpec{
			{Name: "Agent", Source: "ansible"},
			{Name: "agent", Source: "winget"},
		},
	})
	if !errors.Is(err, ErrInvalidSoftwarePlan) {
		t.Fatalf("error = %v, want ErrInvalidSoftwarePlan", err)
	}
}
