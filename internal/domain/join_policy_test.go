package domain

import "testing"

func TestValidateDirectoryJoinAllowsWindowsSamba(t *testing.T) {
	err := ValidateDirectoryJoin(DirectoryJoinRequest{Platform: "windows", Provider: "samba-ad", DomainName: "example.test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDirectoryJoinRejectsWindowsFreeIPA(t *testing.T) {
	err := ValidateDirectoryJoin(DirectoryJoinRequest{Platform: "windows", Provider: "freeipa", DomainName: "example.test"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateDirectoryJoinAllowsLinuxProviders(t *testing.T) {
	for _, provider := range []string{"samba-ad", "freeipa"} {
		if err := ValidateDirectoryJoin(DirectoryJoinRequest{Platform: "linux", Provider: provider, DomainName: "example.test"}); err != nil {
			t.Fatalf("provider %s: unexpected error: %v", provider, err)
		}
	}
}

func TestValidateDirectoryJoinRequiresDomainName(t *testing.T) {
	if err := ValidateDirectoryJoin(DirectoryJoinRequest{Platform: "linux", Provider: "freeipa"}); err == nil {
		t.Fatal("expected error")
	}
}
