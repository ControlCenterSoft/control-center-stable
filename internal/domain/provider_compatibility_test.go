package domain

import "testing"

func TestResolveProviderCanonicalizesLegacySamba(t *testing.T) {
	for _, input := range []Provider{ProviderSamba, legacyProviderSamba} {
		resolved, err := ResolveProvider(input, Requirements{WindowsDomainJoin: true})
		if err != nil {
			t.Fatalf("provider %q: %v", input, err)
		}
		if resolved != ProviderSamba {
			t.Fatalf("provider %q resolved to %q", input, resolved)
		}
	}
}

func TestJoinAndReadinessAcceptCanonicalSamba(t *testing.T) {
	if err := ValidateDirectoryJoin(DirectoryJoinRequest{
		Platform: "windows", Provider: string(ProviderSamba), DomainName: "example.test",
	}); err != nil {
		t.Fatalf("canonical Samba join rejected: %v", err)
	}
	ready := EvaluateDomainReadiness(string(ProviderSamba), true, true, true)
	if !ready.Ready || len(ready.Blockers) != 0 {
		t.Fatalf("canonical Samba readiness = %#v", ready)
	}
}

func TestAutoIsNotValidForConcreteJoinOrReadiness(t *testing.T) {
	if err := ValidateDirectoryJoin(DirectoryJoinRequest{
		Platform: "linux", Provider: string(ProviderAuto), DomainName: "example.test",
	}); err == nil {
		t.Fatal("auto provider accepted for concrete join")
	}
	ready := EvaluateDomainReadiness(string(ProviderAuto), true, true, true)
	if ready.Ready || len(ready.Blockers) != 1 || ready.Blockers[0] != "unsupported-provider" {
		t.Fatalf("auto readiness = %#v", ready)
	}
}
