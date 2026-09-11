package pxe

import "testing"

func TestBuildUnattendedPlanWindowsDomainJoin(t *testing.T) {
	phases, err := BuildUnattendedPlan(UnattendedRequest{
		OSFamily:     "windows",
		ProfileName:  "desktop",
		DomainEnroll: true,
		PostInstall:  []string{"install-management-agent"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := phases[3].Name, "identity"; got != want {
		t.Fatalf("phase=%q want=%q", got, want)
	}
	if got, want := phases[3].Actions[0], "join-domain"; got != want {
		t.Fatalf("identity action=%q want=%q", got, want)
	}
	if got, want := phases[len(phases)-1].Name, "finalize"; got != want {
		t.Fatalf("last phase=%q want=%q", got, want)
	}
}

func TestBuildUnattendedPlanLinuxEnrollment(t *testing.T) {
	phases, err := BuildUnattendedPlan(UnattendedRequest{OSFamily: "linux", ProfileName: "server", DomainEnroll: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := phases[3].Actions[0], "enroll-identity"; got != want {
		t.Fatalf("identity action=%q want=%q", got, want)
	}
}

func TestBuildUnattendedPlanRejectsUnsupportedOS(t *testing.T) {
	_, err := BuildUnattendedPlan(UnattendedRequest{OSFamily: "other", ProfileName: "generic"})
	if err == nil {
		t.Fatal("expected error")
	}
}
