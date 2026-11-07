package writer

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

const usLinkFixtureServer = "srv_7BFIeYyXBpRWRLzgrvXRa8Ml2H7kI5jz7XcCg1ymsaE"

func usInvitationLinkFixture(t *testing.T) (usInvitationLinkBinding, []byte) {
	t.Helper()
	key, err := hex.DecodeString("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
	if err != nil {
		t.Fatal(err)
	}
	p, err := prepareUSInvitationLink("local", 1, usLinkFixtureServer, key, "key-life", 3)
	if err != nil {
		t.Fatal(err)
	}
	return p, key
}
func TestUSInvitationLinkPreparationAndRequestDigest(t *testing.T) {
	link, key := usInvitationLinkFixture(t)
	// This fixed vector is already independently checked in networking's own
	// TestClaimIdentitySharedVector; do not import its legacy DB registrations.
	if link.serverID != usLinkFixtureServer {
		t.Fatal("source identity vector differs")
	}
	key[0] ^= 1
	if link.publicKey[0] == key[0] {
		t.Fatal("key aliases original buffer")
	}
	i, scope := usInvitationPreparationFixture()
	code := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	sum := sha256.Sum256([]byte(code))
	hash := hex.EncodeToString(sum[:])
	p, err := prepareUSInvitationCreate(i, scope, "invite", hash, usInvitationCreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	protected, err := prepareUSInvitationCode(p, "key", make([]byte, 32), make([]byte, 12), code)
	if err != nil {
		t.Fatal(err)
	}
	command, err := freezeUSInvitationIssuance(p, protected, link)
	if err != nil {
		t.Fatal(err)
	}
	full := usInvitationIssuanceRequest{invitation: *p, code: protected, link: link}
	want, err := canonicalRequestDigest(usInvitationCreate, 1, i, full)
	if err != nil || want != command.digest {
		t.Fatal("complete original link payload omitted", err)
	}
	for _, change := range []func(*usInvitationLinkBinding){
		func(v *usInvitationLinkBinding) { v.revision++ },
		func(v *usInvitationLinkBinding) { v.keyIncarnation = "another-key-life" },
		func(v *usInvitationLinkBinding) { v.resetGeneration++ },
		func(v *usInvitationLinkBinding) { *v = usInvitationLinkBinding{mode: "portico", revision: 1} },
		func(v *usInvitationLinkBinding) {
			v.publicKey[0] ^= 1
			h := sha256.New()
			_, _ = h.Write([]byte("portico.server.identity.v1\x00"))
			_, _ = h.Write(v.publicKey[:])
			v.serverID = "srv_" + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
		},
	} {
		q := link
		change(&q)
		next, err := freezeUSInvitationIssuance(p, protected, q)
		if err != nil || next.digest == command.digest {
			t.Fatal("link witness absent from digest", err)
		}
	}
	if _, err := prepareUSInvitationLink("local", 1, "wrong", key, "key-life", 3); err == nil {
		t.Fatal("key/server namespace mismatch accepted")
	}
	if _, err := prepareUSInvitationLink("portico", 1, "", nil, "", 0); err != nil {
		t.Fatal("Hosted empty link expectation refused", err)
	}
}
func TestUSInvitationLinkNativeCurrentPublication(t *testing.T) {
	w, _, input := usInvitationSnapshotFixture(t)
	p, _ := usInvitationLinkFixture(t)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO identity_state(singleton,sign_in_kind,revision,authority_generation) VALUES(1,'local',1,1) ON CONFLICT(singleton) DO UPDATE SET sign_in_kind='local',revision=1`, nil},
		{`INSERT INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) VALUES(?,?,?,'fixture')`, []any{p.serverID, p.publicKey[:], p.keyIncarnation}},
		{`INSERT INTO networking_claim_identity(singleton,server_id,public_key,reset_generation) VALUES(1,?,?,?)`, []any{p.serverID, p.publicKey[:], p.resetGeneration}},
	} {
		if _, err := w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	r := nativeInventoryFixtureRegistry(t, w, usInvitationLinkDefinitions(), "us_invitation_link.go")
	turn, _, _ := usPolicyReceiptTurn(t, w, r, input)
	if err := checkUSInvitationFreshLink(t.Context(), turn, p); err != nil {
		t.Fatal("native published key mismatch", err)
	}
	for _, change := range []func(*usInvitationLinkBinding){
		func(v *usInvitationLinkBinding) { v.revision++ }, func(v *usInvitationLinkBinding) { v.keyIncarnation = "other" }, func(v *usInvitationLinkBinding) { v.resetGeneration++ }, func(v *usInvitationLinkBinding) { v.publicKey[0] ^= 1 },
	} {
		bad := p
		change(&bad)
		if err := checkUSInvitationFreshLink(t.Context(), turn, bad); err == nil {
			t.Fatal("stale publication accepted")
		}
	}
	// Source mode equality is native too; no caller bool supplies a Hosted mode.
	if err := checkUSInvitationFreshLink(t.Context(), turn, usInvitationLinkBinding{mode: "portico", revision: 1}); err == nil {
		t.Fatal("caller-selected mode accepted")
	}
	if len(p.String()) == 0 || strings.Contains(p.String(), p.serverID) {
		t.Fatal("link preparation formatting")
	}
}
