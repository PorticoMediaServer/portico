package writer

import (
	"errors"
	"testing"
)

func TestUOSettingsConnectivityCoverage(t *testing.T) {
	for _, tc := range []struct {
		k   UOSettingKey
		max float64
	}{
		{UOSettingRemoteBitrateLimitKbps, 200000}, {UOSettingConnectionLimit, 65536}, {UOSettingUploadCapacityKbps, 100000000}, {UOSettingPausedSessionTimeoutMinutes, 1440},
	} {
		d, ok := UOSettingDefault(tc.k)
		if !ok || d.Number != 0 {
			t.Fatal(tc, d)
		}
		for _, n := range []float64{0, tc.max} {
			if _, e := UOValidateSetting(tc.k, UOValue{Kind: UOInteger, Number: n}); e != nil {
				t.Fatal(tc, n, e)
			}
		}
		for _, n := range []float64{-1, tc.max + 1, 0.5} {
			if _, e := UOValidateSetting(tc.k, UOValue{Kind: UOInteger, Number: n}); e == nil && !(tc.k == UOSettingRemoteBitrateLimitKbps && n != 0.5) {
				t.Fatal(tc, n)
			}
		}
	}
	v, e := UOValidateSetting(UOSettingAdvertiseUrl, UOValue{Kind: UOText, Text: " https://Example.COM/ "})
	if e != nil || v.Text != "https://example.com" {
		t.Fatal(v, e)
	}
	for _, s := range []string{"https://host/path", "https://user@host", "https://host:0", "https://host?", "https://host/#x"} {
		if _, e := UOValidateSetting(UOSettingAdvertiseUrl, UOValue{Kind: UOText, Text: s}); e == nil {
			t.Fatal(s)
		}
	}
	if _, e := UOValidateSetting(UOSettingAdvertisedInterface, UOValue{Kind: UOText, Text: "Ethernet 2"}); e != nil {
		t.Fatal(e)
	}
	if _, e := UOValidateSetting(UOSettingAdvertisedInterface, UOValue{Kind: UOText, Text: "eth/0"}); e == nil {
		t.Fatal("interface")
	}
	d, _ := UOSettingDefault(UOSettingLanDiscoveryEnabled)
	if !d.Bool {
		t.Fatal(d)
	}
}

func TestUOSettingsCapsExactPolicyCAS(t *testing.T) {
	w := wc008NativeExecutor(t)
	uoSettingsSeed(t, w)
	wc008NativeExec(t, w, `INSERT INTO playback_owner_policy(singleton,revision,transcoding_enabled) VALUES(1,1,1)`)
	p, e := uoCapPatch(UOSettingServerCap, uoNullableCap{limited: true, limit: 5})
	if e != nil {
		t.Fatal(e)
	}
	q := uoTestSettings(1, p)
	q.expectedPolicyRevision = 1
	turn := uoTestTurn(t, w, uoSettingsDefinitions())
	out, e := applyUOSettings(t.Context(), turn, q)
	if e != nil || out.policyRevision != 2 || out.revision != 2 {
		t.Fatal(out, e)
	}
	uoTestCommit(t, turn)
	// A stale projection cannot clear this successor's durable cap intent.
	stale := uoTestTurn(t, w, uoSettingsDefinitions())
	if _, e = acceptUOConfigurationProjection(t.Context(), stale, uoProjectionResult{field: UOSettingServerCap, pendingRevision: 1, currentGeneration: 2, succeeded: true}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = stale.transaction.rollback(nil); e != nil {
		t.Fatal(e)
	}
	var cap, rev int64
	if e = w.db.QueryRowContext(t.Context(), `SELECT server_cap,revision FROM playback_owner_policy WHERE singleton=1`).Scan(&cap, &rev); e != nil || cap != 5 || rev != 2 {
		t.Fatal(cap, rev, e)
	}
	q.expectedRevision = 2
	turn = uoTestTurn(t, w, uoSettingsDefinitions())
	if _, e = applyUOSettings(t.Context(), turn, q); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = turn.transaction.rollback(nil); e != nil {
		t.Fatal(e)
	}
}
