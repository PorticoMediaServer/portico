package identity

import "testing"

// Same fixed vector as writer's local fixture helper; keep identity/dbwork
// imports out of that isolated test binary's SQLite function fingerprint.
func TestDigestUsesCanonicalUnpaddedBase64URL(t *testing.T) {
	if Digest("pat.synthetic-only-format-control") != "OkT38SlHRMRlkodpCE2-xIjzwyNcb_XpF1188AFx9oM" {
		t.Fatal("stored credential digest encoding changed")
	}
}
