package writer

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// The Windows grammar is checked through the pure helper so a Unix runner still
// proves it. Production passes the actual GOOS.
func TestIAConfigureLibraryLocalRootGrammar(t *testing.T) {
	for _, tc := range []struct {
		name    string
		root    string
		windows bool
		want    bool
	}{
		{"unix_absolute", "/media/films", false, true},
		{"unix_root", "/", false, true},
		{"unix_relative", "media/films", false, false},
		{"unix_dot_relative", "./media", false, false},
		{"unix_unclean_parent", "/a/../b", false, false},
		{"unix_trailing_separator", "/media/", false, false},
		{"unix_double_slash", "//server/share", false, false},
		{"unix_windows_absolute", `C:\Media`, false, false},
		{"windows_drive_backslash", `C:\Media\Films`, true, true},
		{"windows_drive_forward", "C:/Media", true, true},
		{"windows_drive_root", `C:\`, true, true},
		{"windows_drive_relative", `C:Media`, true, false},
		{"windows_unclean_parent", `C:\Media\..\Films`, true, false},
		{"windows_unclean_dot", `C:\Media\.\Films`, true, false},
		{"windows_trailing_separator", `C:\Media\`, true, false},
		{"windows_rooted_no_drive", `\Media`, true, false},
		{"windows_unc", `\\server\share\media`, true, true},
		{"windows_unc_unclean", `\\server\share\media\..\other`, true, false},
		{"windows_device", `\\?\C:\Media`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := iaConfigureLibraryLocalRoot(tc.root, tc.windows); got != tc.want {
				t.Fatalf("%q windows=%v = %v, want %v", tc.root, tc.windows, got, tc.want)
			}
		})
	}
}

func iaNativeAbsoluteRoot(t *testing.T, tail string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `C:\` + strings.ReplaceAll(tail, "/", `\`)
	}
	return "/" + tail
}

// Every unrepresentable local root is refused by complete validation BEFORE any
// write: no namespace, live row, policy, intent, event or receipt survives.
func TestIAConfigureLibraryRefusesUnrepresentableRoots(t *testing.T) {
	for name, mutate := range map[string]func(*iaConfigureLibrarySource){
		"relative":           func(s *iaConfigureLibrarySource) { s.configuredRoot, s.resolvedRoot = "relative/../x", "relative/x" },
		"unclean_resolved":   func(s *iaConfigureLibrarySource) { s.resolvedRoot = "/a/../b" },
		"unclean_configured": func(s *iaConfigureLibrarySource) { s.configuredRoot = "/a/../b" },
		"trailing_separator": func(s *iaConfigureLibrarySource) { s.configuredRoot, s.resolvedRoot = "/media/", "/media/" },
		"empty":              func(s *iaConfigureLibrarySource) { s.configuredRoot, s.resolvedRoot = "", "" },
		"nul":                func(s *iaConfigureLibrarySource) { s.resolvedRoot = "/media\x00" },
		"control":            func(s *iaConfigureLibrarySource) { s.configuredRoot = "/media\n" },
		"oversized": func(s *iaConfigureLibrarySource) {
			s.resolvedRoot = "/" + strings.Repeat("r", iaConfigureLibraryMaxRoot)
		},
		"foreign_windows_drive": func(s *iaConfigureLibrarySource) { s.configuredRoot, s.resolvedRoot = `C:\Media`, `C:\Media` },
		"windows_device_path":   func(s *iaConfigureLibrarySource) { s.configuredRoot, s.resolvedRoot = `\\?\C:\Media`, `\\?\C:\Media` },
	} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && name == "foreign_windows_drive" {
				t.Skip("a drive-root path is native on Windows")
			}
			w, r, p := iaConfigureLibraryFixture(t)
			source := iaConfigureLibrarySourceFixture(0, iaNativeAbsoluteRoot(t, "media"))
			mutate(&source)
			q := iaConfigureLibraryRequest{
				reference: iaConfigureLibraryReference(50, 51, 52), libraryID: 950, libraryKey: "lib-root-refusal", name: "Roots", kind: "movie",
				sources: []iaConfigureLibrarySource{source},
			}
			turn, budget := iaConfigureLibraryTurn(t, w, r, q)
			out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q)
			if !errors.Is(err, ErrInvalid) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.sources) != 0 {
				t.Fatal("root refusal", out, err)
			}
			turn.transaction.rollback(nil)
			for _, table := range []string{"libraries", "catalog_libraries", "library_sources", "library_scan_policies", "inventory_strict_intents", "api_events",
				"catalog_configuration_operations", "catalog_configuration_receipts", "catalog_configuration_sources"} {
				if got := iaConfigureLibraryCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
					t.Fatal("refused root wrote", table, got)
				}
			}
		})
	}
}

// A native absolute root is accepted. Remote-kind roots stay opaque text, so a
// Windows location is representable as a remote source on any host.
func TestIAConfigureLibraryAcceptsRepresentableRoots(t *testing.T) {
	t.Run("native_local", func(t *testing.T) {
		w, r, p := iaConfigureLibraryFixture(t)
		root := iaNativeAbsoluteRoot(t, "media/films")
		q := iaConfigureLibraryRequest{
			reference: iaConfigureLibraryReference(53, 54, 55), libraryID: 951, libraryKey: "lib-root-accept", name: "Roots", kind: "movie",
			sources: []iaConfigureLibrarySource{iaConfigureLibrarySourceFixture(0, root)},
		}
		turn, budget := iaConfigureLibraryTurn(t, w, r, q)
		out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q)
		if err != nil || out.decision != receiptFresh || len(out.result.facts.sources) != 1 || out.result.facts.sources[0].resolvedRoot != root {
			t.Fatal("native root", out, err)
		}
		turn.transaction.rollback(nil)
	})
	t.Run("remote_opaque", func(t *testing.T) {
		w, r, p := iaConfigureLibraryFixture(t)
		source := iaConfigureLibrarySourceFixture(0, `\\server\share\media`)
		source.kind, source.classification = "remote", "network"
		q := iaConfigureLibraryRequest{
			reference: iaConfigureLibraryReference(56, 57, 58), libraryID: 952, libraryKey: "lib-root-remote", name: "Roots", kind: "movie",
			sources: []iaConfigureLibrarySource{source},
		}
		turn, budget := iaConfigureLibraryTurn(t, w, r, q)
		out, err := configureIALibrary(t.Context(), turn, p, t.Context(), budget, q)
		if err != nil || out.decision != receiptFresh || len(out.result.facts.sources) != 1 || out.result.facts.sources[0].kind != "remote" {
			t.Fatal("remote root", out, err)
		}
		turn.transaction.rollback(nil)
	})
}

// The command rejects 65 roots itself; it does not rely on the result codec
// preflight, which would also see an invalid request only after validation.
func TestIAConfigureLibraryRootBoundIsLocal(t *testing.T) {
	q := iaConfigureLibraryRequest{
		reference: iaConfigureLibraryReference(59, 60, 61), libraryID: 953, libraryKey: "lib-root-bound", name: "Roots", kind: "movie",
	}
	for i := 0; i < iaConfigureLibraryMaxSources; i++ {
		q.sources = append(q.sources, iaConfigureLibrarySourceFixture(i, fmt.Sprintf("/bounds/%02d", i)))
	}
	if err := validateIAConfigureLibraryRequest(q); err != nil {
		t.Fatal("64 roots refused", err)
	}
	q.sources = append(q.sources, iaConfigureLibrarySourceFixture(iaConfigureLibraryMaxSources, "/bounds/64"))
	if err := validateIAConfigureLibraryRequest(q); !errors.Is(err, ErrInvalid) {
		t.Fatal("65 roots admitted by the command", err)
	}
}
