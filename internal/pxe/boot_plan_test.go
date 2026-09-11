package pxe

import "testing"

func TestBuildBootArtifactPlan(t *testing.T) {
	windows, err := BuildBootArtifactPlan("windows", "uefi")
	if err != nil || windows.Loader != "wimboot" {
		t.Fatalf("unexpected windows plan: %#v err=%v", windows, err)
	}
	linux, err := BuildBootArtifactPlan("linux", "bios")
	if err != nil || linux.Kernel != "vmlinuz" || linux.Initrd != "initrd" {
		t.Fatalf("unexpected linux plan: %#v err=%v", linux, err)
	}
	if _, err := BuildBootArtifactPlan("other", "uefi"); err == nil {
		t.Fatal("expected unsupported platform error")
	}
}
