package networking

import (
	"context"
	"testing"
)

// Remote access (the console setting) is the one switch for port mapping and
// the Portico address: the remote config reports it as Enabled, only "on"
// enables, the config can't store it, and a settings read error keeps the
// last answer.
func TestRemoteConfigEnabledIsTheRemoteAccessSetting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}
	f, remote, _, calls, ctx := contactFixture(t)
	f.store.installedGuard = GuardInstalledClaim
	enabled := func() bool {
		t.Helper()
		config, _, _, _, key, err := remote.snapshot(ctx)
		clear(key)
		if err != nil {
			t.Fatal(err)
		}
		return config.Enabled
	}
	if !enabled() {
		t.Fatal("no setting yet reads as On, the setting's default")
	}
	for _, c := range []struct {
		value string
		want  bool
	}{{"off", false}, {"private", false}, {"on", true}} {
		useRemoteAccess(remote, c.value)
		if got := enabled(); got != c.want {
			t.Fatalf("%s: enabled=%v", c.value, got)
		}
	}
	// The config can't switch it: Enabled in a configure call is ignored.
	useRemoteAccess(remote, "off")
	config, _, authority, _, key, err := remote.snapshot(ctx)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	config.Enabled = true
	if err := remote.configure(ctx, authority, config); err != nil {
		t.Fatal(err)
	}
	if enabled() {
		t.Fatal("configure turned remote access on")
	}
	// A read error ("") keeps the last answer.
	remote.SetRemoteAccess(func(context.Context) string { return "" })
	if enabled() {
		t.Fatal("a settings read error flipped remote access")
	}
	if calls.publishes != 0 || calls.names != 0 {
		t.Fatal("a local configuration read contacted Hosted")
	}
}
