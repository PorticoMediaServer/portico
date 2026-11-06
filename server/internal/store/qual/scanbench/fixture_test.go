package scanbench

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestFixtureDeterminismAndPhysicalCopies(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("secure directory handle fixture")
	}
	scratch := t.TempDir()
	template := filepath.Join(scratch, "seed.mkv")
	data := []byte("caller supplied small payload; traversal test, not a playable media claim")
	if err := os.WriteFile(template, data, 0600); err != nil {
		t.Fatal(err)
	}
	c := FixtureConfig{Files: 257, Seed: 42, Template: template}
	a, b := filepath.Join(scratch, "a"), filepath.Join(scratch, "b")
	fa, err := Generate(t.Context(), a, c)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := Generate(t.Context(), b, c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fa, fb) {
		t.Fatal("nondeterministic manifest")
	}
	got, err := ReadFixture(a)
	if err != nil || !reflect.DeepEqual(got, fa) {
		t.Fatal(got, err)
	}
	one, err := os.Stat(filepath.Join(a, fixturePath(0, 42, ".mkv")))
	if err != nil {
		t.Fatal(err)
	}
	two, err := os.Stat(filepath.Join(a, fixturePath(1, 42, ".mkv")))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(one, two) || one.Size() != int64(len(data)) || one.ModTime().UnixNano() != fa.ModifiedNS {
		t.Fatal("file copy/size/time changed")
	}
	if _, err := Generate(t.Context(), a, c); err == nil {
		t.Fatal("overwrote existing fixture")
	}
	raw, err := os.ReadFile(filepath.Join(a, ".portico-scan-fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte(` {"extra":true}`)...)
	if err := os.WriteFile(filepath.Join(a, ".portico-scan-fixture.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFixture(a); err == nil {
		t.Fatal("trailing manifest accepted")
	}
	if err := os.WriteFile(filepath.Join(a, ".portico-scan-fixture.json"), append(raw[:len(raw)-len(` {"extra":true}`)], bytes.Repeat([]byte(" "), 9000)...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFixture(a); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}
func TestFixtureCanceledAndInvalid(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	template := filepath.Join(t.TempDir(), "seed.wav")
	if err := os.WriteFile(template, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Generate(ctx, root, FixtureConfig{Files: 3, Template: template}); err == nil {
		t.Fatal("canceled generation succeeded")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("canceled generation created root", err)
	}
	for _, n := range []int{0, -1, QualificationFiles + 1} {
		if _, err := Generate(t.Context(), root, FixtureConfig{Files: n, Template: template}); err == nil {
			t.Fatal("bad count", n)
		}
	}
}

type fixtureSwapContext struct {
	context.Context
	check func()
}

func (c fixtureSwapContext) Err() error { c.check(); return c.Context.Err() }

func TestFixtureSwappedBucketCannotEscape(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("retained directory handles")
	}
	scratch := t.TempDir()
	root := filepath.Join(scratch, "library")
	outside := filepath.Join(scratch, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(scratch, "seed.wav")
	if err := os.WriteFile(template, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	ctx := fixtureSwapContext{Context: t.Context(), check: func() {
		calls++
		if calls == 3 {
			if err := os.Rename(filepath.Join(root, "bucket-000000"), filepath.Join(root, "retained")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "bucket-000000")); err != nil {
				t.Fatal(err)
			}
		}
	}}
	_, err := Generate(ctx, root, FixtureConfig{Files: 2, Template: template})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("bucket swap escaped root", entries, err)
	}
	if _, err := os.Stat(filepath.Join(root, "retained", filepath.Base(fixturePath(1, 0, ".wav")))); err != nil {
		t.Fatal("lost retained bucket", err)
	}
}
