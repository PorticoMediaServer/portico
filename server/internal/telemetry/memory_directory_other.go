//go:build !linux

package telemetry

// memoryTranscodeDirectory has no candidate RAM conversion directory on this platform.
func memoryTranscodeDirectory() string { return "" }
