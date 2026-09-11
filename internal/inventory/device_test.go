package inventory

import (
	"errors"
	"testing"
)

func TestNormalizeDevice(t *testing.T) {
	device, err := NormalizeDevice(Device{
		Hostname:  " workstation-01 ",
		Platform:  PlatformWindows,
		MachineID: "machine-123",
		Addresses: []string{"10.0.0.20", "10.0.0.20", " 10.0.0.10 "},
		Tags:      []string{"Office", "office", "Managed"},
	})
	if err != nil {
		t.Fatalf("NormalizeDevice() error = %v", err)
	}
	if device.Hostname != "workstation-01" {
		t.Fatalf("Hostname = %q", device.Hostname)
	}
	if len(device.Addresses) != 2 || device.Addresses[0] != "10.0.0.10" {
		t.Fatalf("Addresses = %#v", device.Addresses)
	}
	if len(device.Tags) != 2 || device.Tags[0] != "Managed" || device.Tags[1] != "Office" {
		t.Fatalf("Tags = %#v", device.Tags)
	}
}

func TestNormalizeDeviceRequiresStableIdentity(t *testing.T) {
	_, err := NormalizeDevice(Device{Hostname: "host", Platform: PlatformLinux})
	if !errors.Is(err, ErrInvalidDevice) {
		t.Fatalf("error = %v, want ErrInvalidDevice", err)
	}
}

func TestNormalizeDeviceSupportsAndroid(t *testing.T) {
	_, err := NormalizeDevice(Device{
		Hostname: "tablet",
		Platform: PlatformAndroid,
		Serial:   "serial-1",
	})
	if err != nil {
		t.Fatalf("NormalizeDevice() error = %v", err)
	}
}
