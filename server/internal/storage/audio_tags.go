package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"github.com/PorticoMediaServer/portico/server/internal/audiotags"
	"time"
)

// ReadAudioTags binds scanning reads to the admitted inventory root and helper.
func ReadAudioTags(ctx context.Context, path string) (audiotags.Tags, error) {
	if err := CheckScanRead(ctx, path); err != nil {
		return audiotags.Tags{}, err
	}
	r, input, err := inventoryReadRequest(ctx, path)
	if err != nil {
		return audiotags.Tags{}, err
	}
	if input == nil {
		f, e := os.Open(path)
		if e != nil {
			return audiotags.Tags{}, e
		}
		defer f.Close()
		return tagsFromFile(f)
	}
	c := input.client
	if c.IsRemote(path) {
		return audiotags.Tags{}, ErrRemoteRange
	}
	if c.Guard != nil {
		if err = c.Guard(path); err != nil {
			return audiotags.Tags{}, err
		}
	}
	req := request{Operation: "inventory-audio-tags", Path: path, Inventory: &r}
	if c.MountedRoot != nil {
		req.MountedRoot = c.MountedRoot(path)
	}
	raw, _ := json.Marshal(req)
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(raw)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var result audiotags.Tags
	err = c.Supervisor.Run(ctx, "scan:"+input.source, cmd, func(rd io.Reader) error {
		b, e := io.ReadAll(io.LimitReader(rd, (24<<20)+1))
		if e != nil {
			return e
		}
		if len(b) > 24<<20 {
			return errors.New("audio tags exceed limit")
		}
		return json.Unmarshal(b, &result)
	})
	return result, err
}
func tagsFromFile(f *os.File) (audiotags.Tags, error) {
	before, e := f.Stat()
	if e != nil || !before.Mode().IsRegular() {
		return audiotags.Tags{}, ErrInventoryChanged
	}
	tags := audiotags.Read(f, before.Size())
	after, e := f.Stat()
	if e != nil || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return audiotags.Tags{}, ErrInventoryChanged
	}
	return tags, nil
}
func inventoryAudioTagsHelper(r request, out io.Writer) error {
	if r.Inventory == nil {
		return ErrInventoryChanged
	}
	f, e := openInventoryContent(*r.Inventory)
	if e != nil {
		return e
	}
	defer f.Close()
	tags, e := tagsFromFile(f)
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(tags)
}
