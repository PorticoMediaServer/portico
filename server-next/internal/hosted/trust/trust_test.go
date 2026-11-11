package trust

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

// testKey derives a key from a phrase, the same way the fixture generator did inside the
// old service's module. None of these is a real key.
func testKey(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("portico test fixture, never a real key: " + label))
	return ed25519.NewKeyFromSeed(seed[:])
}

type oldService struct {
	RootPublic      string   `json:"rootPublic"`
	SigningKeyID    string   `json:"signingKeyId"`
	Payload         string   `json:"payload"`
	Envelope        Envelope `json:"envelope"`
	ServerPublicKey string   `json:"serverPublicKey"`
	ServerID        string   `json:"serverId"`
}

func readFixture(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatal(err)
	}
}

func decode(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := encoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

var inTwentySix = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

func TestDocumentsSignedByTheOldServiceStillVerify(t *testing.T) {
	var old oldService
	readFixture(t, "old_service.json", &old)
	payload, err := old.Envelope.Verify(decode(t, old.RootPublic), inTwentySix, nil)
	if err != nil || string(payload) != old.Payload {
		t.Fatalf("the old service's document did not verify: %q %v", payload, err)
	}

	var shared struct {
		RootPublic string   `json:"rootPublic"`
		Envelope   Envelope `json:"envelope"`
	}
	readFixture(t, "shared_vector.json", &shared)
	payload, err = shared.Envelope.Verify(decode(t, shared.RootPublic), inTwentySix, nil)
	if err != nil || string(payload) != `{"kind":"portico.trust.test-vector","value":1}` {
		t.Fatalf("the vector shared with released servers did not verify: %q %v", payload, err)
	}
}

func TestSigningProducesTheOldServicesBytes(t *testing.T) {
	var old oldService
	readFixture(t, "old_service.json", &old)
	root, signing := testKey("root"), testKey("signing")
	notBefore := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cert, err := Certify(root, signing.Public().(ed25519.PublicKey), Documents, notBefore, notBefore.Add(365*24*time.Hour), 3, []string{"0123456789abcdef0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Sign(signing, cert, []byte(old.Payload))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(envelope)
	want, _ := json.Marshal(old.Envelope)
	if string(got) != string(want) {
		t.Fatalf("signed bytes differ from the old service's\n got %s\nwant %s", got, want)
	}
	id, err := ServerID(testKey("server").Public().(ed25519.PublicKey))
	if err != nil || id != old.ServerID || !ValidServerID(id) {
		t.Fatalf("server id %q, the old service made %q", id, old.ServerID)
	}
}

func TestADocumentThatWasChangedOrOutlivedItsKeyIsRefused(t *testing.T) {
	var old oldService
	readFixture(t, "old_service.json", &old)
	root := decode(t, old.RootPublic)

	changed := old.Envelope
	changed.Payload = encoding.EncodeToString([]byte(`{"kind":"portico.identity.v1","accountId":"acct_other"}`))
	if _, err := changed.Verify(root, inTwentySix, nil); err == nil {
		t.Fatal("a changed payload verified")
	}
	if _, err := old.Envelope.Verify(root, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), nil); err == nil {
		t.Fatal("a document verified after its signing certificate ended")
	}
	if _, err := old.Envelope.Verify(root, inTwentySix, []string{old.SigningKeyID}); err == nil {
		t.Fatal("a document signed by a withdrawn key verified")
	}
	if _, err := old.Envelope.Verify(testKey("another root").Public().(ed25519.PublicKey), inTwentySix, nil); err == nil {
		t.Fatal("a document verified against another root")
	}
	stretched := *old.Envelope.Certificate
	stretched.NotAfter = stretched.NotAfter.Add(24 * time.Hour)
	changed = old.Envelope
	changed.Certificate = &stretched
	if _, err := changed.Verify(root, inTwentySix, nil); err == nil {
		t.Fatal("a certificate with a changed date verified")
	}
}

// signer is Portico Services in these tests.
type signer struct {
	root ed25519.PrivateKey
	key  ed25519.PrivateKey
	cert *Certificate
}

func newSigner(t *testing.T) signer {
	t.Helper()
	s := signer{root: testKey("root"), key: testKey("signing")}
	var err error
	s.cert, err = Certify(s.root, s.key.Public().(ed25519.PublicKey), Documents, inTwentySix.Add(-time.Hour), inTwentySix.Add(time.Hour), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (s signer) sign(t *testing.T, document any) Envelope {
	t.Helper()
	payload, _ := json.Marshal(document)
	envelope, err := Sign(s.key, s.cert, payload)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func (s signer) verifier() Verifier { return Verifier{Root: s.root.Public().(ed25519.PublicKey)} }

func TestAServerAcceptsOnlyDocumentsMeantForItAndOfTheKindItAskedFor(t *testing.T) {
	s := newSigner(t)
	v := s.verifier()
	note := Note{Kind: NoteKind, ServerID: "srv_mine", Revision: 4, Left: []string{"acct_a"}, IssuedAt: inTwentySix, ExpiresAt: inTwentySix.Add(NoteLifetime)}
	signed := s.sign(t, note)

	got, err := v.OpenNote(signed, "srv_mine", inTwentySix)
	if err != nil || !reflect.DeepEqual(got, note) {
		t.Fatalf("the server's own note: %+v %v", got, err)
	}
	if _, err := v.OpenNote(signed, "srv_other", inTwentySix); err == nil {
		t.Fatal("a note for another server was accepted")
	}
	if _, err := v.OpenNote(signed, "srv_mine", inTwentySix.Add(NoteLifetime)); err == nil {
		t.Fatal("a note was accepted after it ended")
	}
	if _, err := v.OpenApproval(signed, "srv_mine", PurposeSetup, inTwentySix); err == nil {
		t.Fatal("a note was accepted as an approval")
	}

	identity := Identity{Kind: IdentityKind, AccountID: "acct_a", Username: "ada", Email: "ada@example.test", ServerID: "srv_mine", Challenge: "c1",
		IssuedAt: inTwentySix, ExpiresAt: inTwentySix.Add(5 * time.Minute),
		Pictures: []Picture{{Small: "https://pictures.example/abc/160.jpg", Large: "https://pictures.example/abc/512.jpg"}}}
	signed = s.sign(t, identity)
	if got, err := v.OpenIdentity(signed, "srv_mine", "c1", inTwentySix.Add(time.Minute)); err != nil || got.Pictures[0].Small != identity.Pictures[0].Small || got.Email != "ada@example.test" {
		t.Fatalf("a sign-in note: %+v %v", got, err)
	}
	if _, err := v.OpenIdentity(signed, "srv_mine", "c2", inTwentySix); err == nil {
		t.Fatal("a sign-in note for another challenge was accepted")
	}
	if _, err := v.OpenIdentity(signed, "srv_mine", "c1", inTwentySix.Add(6*time.Minute)); err == nil {
		t.Fatal("a sign-in note was accepted after it ended")
	}
	if _, err := v.OpenNote(signed, "srv_mine", inTwentySix); err == nil {
		t.Fatal("a sign-in note was accepted as a note")
	}

	approval := Approval{Kind: ApprovalKind, ApprovalID: "apr_1", ServerID: "srv_mine", ServerName: "Cabin", Purpose: PurposeSetup, AccountID: "acct_a",
		IssuedAt: inTwentySix, ExpiresAt: inTwentySix.Add(10 * time.Minute)}
	signed = s.sign(t, approval)
	// It comes back to the server in the browser's address and is read from there.
	returned, err := ParseReturned(Returned(signed))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := v.OpenApproval(returned, "srv_mine", PurposeSetup, inTwentySix); err != nil || got.ServerName != "Cabin" || got.Purpose != PurposeSetup || got.AccountID != "acct_a" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := v.OpenApproval(returned, "srv_mine", PurposeChangeOwner, inTwentySix); err == nil {
		t.Fatal("a setup approval was accepted for a change of owner")
	}
	if _, err := v.OpenApproval(returned, "srv_other", PurposeSetup, inTwentySix); err == nil {
		t.Fatal("an approval given for one server was accepted by another")
	}
	if _, err := v.OpenApproval(returned, "srv_mine", PurposeSetup, inTwentySix.Add(11*time.Minute)); err == nil {
		t.Fatal("an approval was accepted after it ended")
	}
	if _, err := ParseReturned("not an approval"); err == nil {
		t.Fatal("nonsense was read as an approval")
	}
}

func TestAServersRequestsAndAcknowledgementsAreItsOwn(t *testing.T) {
	key, other := testKey("server"), testKey("another server")
	public := key.Public().(ed25519.PublicKey)
	body := []byte(`{"documentRevision":3}`)
	at, signature := SignRequest(key, "POST", "/v1/servers/srv_x/check-in", inTwentySix, body)

	if err := VerifyRequest(public, "POST", "/v1/servers/srv_x/check-in", at, signature, body, inTwentySix.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"another body":   VerifyRequest(public, "POST", "/v1/servers/srv_x/check-in", at, signature, []byte(`{}`), inTwentySix),
		"another path":   VerifyRequest(public, "POST", "/v1/servers/srv_x/leave", at, signature, body, inTwentySix),
		"another key":    VerifyRequest(other.Public().(ed25519.PublicKey), "POST", "/v1/servers/srv_x/check-in", at, signature, body, inTwentySix),
		"ten minutes on": VerifyRequest(public, "POST", "/v1/servers/srv_x/check-in", at, signature, body, inTwentySix.Add(10*time.Minute)),
	} {
		if err == nil {
			t.Errorf("a request with %s was accepted", name)
		}
	}

	signed := SignAck(key, Ack{ServerID: "srv_x", Revision: 4})
	if ack, err := VerifyAck(public, "srv_x", signed); err != nil || ack.Revision != 4 {
		t.Fatalf("%+v %v", ack, err)
	}
	if _, err := VerifyAck(other.Public().(ed25519.PublicKey), "srv_x", signed); err == nil {
		t.Fatal("an acknowledgement signed by another server was accepted")
	}
	if _, err := VerifyAck(public, "srv_y", signed); err == nil {
		t.Fatal("an acknowledgement was accepted for another server")
	}
}
