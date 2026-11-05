package searchpolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func TestCompiledAndCompatibility(t *testing.T) {
	actual := Engine{SQLiteVersion, SQLiteSourceID}
	p := Compiled()
	if p.RecipeVersion != RecipeV1 || p.EngineVersion != EngineVersion || p.UnicodeFingerprint != fingerprint(unicode.Version, norm.Version) {
		t.Fatal("compiled profile does not describe the actual normalization tables")
	}
	for _, tc := range []struct {
		name   string
		stored Profile
		engine Engine
		want   Compatibility
	}{
		{"current", p, actual, Compatibility{Current, ProfileMatches, true}},
		{"missing", Profile{}, actual, Compatibility{Unavailable, StoredMissing, true}},
		{"partial", Profile{RecipeVersion: RecipeV1}, actual, Compatibility{Unavailable, StoredMalformed, true}},
		{"research-label", Profile{RecipeV1, EngineVersion, "008bb74"}, actual, Compatibility{Unavailable, StoredMalformed, true}},
		{"wrong-recipe", Profile{"other-recipe", p.EngineVersion, p.UnicodeFingerprint}, actual, Compatibility{Stale, ProfileMismatch, true}},
		{"wrong-stored-engine", Profile{p.RecipeVersion, "another-engine", p.UnicodeFingerprint}, actual, Compatibility{Stale, ProfileMismatch, true}},
		{"uppercase-digest", Profile{p.RecipeVersion, p.EngineVersion, ProfileAlgorithm + ":" + strings.Repeat("A", 64)}, actual, Compatibility{Unavailable, StoredMalformed, true}},
		{"short-digest", Profile{p.RecipeVersion, p.EngineVersion, ProfileAlgorithm + ":00"}, actual, Compatibility{Unavailable, StoredMalformed, true}},
		{"wrong-native-version", p, Engine{"3.53.3", SQLiteSourceID}, Compatibility{Unavailable, NativeEngineMismatch, false}},
		{"wrong-native-source", p, Engine{SQLiteVersion, "different source"}, Compatibility{Unavailable, NativeEngineMismatch, false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.stored
			if got := Compare(tc.stored, tc.engine); got != tc.want {
				t.Fatalf("Compare = %+v, want %+v", got, tc.want)
			}
			if tc.stored != before {
				t.Fatal("comparison changed durable metadata")
			}
		})
	}
}

func TestUnicodeProfileRestartRouting(t *testing.T) {
	// These are independently encoded in the fixed byte vectors. This is a
	// metadata routing control, not a durable publisher/rebuild qualification.
	a := Profile{RecipeV1, EngineVersion, fingerprint("15.0.0", "15.0.0")}
	b := Profile{RecipeV1, EngineVersion, fingerprint("17.0.0", "17.0.0")}
	if a == b {
		t.Fatal("different compiled Unicode tables aliased")
	}
	actual := Engine{SQLiteVersion, SQLiteSourceID}
	previous := a
	if Compiled() == a {
		previous = b
	}
	for range 2 { // Reopening during a rebuild must derive the same stale state.
		if got := Compare(previous, actual); got.State != Stale || !got.EngineAttested {
			t.Fatalf("previous compatible-native bank = %+v", got)
		}
	}
	if got := Compare(Compiled(), actual); got.State != Current {
		t.Fatalf("a completed compatible bank = %+v", got)
	}
}

func TestProfileByteVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/profile-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		GoUnicode   string   `json:"go_unicode"`
		TextUnicode string   `json:"text_unicode"`
		Fields      []string `json:"fields"`
		EncodedHex  string   `json:"encoded_hex"`
		SHA256      string   `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases) != 3 {
		t.Fatalf("fixed vectors: %d, %v", len(cases), err)
	}
	for _, tc := range cases {
		// Independent fixture encoder: no production writeString call.
		var encoded bytes.Buffer
		encoded.WriteString(ProfileAlgorithm + "\x00")
		for _, field := range tc.Fields {
			if err := binary.Write(&encoded, binary.BigEndian, uint64(len(field))); err != nil {
				t.Fatal(err)
			}
			encoded.WriteString(field)
		}
		hash := sha256.Sum256(encoded.Bytes())
		if hex.EncodeToString(encoded.Bytes()) != tc.EncodedHex || hex.EncodeToString(hash[:]) != tc.SHA256 {
			t.Fatal("independent fixed byte trace differs")
		}
		if got := fingerprint(tc.GoUnicode, tc.TextUnicode); got != ProfileAlgorithm+":"+tc.SHA256 {
			t.Fatalf("profile %s/%s = %s, want %s", tc.GoUnicode, tc.TextUnicode, got, tc.SHA256)
		}
	}
}

func TestPreparationSourcePin(t *testing.T) {
	// The closure contains the production functions that emit title, People
	// and fragment strings. Token spellings and literals are bound, while
	// comments, positions and gofmt whitespace do not change a bank identity.
	paths := []string{
		"internal/facettext/prefix.go",
		"internal/facettext/search.go",
		"internal/sorttext/sort.go",
		"internal/store/search/fragments.go",
		"internal/store/search/recipe.go",
	}
	h := sha256.New()
	h.Write([]byte("portico-search-preparation-tokens-v1\x00"))
	for _, path := range paths {
		source, err := os.ReadFile(filepath.Join("../../../..", path))
		if err != nil {
			t.Fatal(err)
		}
		writeString(h, path)
		tokens := tokenBytes(t, source)
		var count [8]byte
		binary.BigEndian.PutUint64(count[:], uint64(len(tokens)/2))
		h.Write(count[:])
		for _, value := range tokens {
			writeString(h, value)
		}
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != PreparationSHA256 {
		t.Fatalf("preparation closure = %s, want %s; review the profile before changing this pin", got, PreparationSHA256)
	}
	// Verify the pinned dependency identity without relying on test-binary
	// build info, which can omit dependencies under some Go invocations.
	mod, err := os.ReadFile("../../../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile("../../../../go.sum")
	if err != nil {
		t.Fatal(err)
	}
	for _, dependency := range []string{TextModule, SQLiteModule} {
		module, version, _ := strings.Cut(dependency, "@")
		if !strings.Contains(string(mod), module+" "+version+"\n") {
			t.Fatalf("go.mod no longer pins %s", dependency)
		}
	}
	if !strings.Contains(string(sum), "golang.org/x/text v0.42.0 "+TextModuleSum+"\n") {
		t.Fatal("x/text module checksum changed")
	}
}

func tokenBytes(t *testing.T, source []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	file := fset.AddFile("source.go", -1, len(source))
	var scan scanner.Scanner
	scan.Init(file, source, func(_ token.Position, message string) { t.Error(message) }, 0)
	var out []string
	for {
		_, kind, value := scan.Scan()
		if kind == token.EOF {
			return out
		}
		if kind == token.SEMICOLON {
			value = ";"
		}
		out = append(out, kind.String(), value)
	}
}

func TestPreparationTokenCodec(t *testing.T) {
	a := tokenBytes(t, []byte("package p\n// ignored\nconst v = \"x\"\n"))
	b := tokenBytes(t, []byte("package p; const v=\"x\";"))
	c := tokenBytes(t, []byte("package p; const v=\"y\";"))
	if strings.Join(a, "\x00") != strings.Join(b, "\x00") || strings.Join(a, "\x00") == strings.Join(c, "\x00") {
		t.Fatal("token codec ignores a literal or depends on source formatting")
	}
}
