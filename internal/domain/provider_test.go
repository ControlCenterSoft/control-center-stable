package domain

import (
	"errors"
	"testing"
)

func TestResolveProviderAutoUsesSambaForWindowsDomain(t *testing.T) {
	got, err := ResolveProvider(ProviderAuto, Requirements{WindowsDomainJoin: true})
	if err != nil {
		t.Fatalf("ResolveProvider() error = %v", err)
	}
	if got != ProviderSamba {
		t.Fatalf("provider = %q, want %q", got, ProviderSamba)
	}
}

func TestResolveProviderAutoUsesFreeIPAWithoutWindowsRequirements(t *testing.T) {
	got, err := ResolveProvider(ProviderAuto, Requirements{})
	if err != nil {
		t.Fatalf("ResolveProvider() error = %v", err)
	}
	if got != ProviderFreeIPA {
		t.Fatalf("provider = %q, want %q", got, ProviderFreeIPA)
	}
}

func TestResolveProviderHonorsExplicitSambaChoice(t *testing.T) {
	got, err := ResolveProvider(ProviderSamba, Requirements{})
	if err != nil || got != ProviderSamba {
		t.Fatalf("ResolveProvider() = %q, %v", got, err)
	}
}

func TestResolveProviderRejectsFreeIPAForGroupPolicy(t *testing.T) {
	_, err := ResolveProvider(ProviderFreeIPA, Requirements{GroupPolicy: true})
	if !errors.Is(err, ErrIncompatibleProvider) {
		t.Fatalf("error = %v, want ErrIncompatibleProvider", err)
	}
}
