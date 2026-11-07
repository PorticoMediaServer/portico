package writer

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func usInvitationPreparationFixture() (CommandIdentity, usInvitationScope) {
	return CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}, usInvitationScope{
		ownerID: "us-owner", operationID: "invite-operation", issuer: usPolicyIssuer{
			server: "server", account: "us-owner", profile: "profile", family: "family",
			memberIncarnation: strings.Repeat("a", 32), profileLifetime: strings.Repeat("b", 32), installation: strings.Repeat("c", 32),
			tokenDigest: usCredentialFixtureHash("invitation-issuer"), epoch: 1, familyGeneration: 1, authorityGeneration: 1,
		},
	}
}

func TestUSInvitationPreparation(t *testing.T) {
	i, scope := usInvitationPreparationFixture()
	for _, kind := range []CommandID{usInvitationCreate, usInvitationEdit, usInvitationRevoke} {
		d, ok := describeUSInvitationCommand(kind)
		if !ok || d.ID != kind || d.Durability != Full || d.Name == "" {
			t.Fatal("FULL local descriptor missing", kind)
		}
	}
	if _, ok := describeUSInvitationCommand(SecurityAccept); ok {
		t.Fatal("borrowed purpose")
	}
	note := "  " + strings.Repeat("界", 500) + "  "
	grant := usInvitationAccess{mode: "limit", streams: 1000, rating: " pg-13 "}
	libraries := []string{strings.Repeat("L", 128)}
	p, err := prepareUSInvitationCreate(i, scope, "invite", usInvitationFixtureHash, usInvitationCreateInput{notes: &note, libraries: libraries, boundUsername: " Recipient ", access: &grant})
	if err != nil {
		t.Fatal(err)
	}
	if p.role != "member" || p.hours != 168 || p.boundUsername != "recipient" || p.access.rating != "PG-13" || len([]rune(p.notes.String)) != 500 || !p.notes.Valid {
		t.Fatal("normalization/defaults", p)
	}
	libraries[0], note, grant.streams = "mutated", "mutated", 1
	if p.libraries[0] != strings.Repeat("L", 128) || p.access.streams != 1000 || p.notes.String != strings.Repeat("界", 500) || !strings.Contains(p.librariesJSON, strings.Repeat("L", 128)) {
		t.Fatal("input aliases frozen request")
	}
	defaults, err := prepareUSInvitationCreate(i, scope, "default", usInvitationFixtureHash, usInvitationCreateInput{})
	if err != nil || defaults.librariesJSON != "[]" || defaults.notes.Valid || defaults.access.mode != "inherit" || !defaults.access.allowRemote || !defaults.access.accessTokens {
		t.Fatal("default grant", err)
	}
	// Constructors remain usable before RD2 registers these purposes; no digest
	// workaround or alternate security purpose is used to get there.
	_, describeErr := Describe(usInvitationCreate)
	_, digestErr := canonicalRequestDigest(usInvitationCreate, 1, i, *p)
	if (describeErr == nil) != (digestErr == nil) {
		t.Fatal("canonical digest must respect shared purpose registry", describeErr, digestErr)
	}
	if _, err := prepareUSInvitationRevoke(i, scope, "invite", usMaxRevision-1); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareUSInvitationRevoke(i, scope, "invite", usMaxRevision); err == nil {
		t.Fatal("revision overflow")
	}
}

func TestUSInvitationPreparationBounds(t *testing.T) {
	i, scope := usInvitationPreparationFixture()
	ids := make([]string, 256)
	for n := range ids {
		ids[n] = fmt.Sprintf("library-%d", n)
	}
	if _, err := prepareUSInvitationCreate(i, scope, "invite", usInvitationFixtureHash, usInvitationCreateInput{libraries: ids, role: "admin", expiresInHours: 720}); err != nil {
		t.Fatal("legal upper bounds", err)
	}
	notes := []string{strings.Repeat("界", 501), "private\x00note", "private\u200bnote", string([]byte{0xff})}
	for _, note := range notes {
		if _, err := prepareUSInvitationCreate(i, scope, "invite", usInvitationFixtureHash, usInvitationCreateInput{notes: &note}); err == nil {
			t.Fatal("invalid notes admitted")
		}
	}
	for _, q := range []usInvitationCreateInput{
		{role: "owner"}, {role: "Member"}, {expiresInHours: -1}, {expiresInHours: 721},
		{libraries: append(ids, "extra")}, {libraries: []string{"same", "same"}}, {libraries: []string{strings.Repeat("x", 129)}}, {libraries: []string{"has space"}},
		{boundUsername: strings.Repeat("u", 65)}, {boundUsername: "two words"},
		{access: &usInvitationAccess{mode: "limit", streams: 1001}}, {access: &usInvitationAccess{mode: "unlimited", streams: 1}},
		{access: &usInvitationAccess{mode: "inherit", rating: "NOT-A-RATING"}},
	} {
		if _, err := prepareUSInvitationCreate(i, scope, "invite", usInvitationFixtureHash, q); err == nil {
			t.Fatal("invalid input admitted", q)
		}
	}
	for _, hash := range []string{"plaintext-capability", strings.Repeat("A", 64), strings.Repeat("a", 63)} {
		if _, err := prepareUSInvitationCreate(i, scope, "invite", hash, usInvitationCreateInput{}); err == nil {
			t.Fatal("non-digest admitted")
		}
	}
	for _, op := range []string{"short", strings.Repeat("a", 129), "operation\n"} {
		bad := scope
		bad.operationID = op
		if _, err := prepareUSInvitationRevoke(i, bad, "invite", 1); err == nil {
			t.Fatal("invalid operation admitted")
		}
	}
	bad := scope
	bad.issuer.tokenDigest = ""
	if _, err := prepareUSInvitationRevoke(i, bad, "invite", 1); err == nil {
		t.Fatal("missing issuer witness")
	}
}

func TestUSInvitationEditPreparation(t *testing.T) {
	i, scope := usInvitationPreparationFixture()
	now := int64(1700000000000)
	expires := time.UnixMilli(now).Add(720 * time.Hour).UTC().Format(time.RFC3339)
	q := usInvitationEditInput{expectedRevision: 2, role: "admin", expiresAt: expires}
	p, err := prepareUSInvitationEdit(i, scope, "invite", q, now)
	if err != nil || p.replaceAccess || p.librariesJSON != "[]" || p.expiresMS != now+usInvitationMaxSpanMS {
		t.Fatal("edit normalization", err)
	}
	q.access = &usInvitationAccess{mode: "unlimited"}
	p, err = prepareUSInvitationEdit(i, scope, "invite", q, now)
	if err != nil || !p.replaceAccess {
		t.Fatal("access replacement", err)
	}
	for _, expiry := range []string{"", "bad", time.UnixMilli(now).UTC().Format(time.RFC3339), time.UnixMilli(now + usInvitationMaxSpanMS + 1000).UTC().Format(time.RFC3339)} {
		q.expiresAt = expiry
		if _, err := prepareUSInvitationEdit(i, scope, "invite", q, now); err == nil {
			t.Fatal("invalid expiry")
		}
	}
	q.expiresAt, q.role = expires, ""
	if _, err := prepareUSInvitationEdit(i, scope, "invite", q, now); err == nil {
		t.Fatal("edit defaulted omitted role")
	}
	if _, _, err := (*usInvitationPrepared)(nil).effectBindings(now); err == nil {
		t.Fatal("nil preparation admitted")
	}
}
