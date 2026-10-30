package servicelog

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestReportsScrubShortNamedSecrets(t *testing.T) {
	for _, input := range []string{
		`{"password":"short-password","totpSecret":"SHORTFACTORSEED","api_key":"short-key","title":"A film"}`,
		`password='short password' refresh_token=short-refresh&device_code=12345678`,
		`url=https://user:short-password@provider.example/file?api_key=short-key&access_token=short-access`,
		`{"clientSecret":"escaped\"value","recoveryCode":"short-recovery","pin":"1234"}`,
		`Authorization: Bearer short-bearer`,
	} {
		output := redactClientLog(input)
		for _, secret := range []string{"short-password", "SHORTFACTORSEED", "short-key", "short password", "short-refresh", "12345678", "short-access", "escaped", "short-recovery", "1234", "short-bearer"} {
			if strings.Contains(output, secret) {
				t.Fatalf("secret %q survived: %s", secret, output)
			}
		}
		if !strings.Contains(output, "[REDACTED]") {
			t.Fatalf("no redaction: %s", output)
		}
	}
	input := `{"title":"A film","code":"provider_unavailable","status":503}`
	if output := redactClientLog(input); output != input {
		t.Fatalf("ordinary diagnostics changed: %s", output)
	}
}

func TestForwardedLogOutputDoesNotExposeSecrets(t *testing.T) {
	recorder := New(Options{Capacity: 10})
	var forwarded bytes.Buffer
	input := `[server] password="short password" token=abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ` + "\n"
	if n, err := io.WriteString(recorder.Tee(&forwarded), input); err != nil || n != len(input) {
		t.Fatalf("forwarded write: %d %v", n, err)
	}
	for _, secret := range []string{"short password", "abcdefghijklmnopqrstuvwxyz"} {
		if strings.Contains(forwarded.String(), secret) {
			t.Fatalf("stderr copy exposed %q: %s", secret, &forwarded)
		}
	}
}
