package networking

import "testing"

func TestNewRemoteScopeUsesOnlyInstalledPorticoLifecycleDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, test := range []struct {
		name, kind                  string
		managed, acknowledged, want bool
	}{
		{"Local", "local", true, true, false},
		{"no kind", "", true, true, false},
		{"Portico before ACK", "portico", true, false, false},
		{"Portico managed off", "portico", false, true, false},
		{"Portico installed", "portico", true, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, remote, _, calls, ctx := contactFixture(t)
			f.store.installedGuard = GuardInstalledClaim
			if test.kind != "" {
				if _, err := f.db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,?)`, test.kind); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.db.Exec(`UPDATE identity_managed_route SET enabled=? WHERE singleton=1`, test.managed); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(`UPDATE networking_claim_intents SET installation_acknowledged=? WHERE stage='installed'`, test.acknowledged); err != nil {
				t.Fatal(err)
			}
			config, _, authority, _, key, err := remote.snapshot(ctx)
			clear(key)
			if err != nil || config.Enabled != test.want {
				t.Fatalf("new scope enabled=%v want=%v error=%v", config.Enabled, test.want, err)
			}
			if calls.publishes != 0 || calls.names != 0 {
				t.Fatal("local configuration read contacted Hosted")
			}
			var observations int
			if err := f.db.QueryRow(`SELECT count(*) FROM networking_claim_authority`).Scan(&observations); err != nil || observations != 0 {
				t.Fatal("new scope required a fabricated authority observation", err)
			}
			if test.want {
				config.Enabled = false
				if err := remote.configure(ctx, authority, config); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					config, _, current, _, key, err := remote.snapshot(ctx)
					clear(key)
					if err != nil || current != authority || config.Enabled {
						t.Fatal("same-scope read overrode the owner's off choice", err)
					}
				}
			}
		})
	}
}
