package identity

import (
	"strings"
	"testing"
)

func TestSetupCodeShapeAndReading(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		code, err := newSetupCode()
		if err != nil || len(code) != 9 || code[4] != '-' {
			t.Fatal("shape", code, err)
		}
		plain, ok := normalizedSetupCode(code)
		if !ok || len(plain) != 8 || strings.ContainsAny(plain, "ILOU") {
			t.Fatal("Crockford alphabet", code)
		}
		seen[code] = true
	}
	if len(seen) < 60 {
		t.Fatal("codes repeat", len(seen))
	}
	for typed, want := range map[string]string{"7k4m-q2xp": "7K4MQ2XP", " 7K4M Q2XP ": "7K4MQ2XP", "IL0O-1234": "11001234"} {
		if got, ok := normalizedSetupCode(typed); !ok || got != want {
			t.Errorf("%q read as %q", typed, got)
		}
	}
	for _, bad := range []string{"", "7K4M-Q2X", "7K4M-Q2XPZ", "7K4M-Q2X!", "7K4M-Q2XU"} {
		if _, ok := normalizedSetupCode(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if line := SetupCodeLogLine("7K4M-Q2XP"); !strings.HasPrefix(line, SetupCodeLogPrefix+" 7K4M-Q2XP") {
		t.Fatal("log line", line)
	}
}
