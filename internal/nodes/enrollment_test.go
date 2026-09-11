package nodes

import "testing"

func TestPlanEnrollmentNormalizesCapabilities(t *testing.T) {
	plan, err := PlanEnrollment(EnrollmentRequest{
		NodeID:       "node-01",
		DisplayName:  "Node 01",
		OSFamily:     "Linux",
		Architecture: "AMD64",
		Capabilities: []Capability{CapabilityMonitoring, CapabilityAutomation, CapabilityMonitoring},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Capabilities) != 2 || plan.Capabilities[0] != CapabilityAutomation || plan.Capabilities[1] != CapabilityMonitoring {
		t.Fatalf("capabilities = %#v", plan.Capabilities)
	}
	if len(plan.Steps) != 4 || plan.Steps[0].Order != 1 || plan.Steps[3].Order != 4 {
		t.Fatalf("steps = %#v", plan.Steps)
	}
}

func TestPlanEnrollmentRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := PlanEnrollment(EnrollmentRequest{NodeID: "node-02", DisplayName: "Node 02", OSFamily: "windows", Architecture: "arm64"}); err == nil {
		t.Fatal("unsupported Windows architecture accepted")
	}
	if _, err := PlanEnrollment(EnrollmentRequest{NodeID: "node-03", DisplayName: "Node 03", OSFamily: "other", Architecture: "amd64"}); err == nil {
		t.Fatal("unsupported OS family accepted")
	}
}
