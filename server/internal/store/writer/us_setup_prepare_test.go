package writer

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func setupTestDigest(v byte) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat(string(v), 32)))
}
func setupTestIdentity() CommandIdentity {
	return CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}
}
func TestUSSetupPreparation(t *testing.T) {
	i := setupTestIdentity()
	a, b := setupTestDigest('a'), setupTestDigest('b')
	p, e := prepareUSSetupCode(i, "", a)
	if e != nil {
		t.Fatal(e)
	}
	id, args, e := p.effectBindings(1000)
	if e != nil || id != usSetupCodeInsert || len(args) != 3 {
		t.Fatal("insert preparation")
	}
	p, e = prepareUSSetupCode(i, a, b)
	if e != nil {
		t.Fatal(e)
	}
	id, args, e = p.effectBindings(1000)
	if e != nil || id != usSetupCodeRotate || args[2] != a {
		t.Fatal("rotate CAS")
	}
	for _, v := range []string{"", strings.Repeat("x", 43), strings.Repeat("a", 44)} {
		if _, e = prepareUSSetupCode(i, "", v); e == nil {
			t.Fatal("bad digest")
		}
	}
	if _, e = prepareUSSetupCode(i, a, a); e == nil {
		t.Fatal("unchanged code")
	}
	if _, e = prepareUSSetupCode(CommandIdentity{}, "", a); e == nil {
		t.Fatal("missing identity")
	}
	p, e = prepareUSSetupAdmission(i, a, b, a, false, 0)
	if e != nil {
		t.Fatal(e)
	}
	id, args, e = p.effectBindings(1000)
	if e != nil || id != usSetupAdmissionIssue || args[3] != int64(1801000) {
		t.Fatal("30 minute lifetime")
	}
	p, e = prepareUSSetupAdmission(i, a, b, a, true, 2000)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = p.effectBindings(2000); e == nil {
		t.Fatal("expired consume")
	}
	if _, _, e = p.effectBindings(usMaxRevision); e == nil {
		t.Fatal("overflow")
	}
	if _, e = prepareUSSetupAdmission(i, a, b, a, false, 2000); e == nil {
		t.Fatal("issue accepts expiry override")
	}
}
func TestUSSetupPreparationRedaction(t *testing.T) {
	secret := setupTestDigest('z')
	p := usSetupPrepared{oldCode: secret, code: secret, token: secret, peer: secret, origin: secret}
	for _, v := range []any{p, &p} {
		if !strings.Contains(v.(fmt.Stringer).String(), "redacted") || !strings.Contains(v.(fmt.GoStringer).GoString(), "redacted") {
			t.Fatal("methods")
		}
		for _, verb := range []string{"%s", "%v", "%+v", "%#v", "%q", "%x", "%X", "%d", "%100.3s", "%#100.20v"} {
			for _, c := range []any{v, []any{v}, map[string]any{"p": v}, struct{ P any }{v}} {
				if strings.Contains(fmt.Sprintf(verb, c), secret) {
					t.Fatal("private witness leaked")
				}
			}
		}
	}
}
