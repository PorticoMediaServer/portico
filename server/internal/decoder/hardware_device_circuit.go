package decoder

// Empty devices retain the legacy default-backend circuit. Explicit adapters
// have independent failure/recovery histories, including GPUs of one vendor.
func deviceCircuitKey(backend HardwareBackend, device string) HardwareBackend {
	if device == "" {
		return backend
	}
	return HardwareBackend(string(backend) + "\x00" + device)
}

func RecordHardwareDeviceFailure(backend HardwareBackend, device string) {
	RecordHardwareFailure(deviceCircuitKey(backend, device))
}

func deviceCircuitOpen(backend HardwareBackend, device string) bool {
	return hardwareCircuitOpen(deviceCircuitKey(backend, device))
}

func deviceNeedsProbe(backend HardwareBackend, device string) bool {
	return hardwareNeedsProbe(deviceCircuitKey(backend, device))
}

func clearDeviceCircuit(backend HardwareBackend, device string) {
	clearHardwareCircuit(deviceCircuitKey(backend, device))
}
