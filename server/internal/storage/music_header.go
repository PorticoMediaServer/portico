package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"time"
)

// ReadScanAudioPrefix reads at most one MiB through the scan's existing root,
// helper and authority. It never reads the rest of a large music file.
func ReadScanAudioPrefix(ctx context.Context, path string) ([]byte, error) {
	return ReadScanPrefix(ctx, path, 1<<20)
}

// ReadScanPrefix uses the same inventory authority as audio header reads, with
// a smaller bound for container headers. It never captures the rest of a file.
func ReadScanPrefix(ctx context.Context, path string, limit int64) ([]byte, error) {
	if limit <= 0 || limit > 1<<20 {
		return nil, ErrRemoteLimit
	}
	if err := CheckScanRead(ctx, path); err != nil {
		return nil, err
	}
	r, input, err := inventoryReadRequest(ctx, path)
	if err != nil {
		return nil, err
	}
	if input == nil {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return scanPrefix(f, limit)
	}
	c := input.client
	if c.IsRemote(path) {
		return nil, ErrRemoteRange
	}
	if c.Guard != nil {
		if err := c.Guard(path); err != nil {
			return nil, err
		}
	}
	req := request{Operation: "inventory-audio-prefix", Path: path, Inventory: &r, Limit: limit}
	if c.MountedRoot != nil {
		req.MountedRoot = c.MountedRoot(path)
	}
	raw, _ := json.Marshal(req)
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(raw)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var prefix []byte
	err = c.Supervisor.Run(ctx, "scan:"+input.source, cmd, func(rd io.Reader) error {
		var err error
		prefix, err = io.ReadAll(io.LimitReader(rd, limit+1))
		if int64(len(prefix)) > limit {
			return ErrInventoryChanged
		}
		return err
	})
	return prefix, err
}

func scanPrefix(f *os.File, limit int64) ([]byte, error) {
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return nil, ErrInventoryChanged
	}
	prefix, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return nil, ErrInventoryChanged
	}
	return prefix, nil
}

func inventoryAudioPrefixHelper(r request, out io.Writer) error {
	if r.Inventory == nil || r.Limit <= 0 || r.Limit > 1<<20 {
		return ErrInventoryChanged
	}
	f, err := openInventoryContent(*r.Inventory)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix, err := scanPrefix(f, r.Limit)
	if err != nil {
		return err
	}
	_, err = out.Write(prefix)
	return err
}
