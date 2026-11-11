package remote

// SetIPv4Loopback stands in for whether this host has IPv4 loopback, until restore.
func SetIPv4Loopback(has bool) (restore func()) {
	before := ipv4Loopback
	ipv4Loopback = func() bool { return has }
	return func() { ipv4Loopback = before }
}
