package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type audioCover struct{ Data []byte }

var audioDiscFolder = regexp.MustCompile(`(?i)^(disc|disk|cd|vol|volume|part|act|digital media)[ ._-]*[0-9]{1,3}(?:$|[ ._()-].*)`)

func (c *Client) ReadAudioCover(ctx context.Context, key, path string) ([]byte, error) {
	r, input, e := inventoryReadRequest(ctx, path)
	if e != nil {
		return nil, e
	}
	if input == nil {
		return nil, os.ErrNotExist
	}
	if c.Guard != nil {
		if e = c.Guard(path); e != nil {
			return nil, e
		}
	}
	if e = CheckScanRead(ctx, path); e != nil {
		return nil, e
	}
	req := request{Operation: "inventory-audio-cover", Inventory: &r, Path: path}
	if c.MountedRoot != nil {
		req.MountedRoot = c.MountedRoot(path)
	}
	raw, _ := json.Marshal(req)
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(raw)
	var cover audioCover
	e = c.Supervisor.Run(ctx, key, cmd, func(rd io.Reader) error {
		raw, e := io.ReadAll(io.LimitReader(rd, (6<<20)+1))
		if e != nil {
			return e
		}
		if len(raw) > 6<<20 {
			return errors.New("cover exceeds limit")
		}
		return json.Unmarshal(raw, &cover)
	})
	return cover.Data, e
}
func inventoryAudioCoverHelper(r request, out io.Writer) error {
	if r.Inventory == nil {
		return ErrInventoryChanged
	}
	root, e := openInventoryRoot(*r.Inventory)
	if e != nil {
		return e
	}
	defer root.Close()
	dir := filepath.Dir(r.Inventory.RelativePath)
	dirs := []string{dir}
	if audioDiscFolder.MatchString(filepath.Base(dir)) {
		parent := filepath.Dir(dir)
		if filepath.IsLocal(parent) {
			dirs = append(dirs, parent)
		}
	}
	for _, dir := range dirs {
		f, e := root.Open(dir)
		if e != nil {
			continue
		}
		names, e := f.ReadDir(4096)
		f.Close()
		if e != nil && e != io.EOF {
			continue
		}
		sort.Slice(names, func(i, j int) bool { return names[i].Name() < names[j].Name() })
		for _, stem := range []string{"cover", "folder", "front", "album", "poster", "default", "albumart"} {
			for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
				wanted := stem + ext
				for _, entry := range names {
					if entry.IsDir() || !strings.EqualFold(entry.Name(), wanted) {
						continue
					}
					path := filepath.Join(dir, entry.Name())
					pic, e := root.Open(path)
					if e != nil {
						continue
					}
					before, e := pic.Stat()
					if e != nil || !before.Mode().IsRegular() || before.Size() > 4<<20 {
						pic.Close()
						continue
					}
					data, readErr := io.ReadAll(io.LimitReader(pic, (4<<20)+1))
					after, statErr := pic.Stat()
					pic.Close()
					current, pathErr := root.Stat(path)
					if readErr != nil || statErr != nil || pathErr != nil || len(data) > 4<<20 || before.Size() != after.Size() || before.ModTime() != after.ModTime() || !os.SameFile(before, current) {
						continue
					}
					return json.NewEncoder(out).Encode(audioCover{data})
				}
			}
		}
	}
	return os.ErrNotExist
}
