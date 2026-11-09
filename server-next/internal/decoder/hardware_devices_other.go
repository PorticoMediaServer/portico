//go:build !linux && !windows

package decoder

import "context"

func DiscoverHardwareDevices(ctx context.Context) []HardwareDevice { return discoverNVIDIADevices(ctx) }
