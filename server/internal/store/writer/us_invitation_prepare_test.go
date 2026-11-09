package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func usInvitationPreparationFixture() (CommandIdentity, usInvitationScope) {
	return CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}, usInvitationScope{
		ownerID: "security-owner", operationID: "operation-0001",
		issuer: usPolicyIssuer{
			server: "server", account: "us-owner", profile: "issuer-profile", family: "issuer-family",
			memberIncarnation: strings.Repeat("a", 32), profileLifetime: strings.Repeat("b", 32),
			installation: strings.Repeat("c", 32), tokenDigest: usCredentialFixtureHash("us-invitation-token"),
			epoch: 1, familyGeneration: 1, authorityGeneration: 1,
		},
	}
}

func TestUSInvitationPreparation(t *testing.T) {
	for _, expected := range []Descriptor{
		{ID: usInvitationCreate, Name: "us_invitation_create", Durability: Full},
		{ID: usInvitationEdit, Name: "us_invitation_edit", Durability: Full},
		{ID: usInvitationRevoke, Name: "us_invitation_revoke", Durability: Full},
	} {
		actual, ok := describeUSInvitationCommand(expected.ID)
		if !ok || actual != expected || actual.ID < 500 || actual.ID > 599 {
			t.Fatal(actual, ok)
		}
	}
	if usInvitationCreate != 550 || usInvitationEdit != 551 || usInvitationRevoke != 552 {
		t.Fatal("US-12 purposes must be 550/551/552")
	}
	if _, ok := describeUSInvitationCommand(553); ok {
		t.Fatal("borrowed unrelated purpose")
	}

	i, scope := usInvitationPreparationFixture()
	p, err := prepareUSInvitationCreate(i, scope, "us-created", usInvitationFixtureHash, usInvitationCreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	if p.role != "member" || p.hours != 168 || len(p.libraries) != 0 || p.librariesJSON != "[]" || p.access.mode != "inherit" || !p.access.allowRemote || !p.access.accessTokens || p.boundUsername != "" {
		t.Fatal("create defaults", p)
	}

	notes := "  hello  "
	libraries := []string{"movies", "music"}
	access := &usInvitationAccess{mode: "limit", streams: 2, allowRemote: false, accessTokens: true, rating: " pg-13 "}
	q := usInvitationCreateInput{notes: &notes, role: "admin", libraries: libraries, expiresInHours: 1, boundUsername: "  ReCiPient ", access: access}
	p, err = prepareUSInvitationCreate(i, scope, "us-created", usInvitationFixtureHash, q)
	if err != nil {
		t.Fatal(err)
	}
	// Caller mutation after preparation must not reach the frozen request.
	notes = "changed"
	libraries[0] = "changed"
	access.streams = 99
	access.rating = "X"
	if p.notes.String != "hello" || !p.notes.Valid || p.libraries[0] != "movies" || p.librariesJSON != `["movies","music"]` || p.access.streams != 2 || p.access.rating != "PG-13" || p.boundUsername != "recipient" {
		t.Fatal("caller mutation escaped frozen preparation", p)
	}
	if p.expiresMS != 0 || p.expectedRevision != 0 {
		t.Fatal("create carries edit-only witnesses")
	}

	for _, tc := range []struct {
		name string
		edit func(*usInvitationCreateInput) *usInvitationCreateInput
	}{
		{"bad_role", func(q *usInvitationCreateInput) *usInvitationCreateInput { q.role = "owner"; return q }},
		{"bad_hours_low", func(q *usInvitationCreateInput) *usInvitationCreateInput { q.expiresInHours = -1; return q }},
		{"bad_hours_high", func(q *usInvitationCreateInput) *usInvitationCreateInput { q.expiresInHours = 721; return q }},
		{"duplicate_library", func(q *usInvitationCreateInput) *usInvitationCreateInput { q.libraries = []string{"a", "a"}; return q }},
		{"bad_library", func(q *usInvitationCreateInput) *usInvitationCreateInput {
			q.libraries = []string{"not a library"}
			return q
		}},
		{"too_many_libraries", func(q *usInvitationCreateInput) *usInvitationCreateInput {
			q.libraries = make([]string, usInvitationMaxLibraries+1)
			for n := range q.libraries {
				q.libraries[n] = fmt.Sprint("library-", n)
			}
			return q
		}},
		{"control_note", func(q *usInvitationCreateInput) *usInvitationCreateInput {
			v := "bad\x00note"
			q.notes = &v
			return q
		}},
		{"bad_rating", func(q *usInvitationCreateInput) *usInvitationCreateInput {
			q.access = &usInvitationAccess{mode: "inherit", rating: "XYZ"}
			return q
		}},
		{"bad_streams", func(q *usInvitationCreateInput) *usInvitationCreateInput {
			q.access = &usInvitationAccess{mode: "limit"}
			return q
		}},
		{"space_bound", func(q *usInvitationCreateInput) *usInvitationCreateInput { q.boundUsername = "two names"; return q }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := prepareUSInvitationCreate(i, scope, "us-created", usInvitationFixtureHash, *tc.edit(&usInvitationCreateInput{})); !errors.Is(err, ErrInvalid) {
				t.Fatal("accepted invalid create", err)
			}
		})
	}

	if _, err := prepareUSInvitationCreate(i, scope, "us-created", "ABC", usInvitationCreateInput{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted malformed code hash", err)
	}
	if _, err := prepareUSInvitationCreate(CommandIdentity{}, scope, "us-created", usInvitationFixtureHash, usInvitationCreateInput{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted empty identity", err)
	}

	edit, err := prepareUSInvitationEdit(i, scope, "us-invite", usInvitationEditInput{expectedRevision: 1, role: "member", expiresAt: "1970-01-01T00:00:03Z"}, 1000)
	if err != nil || edit.expiresMS != 3000 || edit.replaceAccess {
		t.Fatal(edit, err)
	}
	for _, tc := range []struct {
		name string
		edit usInvitationEditInput
		now  int64
	}{
		{"revision_zero", usInvitationEditInput{expiresAt: "1970-01-01T00:00:03Z"}, 1000},
		{"past", usInvitationEditInput{expectedRevision: 1, expiresAt: "1970-01-01T00:00:01Z"}, 1000},
		{"too_far", usInvitationEditInput{expectedRevision: 1, expiresAt: "1970-01-31T00:00:02Z"}, 1000},
		{"zero_now", usInvitationEditInput{expectedRevision: 1, expiresAt: "1970-01-01T00:00:03Z"}, 0},
	} {
		if _, err := prepareUSInvitationEdit(i, scope, "us-invite", tc.edit, tc.now); !errors.Is(err, ErrInvalid) {
			t.Fatal(tc.name, err)
		}
	}
	if _, err := prepareUSInvitationRevoke(i, scope, "us-invite", 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted zero revoke revision")
	}

	// Logging preparation must not traverse witnesses or protected wire.
	text := fmt.Sprintf("%v|%+v|%#v|%s", p, p, p, p)
	for _, secret := range []string{p.codeHash, p.boundUsername, p.notes.String, "movies"} {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatal("preparation log leaked", secret)
		}
	}
	signIn, err := prepareUSInvitationSignInKind("local", 1)
	if err != nil || !signIn.valid() {
		t.Fatal(signIn, err)
	}
	if _, err := prepareUSInvitationSignInKind("Hosted", 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted foreign sign-in kind")
	}
	if text := fmt.Sprintf("%v|%+v|%#v", scope, scope, scope); strings.Contains(text, scope.issuer.tokenDigest) {
		t.Fatal("scope log leaked the token digest")
	}
}

func TestUSInvitationProtectedCode(t *testing.T) {
	i, scope := usInvitationPreparationFixture()
	codeBytes := bytes.Repeat([]byte{0x5a}, 32)
	code := base64.RawURLEncoding.EncodeToString(codeBytes)
	if len(code) != 43 {
		t.Fatal("fixture code width", len(code))
	}
	sum := sha256.Sum256([]byte(code))
	hash := hex.EncodeToString(sum[:])
	p, err := prepareUSInvitationCreate(i, scope, "us-created", hash, usInvitationCreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	nonce := bytes.Repeat([]byte{9}, 12)
	protected, err := prepareUSInvitationCode(p, "invitation-key-id", key, nonce, code)
	if err != nil {
		t.Fatal(err)
	}
	if len(protected.body) != 59 || protected.keyID != "invitation-key-id" || !bytes.Equal(protected.nonce, nonce) || !protected.valid(p) {
		t.Fatal("protected code shape", len(protected.body), protected.keyID)
	}
	// Caller mutation of the nonce source cannot rewrite the sealed artifact.
	nonce[0] = 0
	if protected.nonce[0] != 9 {
		t.Fatal("protected nonce aliases caller memory")
	}

	// The exact code-hash binding is what makes the sealed artifact meaningful.
	// A code the preparation did not hash is a conflict, never a fresh issuance.
	other := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x6b}, 32))
	if _, err := prepareUSInvitationCode(p, "invitation-key-id", key, nonce, other); !errors.Is(err, ErrConflict) {
		t.Fatal("accepted mismatched code hash", err)
	}
	for _, tc := range []struct {
		name        string
		prepared    *usInvitationPrepared
		keyID, code string
		key, nonce  []byte
	}{
		{"short_code", p, "invitation-key-id", code[:42], key, nonce},
		{"foreign_key", p, "invitation-key-id", code, bytes.Repeat([]byte{1}, 16), nonce},
		{"foreign_nonce", p, "invitation-key-id", code, key, bytes.Repeat([]byte{1}, 16)},
		{"foreign_key_id", p, "not a key id", code, key, nonce},
		{"missing_preparation", nil, "invitation-key-id", code, key, nonce},
	} {
		if _, err := prepareUSInvitationCode(tc.prepared, tc.keyID, tc.key, tc.nonce, tc.code); !errors.Is(err, ErrInvalid) {
			t.Fatal(tc.name, err)
		}
	}
	revoke, err := prepareUSInvitationRevoke(i, scope, "us-invite", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareUSInvitationCode(revoke, "invitation-key-id", key, nonce, code); !errors.Is(err, ErrInvalid) {
		t.Fatal("sealed a code for a non-create purpose")
	}

	// AAD binds every retained original field; none may be silently retargeted.
	base := usInvitationCodeAAD(p.identity, p.scope.ownerID, p.scope.issuer.server, p.id, p.codeHash, "invitation-key-id")
	for n, changed := range [][32]byte{
		usInvitationCodeAAD(p.identity, "other-owner", p.scope.issuer.server, p.id, p.codeHash, "invitation-key-id"),
		usInvitationCodeAAD(p.identity, p.scope.ownerID, "other-server", p.id, p.codeHash, "invitation-key-id"),
		usInvitationCodeAAD(p.identity, p.scope.ownerID, p.scope.issuer.server, "us-other", p.codeHash, "invitation-key-id"),
		usInvitationCodeAAD(p.identity, p.scope.ownerID, p.scope.issuer.server, p.id, strings.Repeat("b", 64), "invitation-key-id"),
		usInvitationCodeAAD(p.identity, p.scope.ownerID, p.scope.issuer.server, p.id, p.codeHash, "other-key-id"),
	} {
		if changed == base {
			t.Fatal("AAD omitted a retained field", n)
		}
	}

	// Neither the raw Code nor the sealed body may appear in a log line.
	text := fmt.Sprintf("%v|%+v|%#v", protected, protected, protected)
	for _, secret := range []string{code, protected.keyID, base64.StdEncoding.EncodeToString(protected.body), hash} {
		if strings.Contains(text, secret) {
			t.Fatal("protected code log leaked", secret)
		}
	}
}
