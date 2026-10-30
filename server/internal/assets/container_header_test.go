package assets

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestContainerHeaderRequiresWebMDocType(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, want string
	}{
		{"webm", "1a45dfa3874282847765626d", "webm"},
		{"matroska", "1a45dfa38b4282886d6174726f736b61", "matroska"},
		{"other field before type", "1a45dfa38b428681014282847765626d", "webm"},
		{"unknown header size", "1a45dfa3ff4282847765626d", "matroska"},
		{"truncated header", "1a45dfa38b4282847765626d", "matroska"},
		{"truncated type", "1a45dfa3874282857765626d", "matroska"},
		{"unknown field size", "1a45dfa3874282ff7765626d", "matroska"},
		{"type outside header", "1a45dfa3804282847765626d", "matroska"},
		{"duplicate type", "1a45dfa38e4282847765626d4282847765626d", "matroska"},
		{"empty duplicate type", "1a45dfa38a4282804282847765626d", "matroska"},
		{"zero size marker", "1a45dfa3004282847765626d", "matroska"},
		{"text is not EBML", "7765626d", "matroska"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix, err := hex.DecodeString(tc.prefix)
			if err != nil {
				t.Fatal(err)
			}
			if got := containerFromHeader("matroska", prefix); got != tc.want {
				t.Fatalf("container = %q, want %q", got, tc.want)
			}
			if got := containerFromHeader("mov", prefix); got != "mov" {
				t.Fatal("EBML header replaced an unrelated measured format", got)
			}
		})
	}
}

func TestContainerHeaderReadKeepsScanAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "renamed.mkv")
	prefix, err := hex.DecodeString("1a45dfa3874282847765626d")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, prefix, 0600); err != nil {
		t.Fatal(err)
	}
	facts := Facts{Container: "matroska"}
	if err = (Probe{}).completeContainer(context.Background(), path, &facts); err != nil || facts.Container != "webm" {
		t.Fatal("content identity followed the extension", facts, err)
	}
	denied := errors.New("scan authority ended")
	p := Probe{ReadGuard: func(context.Context, string) error { return denied }}
	facts.Container = "matroska"
	if err = p.completeContainer(context.Background(), path, &facts); !errors.Is(err, denied) || facts.Container != "matroska" {
		t.Fatal("container read bypassed admission", facts, err)
	}
}
