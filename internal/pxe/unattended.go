package pxe

import (
	"fmt"
	"strings"
)

// UnattendedRequest describes an unattended OS deployment request.
type UnattendedRequest struct {
	OSFamily     string
	ProfileName  string
	DomainEnroll bool
	PostInstall  []string
}

// InstallPhase is one deterministic step in an unattended installation plan.
type InstallPhase struct {
	Name    string
	Actions []string
}

// BuildUnattendedPlan creates a platform-neutral sequence for Windows or Linux deployment.
func BuildUnattendedPlan(request UnattendedRequest) ([]InstallPhase, error) {
	osFamily := strings.ToLower(strings.TrimSpace(request.OSFamily))
	if osFamily != "windows" && osFamily != "linux" {
		return nil, fmt.Errorf("unsupported OS family %q", request.OSFamily)
	}
	if strings.TrimSpace(request.ProfileName) == "" {
		return nil, fmt.Errorf("profile name is required")
	}

	phases := []InstallPhase{
		{Name: "prepare-boot", Actions: []string{"select-profile", "load-boot-artifacts"}},
		{Name: "install-os", Actions: []string{"partition-storage", "apply-operating-system"}},
		{Name: "configure-network", Actions: []string{"apply-network-config"}},
	}

	if request.DomainEnroll {
		action := "enroll-identity"
		if osFamily == "windows" {
			action = "join-domain"
		}
		phases = append(phases, InstallPhase{Name: "identity", Actions: []string{action}})
	}

	if len(request.PostInstall) > 0 {
		actions := append([]string(nil), request.PostInstall...)
		phases = append(phases, InstallPhase{Name: "post-install", Actions: actions})
	}
	phases = append(phases, InstallPhase{Name: "finalize", Actions: []string{"record-inventory", "reboot"}})
	return phases, nil
}
