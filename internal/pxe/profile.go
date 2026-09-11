package pxe

import (
	"errors"
	"strings"
)

type OSFamily string

type Firmware string

const (
	OSWindows OSFamily = "windows"
	OSLinux   OSFamily = "linux"

	FirmwareUEFI Firmware = "uefi"
	FirmwareBIOS Firmware = "bios"
)

var ErrInvalidProfile = errors.New("invalid PXE deployment profile")

type DeploymentProfile struct {
	ID               string
	Family           OSFamily
	Architecture     string
	Firmware         Firmware
	BootArtifact     string
	UnattendedConfig string
}

func (p DeploymentProfile) Validate() error {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.BootArtifact) == "" {
		return ErrInvalidProfile
	}
	if p.Family != OSWindows && p.Family != OSLinux {
		return ErrInvalidProfile
	}
	if p.Architecture != "amd64" && p.Architecture != "arm64" {
		return ErrInvalidProfile
	}
	if p.Firmware != FirmwareUEFI && p.Firmware != FirmwareBIOS {
		return ErrInvalidProfile
	}
	return nil
}
