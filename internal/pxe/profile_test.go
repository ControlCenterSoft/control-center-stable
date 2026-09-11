package pxe

import (
	"errors"
	"testing"
)

func TestDeploymentProfileValidateWindowsUEFI(t *testing.T) {
	profile := DeploymentProfile{
		ID:               "windows-11",
		Family:           OSWindows,
		Architecture:     "amd64",
		Firmware:         FirmwareUEFI,
		BootArtifact:     "winpe/boot.wim",
		UnattendedConfig: "unattend/windows-11.xml",
	}
	if err := profile.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDeploymentProfileValidateLinuxBIOS(t *testing.T) {
	profile := DeploymentProfile{
		ID:           "ubuntu-server",
		Family:       OSLinux,
		Architecture: "amd64",
		Firmware:     FirmwareBIOS,
		BootArtifact: "linux/vmlinuz",
	}
	if err := profile.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDeploymentProfileRejectsInvalidFamily(t *testing.T) {
	profile := DeploymentProfile{
		ID:           "bad",
		Family:       OSFamily("other"),
		Architecture: "amd64",
		Firmware:     FirmwareUEFI,
		BootArtifact: "boot",
	}
	if err := profile.Validate(); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("Validate() error = %v, want ErrInvalidProfile", err)
	}
}
