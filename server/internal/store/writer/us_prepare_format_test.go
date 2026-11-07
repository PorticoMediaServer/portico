package writer

import (
	"fmt"
	"strings"
	"testing"
)

func TestUSPreparationFormattingRedacts(t *testing.T) {
	const secret = "private-hash-and-installation-sentinel"
	issuer := usPolicyIssuer{tokenDigest: secret, installation: secret}
	policy := usLibraryPolicyRequest{issuer: issuer, libraries: []string{secret}}
	policyCommand := usLibraryPolicyCommand{request: policy}
	pin := usPINRequest{hash: []byte(secret)}
	prepared := usPINPrepared{request: pin}
	request := usPINCommandRequest{issuer: issuer, prepared: prepared}
	command := usPINCommand{request: request}
	for _, value := range []any{issuer, &issuer, policy, &policy, policyCommand, &policyCommand, pin, &pin, prepared, &prepared, request, &request, command, &command} {
		name := fmt.Sprintf("%T", value)
		s, ok := value.(fmt.Stringer)
		if !ok || !strings.Contains(s.String(), "redacted") {
			t.Fatalf("%s has no String redaction", name)
		}
		g, ok := value.(fmt.GoStringer)
		if !ok || !strings.Contains(g.GoString(), "redacted") {
			t.Fatalf("%s has no GoString redaction", name)
		}
		if _, ok := value.(fmt.Formatter); !ok {
			t.Fatalf("%s has no verb-independent formatter", name)
		}
		for _, verb := range []string{"%s", "%v", "%+v", "%#v", "%q", "%x", "%X", "%d", "%100.3s", "%#100.20v"} {
			for _, container := range []any{value, []any{value}, map[string]any{"request": value}, struct{ Value any }{value}} {
				// Failure diagnostics themselves must not print the leaked candidate.
				if strings.Contains(fmt.Sprintf(verb, container), secret) {
					t.Errorf("%s leaks a private witness with %s", name, verb)
				}
			}
		}
	}
}
