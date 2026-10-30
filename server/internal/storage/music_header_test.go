package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScanAudioPrefixIsBoundedAndGuarded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.mpc")
	if err := os.WriteFile(path, make([]byte, 2<<20), 0600); err != nil {
		t.Fatal(err)
	}
	prefix, err := ReadScanAudioPrefix(context.Background(), path)
	if err != nil || len(prefix) != 1<<20 {
		t.Fatal(len(prefix), err)
	}
	if _, err := ReadScanAudioPrefix(context.Background(), filepath.Dir(path)); err == nil {
		t.Fatal("directory accepted")
	}
	blocked := errors.New("source no longer admitted")
	if _, err := ReadScanAudioPrefix(WithScanReadGuard(context.Background(), func(context.Context, string) error { return blocked }), path); !errors.Is(err, blocked) {
		t.Fatal(err)
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := New(bin)
	ctx := WithScanInput(context.Background(), client, "music-prefix", InventoryRequest{Root: filepath.Dir(path)})
	prefix, err = ReadScanAudioPrefix(ctx, path)
	if err != nil || len(prefix) != 1<<20 {
		t.Fatal("actual admitted helper prefix", len(prefix), err)
	}
	if _, err := ReadScanAudioPrefix(ctx, filepath.Join(filepath.Dir(path), "..", "outside.mpc")); err == nil {
		t.Fatal("inventory root escaped")
	}
	for _, input := range []context.Context{context.Background(), ctx} {
		prefix, err = ReadScanPrefix(input, path, 4096)
		if err != nil || len(prefix) != 4096 {
			t.Fatal("container prefix exceeded its bound", len(prefix), err)
		}
		for _, limit := range []int64{0, -1, (1 << 20) + 1} {
			if _, err := ReadScanPrefix(input, path, limit); err == nil {
				t.Fatal("invalid prefix limit accepted", limit)
			}
		}
	}
}
