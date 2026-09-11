package pxe

import "fmt"

// BootArtifactPlan describes the minimal PXE artifacts required for a target.
type BootArtifactPlan struct {
	Platform string
	Firmware string
	Loader   string
	Kernel   string
	Initrd   string
}

// BuildBootArtifactPlan returns a normalized boot artifact plan.
func BuildBootArtifactPlan(platform, firmware string) (BootArtifactPlan, error) {
	switch platform {
	case "windows":
		if firmware != "uefi" && firmware != "bios" {
			return BootArtifactPlan{}, fmt.Errorf("unsupported firmware: %s", firmware)
		}
		return BootArtifactPlan{Platform: platform, Firmware: firmware, Loader: "wimboot"}, nil
	case "linux":
		if firmware != "uefi" && firmware != "bios" {
			return BootArtifactPlan{}, fmt.Errorf("unsupported firmware: %s", firmware)
		}
		return BootArtifactPlan{Platform: platform, Firmware: firmware, Loader: "ipxe", Kernel: "vmlinuz", Initrd: "initrd"}, nil
	default:
		return BootArtifactPlan{}, fmt.Errorf("unsupported platform: %s", platform)
	}
}
