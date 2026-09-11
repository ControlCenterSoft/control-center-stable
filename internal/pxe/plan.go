package pxe

import (
	"errors"
	"fmt"
	"strings"
)

type Profile struct {
	Name                  string `json:"name"`
	OSFamily              string `json:"osFamily"`
	Architecture          string `json:"architecture"`
	Unattended            bool   `json:"unattended"`
	PostInstallAutomation bool   `json:"postInstallAutomation"`
}

type Step struct {
	Order  int    `json:"order"`
	Action string `json:"action"`
}

type Plan struct {
	ProfileName  string `json:"profileName"`
	OSFamily     string `json:"osFamily"`
	Architecture string `json:"architecture"`
	Steps        []Step `json:"steps"`
}

func BuildPlan(profile Profile) (Plan, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.OSFamily = strings.ToLower(strings.TrimSpace(profile.OSFamily))
	profile.Architecture = strings.ToLower(strings.TrimSpace(profile.Architecture))
	if profile.Name == "" {
		return Plan{}, errors.New("profile name is required")
	}
	if profile.Architecture != "amd64" && profile.Architecture != "arm64" {
		return Plan{}, fmt.Errorf("unsupported architecture %q", profile.Architecture)
	}
	steps := []Step{{Order: 1, Action: "ipxe-select-profile"}}
	switch profile.OSFamily {
	case "windows":
		if profile.Architecture != "amd64" {
			return Plan{}, errors.New("windows PXE currently requires amd64")
		}
		steps = append(steps,
			Step{Order: 2, Action: "wimboot-start-winpe"},
			Step{Order: 3, Action: "windows-setup"},
		)
		if profile.Unattended {
			steps = append(steps, Step{Order: len(steps) + 1, Action: "apply-unattended-profile"})
		}
	case "linux":
		steps = append(steps,
			Step{Order: 2, Action: "load-kernel-initrd"},
			Step{Order: 3, Action: "linux-installer"},
		)
		if profile.Unattended {
			steps = append(steps, Step{Order: len(steps) + 1, Action: "apply-autoinstall-profile"})
		}
	default:
		return Plan{}, fmt.Errorf("unsupported OS family %q", profile.OSFamily)
	}
	if profile.PostInstallAutomation {
		steps = append(steps, Step{Order: len(steps) + 1, Action: "handoff-post-install-automation"})
	}
	return Plan{ProfileName: profile.Name, OSFamily: profile.OSFamily, Architecture: profile.Architecture, Steps: steps}, nil
}
