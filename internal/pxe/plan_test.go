package pxe

import "testing"

func TestWindowsPlanIncludesWinPEAndAutomationHandoff(t *testing.T) {
	plan, err := BuildPlan(Profile{Name: "windows-standard", OSFamily: "windows", Architecture: "amd64", Unattended: true, PostInstallAutomation: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 5 {
		t.Fatalf("steps = %#v", plan.Steps)
	}
	if plan.Steps[1].Action != "wimboot-start-winpe" || plan.Steps[4].Action != "handoff-post-install-automation" {
		t.Fatalf("unexpected Windows plan: %#v", plan.Steps)
	}
}

func TestLinuxPlanSupportsArm64(t *testing.T) {
	plan, err := BuildPlan(Profile{Name: "linux-arm64", OSFamily: "linux", Architecture: "arm64", Unattended: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Architecture != "arm64" || plan.Steps[1].Action != "load-kernel-initrd" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestWindowsPlanRejectsArm64(t *testing.T) {
	if _, err := BuildPlan(Profile{Name: "invalid", OSFamily: "windows", Architecture: "arm64"}); err == nil {
		t.Fatal("unsupported Windows arm64 profile accepted")
	}
}
