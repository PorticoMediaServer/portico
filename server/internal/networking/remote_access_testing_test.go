package networking

import "context"

// useRemoteAccess sets the Remote access setting the manager reads (on,
// private or off) and refreshes its cached answer, as a settings save does.
func useRemoteAccess(m *RemoteManager, value string) {
	m.SetRemoteAccess(func(context.Context) string { return value })
	m.remoteOn(context.Background())
}
