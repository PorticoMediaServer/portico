package writer

import (
	"fmt"
	"strings"
	"testing"
)

func TestUSCredentialComponentFormatting(t *testing.T) {
	const secret = "credential-hash-installation-private-sentinel"
	target := usCredentialTarget{account: secret, username: secret, profileName: secret}
	password := usPasswordReplaceRequest{target: target, previousHash: []byte(secret), nextHash: []byte(secret)}
	issue := usCredentialIssueRequest{target: target, secretHash: secret, scopes: []string{secret}}
	revoke := usCredentialRevokeRequest{target: target, secretHash: secret}
	for _, value := range []any{target, &target, password, &password, usPasswordReplacePrepared{request: password}, issue, &issue, usCredentialIssuePrepared{target: target}, usCredentialProtectedManifest{target: target}, revoke, &revoke, usCredentialRevokePrepared{request: revoke}} {
		name := fmt.Sprintf("%T", value)
		s, ok := value.(fmt.Stringer)
		if !ok || !strings.Contains(s.String(), "redacted") {
			t.Fatalf("%s String redaction", name)
		}
		g, ok := value.(fmt.GoStringer)
		if !ok || !strings.Contains(g.GoString(), "redacted") {
			t.Fatalf("%s GoString redaction", name)
		}
		if _, ok := value.(fmt.Formatter); !ok {
			t.Fatalf("%s Format redaction", name)
		}
		for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q", "%x", "%X", "%d", "%100.3s", "%#100.20v"} {
			for _, container := range []any{value, []any{value}, map[string]any{"request": value}, struct{ Value any }{value}} {
				if strings.Contains(fmt.Sprintf(format, container), secret) {
					t.Errorf("%s private witness leaks with %s", name, format)
				}
			}
		}
	}
}
