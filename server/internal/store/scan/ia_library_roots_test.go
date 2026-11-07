package scan

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Abs retains the current native volume on Windows, where FromSlash("/...")
// alone is rooted but not absolute. Concatenate the tail to preserve deliberate
// dot segments in the ownership fixture rather than cleaning them with Join.
func iaNativeRoot(t *testing.T, tail string) string {
	t.Helper()
	root, err := filepath.Abs(string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	return root + filepath.FromSlash(tail)
}

func TestIALibraryRootsPreserveExactOwnedPaths(t *testing.T) {
	paths := [][]byte{[]byte(iaNativeRoot(t, "media/Cafe\u0301/../Cafe\u0301")), []byte(iaNativeRoot(t, "media/資料"))}
	want := []string{string(paths[0]), string(paths[1])}
	budget := int64(len(paths[0]) + len(paths[1]))
	roots, err := PrepareLibraryRoots(paths, budget)
	if err != nil || roots.Len() != 2 || roots.Bytes() != budget {
		t.Fatalf("roots=%+v err=%v", roots, err)
	}
	paths[0][1] = 'X'
	paths[1] = []byte("/changed")
	for i, expected := range want {
		if got, ok := roots.Root(i); !ok || got != expected {
			t.Fatalf("root %d=%q, %v; want %q", i, got, ok, expected)
		}
	}
	for _, i := range []int{-1, 2} {
		if got, ok := roots.Root(i); ok || got != "" {
			t.Fatalf("out of range root %d=%q, %v", i, got, ok)
		}
	}
}

func TestIALibraryRootsRefuseUnrepresentableWithoutLeakingPaths(t *testing.T) {
	for _, raw := range [][][]byte{
		{[]byte("/private-name/"), {'/', 0xff}},
		{{'/', 0xff}, []byte("/private-name/")},
	} {
		roots, err := PrepareLibraryRoots(raw, 8192)
		if !errors.Is(err, ErrRootUnrepresentable) || roots.Len() != 0 || roots.Bytes() != 0 {
			t.Fatalf("roots=%+v err=%v", roots, err)
		}
		if strings.Contains(err.Error(), "private-name") || strings.Contains(err.Error(), "\ufffd") {
			t.Fatalf("path leaked into error: %v", err)
		}
	}
}

func TestIALibraryRootsInvalidAndCapacity(t *testing.T) {
	abs := []byte(iaNativeRoot(t, "media"))
	cases := []struct {
		name string
		raw  [][]byte
		cap  int64
		want error
	}{
		{"empty batch", nil, 8192, ErrInvalid},
		{"empty root", [][]byte{nil}, 8192, ErrInvalid},
		{"relative", [][]byte{[]byte("media")}, 8192, ErrInvalid},
		{"control", [][]byte{append(bytes.Clone(abs), '\n')}, 8192, ErrInvalid},
		{"unicode control", [][]byte{[]byte(string(abs) + "\u0085")}, 8192, ErrInvalid},
		{"NUL before UTF8", [][]byte{{'/', 0xff}, {'/', 0}}, 8192, ErrInvalid},
		{"oversized", [][]byte{append([]byte{'/'}, bytes.Repeat([]byte{'a'}, MaxLibraryRootBytes)...)}, 16384, ErrCapacity},
		{"zero budget", [][]byte{abs}, 0, ErrCapacity},
		{"raw byte boundary", [][]byte{abs}, int64(len(abs) - 1), ErrCapacity},
		{"aggregate boundary", [][]byte{abs, abs}, int64(2*len(abs) - 1), ErrCapacity},
		{"too many roots", make([][]byte, MaxLibraryRoots+1), 8192, ErrCapacity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roots, err := PrepareLibraryRoots(tc.raw, tc.cap)
			if !errors.Is(err, tc.want) || roots.Len() != 0 || roots.Bytes() != 0 {
				t.Fatalf("roots=%+v err=%v; want %v", roots, err, tc.want)
			}
		})
	}
}

func TestIALibraryRootsMaximumAcceptedBoundaries(t *testing.T) {
	prefix := []byte(iaNativeRoot(t, ""))
	root := append(prefix, bytes.Repeat([]byte{'a'}, MaxLibraryRootBytes-len(prefix))...)
	if roots, err := PrepareLibraryRoots([][]byte{root}, MaxLibraryRootBytes); err != nil || roots.Bytes() != MaxLibraryRootBytes {
		t.Fatalf("max root bytes=%d err=%v", roots.Bytes(), err)
	}
	roots := make([][]byte, MaxLibraryRoots)
	for i := range roots {
		roots[i] = []byte(iaNativeRoot(t, "media"))
	}
	if got, err := PrepareLibraryRoots(roots, MaxLibraryRoots*MaxLibraryRootBytes); err != nil || got.Len() != MaxLibraryRoots {
		t.Fatalf("max roots=%d err=%v", got.Len(), err)
	}
}
