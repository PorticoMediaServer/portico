package writer

import (
	"testing"
	"time"
)

func TestWC008DraftSecurityKeepsIssuerSeparateAndNativeTimeExact(t *testing.T) {
	q := wc008SecurityCommand{family: "target", issuerFamily: "issuer", server: "server", account: "account", profile: "profile",
		memberIncarnation: "member-birth", profileLifetime: "profile-birth", identity: CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1},
		resultVersion: 1, oldControlBytes: 1000, epoch: 1, tokenGeneration: 1, appliedMS: 1_123, revokedAt: "1970-01-01T00:00:01Z"}
	if err := q.validate(); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*wc008SecurityCommand){
		func(q *wc008SecurityCommand) { q.family = q.issuerFamily },
		func(q *wc008SecurityCommand) { q.memberIncarnation = "" },
		func(q *wc008SecurityCommand) { q.profileLifetime = "" },
		func(q *wc008SecurityCommand) {
			q.revokedAt = time.UnixMilli(q.appliedMS).UTC().Format(time.RFC3339Nano)
		},
		func(q *wc008SecurityCommand) { q.identity.Sequence++ },
	} {
		changed := q
		edit(&changed)
		if err := changed.validate(); err == nil {
			t.Fatal("unsafe target/time/lifecycle accepted")
		}
	}
}

func TestWC008DraftSecurityNoTurnCannotExposeDomainPrefix(t *testing.T) {
	s, err := applyWC008SecurityDeny(t.Context(), nil, wc008SecurityCommand{})
	if err == nil || s != (wc008SecurityEffect{}) {
		t.Fatal("missing W turn returned security effects", s, err)
	}
	n, err := storeWC008SecurityResult(t.Context(), nil, wc008SecurityCommand{}, wc008IssuerScope{}, wc008SecurityEffect{}, RequestDigest{}, ResultDigest{})
	if err == nil || n != 0 {
		t.Fatal("missing W turn returned security storage", n, err)
	}
}
