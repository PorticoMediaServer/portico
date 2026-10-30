//go:build !windows && !darwin

package hostlimits

// Every other Unix honours a soft limit raised to the hard one, so there is no
// second ceiling to discover.
func platformFileCeiling() (uint64, bool) { return 0, false }

func rlimitValue(v uint64) uint64 { return v }
