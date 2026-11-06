package scanpath

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestFilenameKnownBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
		want string
	}{
		{"empty", nil, ""},
		{"nonnil_empty", []byte{}, ""},
		{"ascii", []byte("Folder/a %_'[.]~.mkv"), "Folder/a %_'[.]~.mkv"},
		{"invalid_utf8", []byte{'a', '/', 0xff, '.', 'm', 'k', 'v'}, `a/\xff.mkv`},
		{"literal_escape", []byte(`a/\xff.mkv`), `a/\x5cxff.mkv`},
		{"controls", []byte{0, 0x1f, ' ', '\\', '~', 0x7f, 0x80, 0xff}, `\x00\x1f \x5c~\x7f\x80\xff`},
		{"utf8_bytes", []byte("é/日本"), `\xc3\xa9/\xe6\x97\xa5\xe6\x9c\xac`},
		{"nfc", []byte("café"), `caf\xc3\xa9`},
		{"nfd", []byte("cafe\u0301"), `cafe\xcc\x81`},
		{"no_containment_authority", []byte("../name"), "../name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := int64(len(tc.raw) + len(tc.want))
			before := bytes.Clone(tc.raw)
			got, err := EncodeFilename(tc.raw, budget)
			if err != nil || got != tc.want || !bytes.Equal(before, tc.raw) {
				t.Fatalf("encode: %q, %v; want %q with unchanged input", got, err, tc.want)
			}
			raw, err := DecodeFilename(tc.want, budget)
			if err != nil || !bytes.Equal(raw, tc.raw) {
				t.Fatalf("decode: %x, %v; want %x", raw, err, tc.raw)
			}
			if len(raw) != 0 {
				raw[0] ^= 0xff
				if !bytes.Equal(before, tc.raw) || got != tc.want {
					t.Fatal("returned bytes alias input or encoded text")
				}
			}
		})
	}
}

func TestFilenameAllByteValues(t *testing.T) {
	var all []byte
	for value := 0; value < 256; value++ {
		raw := []byte{byte(value)}
		want := fmt.Sprintf(`\x%02x`, value)
		if value >= 0x20 && value <= 0x7e && value != '\\' {
			want = string(raw)
		}
		encoded, err := EncodeFilename(raw, 5)
		if err != nil || encoded != want {
			t.Fatalf("byte %02x: %q, %v; want %q", value, encoded, err, want)
		}
		decoded, err := DecodeFilename(want, 5)
		if err != nil || !bytes.Equal(decoded, raw) {
			t.Fatalf("byte %02x inverse: %x, %v", value, decoded, err)
		}
		all = append(all, raw...)
	}
	encoded, err := EncodeFilename(all, 5*256)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeFilename(encoded, int64(len(encoded)+len(all)))
	if err != nil || !bytes.Equal(decoded, all) {
		t.Fatalf("whole byte alphabet: %x, %v", decoded, err)
	}
}

func TestFilenameCanonicalInverse(t *testing.T) {
	invalid := []string{`\`, `\x`, `\x0`, `\X00`, `\x0G`, `\xg0`, `\xff\`,
		`\xFF`, `\xAf`, `\x20`, `\x2f`, `\x41`, `\x7e`, "\x00", "\n", "\x7f", "é", "\xff"}
	for _, display := range invalid {
		raw, err := DecodeFilename(display, math.MaxInt64)
		if !errors.Is(err, ErrEncoding) || raw != nil {
			t.Fatalf("accepted noncanonical %q: %x, %v", display, raw, err)
		}
	}
	// Every possible hex spelling is either the canonical literal or escape,
	// never a second alias for the same native byte.
	for value := 0; value < 256; value++ {
		display := fmt.Sprintf(`\x%02x`, value)
		raw, err := DecodeFilename(display, 5)
		printable := value >= 0x20 && value <= 0x7e && value != '\\'
		if printable && (!errors.Is(err, ErrEncoding) || raw != nil) {
			t.Fatalf("printable escape accepted: %q", display)
		}
		if !printable && (err != nil || !bytes.Equal(raw, []byte{byte(value)})) {
			t.Fatalf("canonical escape refused: %q: %v", display, err)
		}
	}
}

func TestFilenameCompleteBudgetBeforeAllocation(t *testing.T) {
	raw := []byte{'a', '/', 0xff, '\\', 0}
	want := `a/\xff\x5c\x00`
	exact := int64(len(raw) + len(want))
	for _, budget := range []int64{-1, 0, int64(len(raw)), int64(len(want)), exact - 1} {
		if got, err := EncodeFilename(raw, budget); got != "" || !errors.Is(err, ErrCapacity) {
			t.Fatalf("encode budget%d: %q, %v", budget, got, err)
		}
		if got, err := DecodeFilename(want, budget); got != nil || !errors.Is(err, ErrCapacity) {
			t.Fatalf("decode budget%d: %x, %v", budget, got, err)
		}
	}
	for _, budget := range []int64{exact, exact + 1, math.MaxInt64} {
		if got, err := EncodeFilename(raw, budget); got != want || err != nil {
			t.Fatalf("encode boundary%d: %q, %v", budget, got, err)
		}
		if got, err := DecodeFilename(want, budget); !bytes.Equal(got, raw) || err != nil {
			t.Fatalf("decode boundary%d: %x, %v", budget, got, err)
		}
	}
	// A late escape must not leave an allocated partial buffer after a long
	// valid prefix. The budget includes both representations, not just output.
	largeRaw := append(bytes.Repeat([]byte{'a'}, 64<<10), 0xff)
	largeDisplay := strings.Repeat("a", 64<<10) + `\xff`
	largeBudget := int64(len(largeRaw) + len(largeDisplay) - 1)
	if allocations := testing.AllocsPerRun(20, func() {
		if got, err := EncodeFilename(largeRaw, largeBudget); got != "" || !errors.Is(err, ErrCapacity) {
			panic("late encode capacity control")
		}
	}); allocations != 0 {
		t.Fatalf("encode allocated before full preflight: %v", allocations)
	}
	if allocations := testing.AllocsPerRun(20, func() {
		if got, err := DecodeFilename(largeDisplay, largeBudget); got != nil || !errors.Is(err, ErrCapacity) {
			panic("late decode capacity control")
		}
	}); allocations != 0 {
		t.Fatalf("decode allocated before full preflight: %v", allocations)
	}
	badDisplay := strings.Repeat("a", 64<<10) + `\x41`
	if allocations := testing.AllocsPerRun(20, func() {
		if got, err := DecodeFilename(badDisplay, math.MaxInt64); got != nil || !errors.Is(err, ErrEncoding) {
			panic("late canonical control")
		}
	}); allocations != 0 {
		t.Fatalf("decode allocated before canonical validation: %v", allocations)
	}
}

func FuzzFilenameRoundTrip(f *testing.F) {
	for _, raw := range [][]byte{nil, []byte("a/file.mkv"), {0, 0xff, '\\'}, []byte(`\xff`), []byte("é")} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64<<10 {
			t.Skip()
		}
		before := bytes.Clone(raw)
		encoded, err := EncodeFilename(raw, int64(len(raw))*5)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeFilename(encoded, int64(len(raw)+len(encoded)))
		if err != nil || !bytes.Equal(decoded, raw) || !bytes.Equal(raw, before) {
			t.Fatalf("roundtrip: %x, %v", decoded, err)
		}
	})
}

func FuzzFilenameCanonical(f *testing.F) {
	for _, display := range []string{"", `\xff`, `\x5cxff`, `\x41`, `\Xff`, "é", "\x00"} {
		f.Add(display)
	}
	f.Fuzz(func(t *testing.T, display string) {
		if len(display) > 64<<10 {
			t.Skip()
		}
		raw, err := DecodeFilename(display, int64(len(display))*2)
		if err != nil {
			if !errors.Is(err, ErrEncoding) {
				t.Fatal(err)
			}
			return
		}
		encoded, err := EncodeFilename(raw, int64(len(raw)+len(display)))
		if err != nil || encoded != display {
			t.Fatalf("noncanonical inverse: %q, %v", encoded, err)
		}
	})
}
