package compactcatalog

// RebuildDomains returns the registered owner commands in stable order. It
// includes domains registered by other subsystems in this server binary.
func RebuildDomains() []int { return registeredDomains() }
