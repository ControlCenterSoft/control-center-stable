package inventory

import (
	"errors"
	"sort"
	"strings"
)

type Platform string

const (
	PlatformWindows Platform = "windows"
	PlatformLinux   Platform = "linux"
	PlatformAndroid Platform = "android"
	PlatformOther   Platform = "other"
)

var ErrInvalidDevice = errors.New("invalid device inventory record")

type Device struct {
	Hostname  string
	Platform  Platform
	MachineID string
	Serial    string
	Addresses []string
	Tags      []string
}

func NormalizeDevice(device Device) (Device, error) {
	device.Hostname = strings.TrimSpace(device.Hostname)
	device.MachineID = strings.TrimSpace(device.MachineID)
	device.Serial = strings.TrimSpace(device.Serial)
	if device.Hostname == "" || (device.MachineID == "" && device.Serial == "") {
		return Device{}, ErrInvalidDevice
	}
	switch device.Platform {
	case PlatformWindows, PlatformLinux, PlatformAndroid, PlatformOther:
	default:
		return Device{}, ErrInvalidDevice
	}

	device.Addresses = normalizeStrings(device.Addresses)
	device.Tags = normalizeStrings(device.Tags)
	return device, nil
}

func normalizeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i]) < strings.ToLower(result[j])
	})
	return result
}
