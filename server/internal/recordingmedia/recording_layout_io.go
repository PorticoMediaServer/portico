package recordingmedia

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/artifactlease"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// layoutBinding is private custody evidence, not a viewer path or a permission.
// The scheduler must reserve the exact target for this occurrence durably before
// linkLayout. A matching digest alone never establishes target ownership.
type layoutBinding struct {
	allowExisting                                bool // only a prior durable prepared reservation permits crash recovery
	OwnerKey, RecordingID, ArtifactKey           string
	RootIdentity, SourceIdentity, TargetIdentity string
	Source, Target                               string // relative to the already selected recording root
	Object                                       mediaartifact.Object
}

func layoutComponent(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 255 || !utf8.ValidString(s) || strings.ContainsAny(s, "/\\\x00") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func layoutRelative(s string) bool {
	if len(s) > 4096 || !filepath.IsLocal(s) || filepath.ToSlash(filepath.Clean(s)) != s {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if !layoutComponent(part) {
			return false
		}
	}
	return true
}
func (b layoutBinding) valid() bool {
	if b.TargetIdentity != "" && b.TargetIdentity != b.SourceIdentity {
		return false
	}
	if !recordingID.MatchString(b.OwnerKey) || !recordingID.MatchString(b.RecordingID) || !recordingID.MatchString(b.Object.Digest) || b.Object.Size <= 0 || b.RootIdentity == "" || b.SourceIdentity == "" {
		return false
	}
	id, generation, ok := strings.Cut(b.ArtifactKey, ".")
	if !ok || id != b.RecordingID || generation == "" {
		return false
	}
	if _, err := mediaartifact.RetainedCaptureKey(id, parseLayoutGeneration(generation)); err != nil {
		return false
	}
	if b.Source != b.OwnerKey+"/captures/"+b.RecordingID+"/objects/"+b.Object.Digest || !layoutRelative(b.Target) {
		return false
	}
	parts := strings.Split(b.Target, "/")
	return len(parts) >= 1 && !(recordingID.MatchString(parts[0]) && len(parts) > 1 && (parts[1] == "captures" || parts[1] == "objects")) && b.Source != b.Target
}
func parseLayoutGeneration(s string) int64 {
	var n int64
	if len(s) > 19 || s == "" || s[0] == '0' {
		return 0
	}
	for _, r := range s {
		if r < '0' || r > '9' || n > (int64(^uint64(0)>>1)-int64(r-'0'))/10 {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return n
}
func layoutIdentity(f *os.File, expected string) error {
	observed, err := storage.FileObjectIdentity(f)
	if err != nil {
		return err
	}
	if observed != expected {
		return mediaartifact.ErrIdentity
	}
	return nil
}
func verifyLayoutObject(ctx context.Context, f *os.File, object mediaartifact.Object) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0222 != 0 || info.Size() != object.Size {
		return mediaartifact.ErrIdentity
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var offset int64
	for offset < object.Size {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, readErr := f.ReadAt(buf[:min(int64(len(buf)), object.Size-offset)], offset)
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		if n == 0 {
			return mediaartifact.ErrIdentity
		}
		h.Write(buf[:n])
		offset += int64(n)
	}
	if hex.EncodeToString(h.Sum(nil)) != object.Digest {
		return mediaartifact.ErrIdentity
	}
	after, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, after) || after.Size() != info.Size() || after.ModTime() != info.ModTime() || after.Mode() != info.Mode() {
		return mediaartifact.ErrIdentity
	}
	return ctx.Err()
}

// linkLayout is an atomic no-replace publication of already sealed bytes. All
// ancestors are descriptor-pinned without following links. A retry is accepted
// only for the reserved occurrence's source inode; equal bytes are insufficient.
func linkLayout(ctx context.Context, rootPath string, b layoutBinding) (layoutBinding, error) {
	if !b.valid() {
		return b, mediaartifact.ErrIdentity
	}
	if err := ctx.Err(); err != nil {
		return b, err
	}
	root, err := openLayoutDirectory(rootPath)
	if err != nil {
		return b, err
	}
	defer root.Close()
	if err = layoutIdentity(root.file, b.RootIdentity); err != nil {
		return b, err
	}
	source, old, err := root.parent(b.Source, false)
	if err != nil {
		return b, err
	}
	defer source.Close()
	f, err := source.open(old)
	if err != nil {
		return b, err
	}
	defer f.Close()
	if err = artifactlease.Shared(f); err != nil {
		return b, mediaartifact.ErrLeased
	}
	if err = layoutIdentity(f, b.SourceIdentity); err != nil {
		return b, err
	}
	if err = verifyLayoutObject(ctx, f, b.Object); err != nil {
		return b, err
	}
	target, name, err := root.parent(b.Target, true)
	if err != nil {
		return b, err
	}
	defer target.Close()
	if err = sameLayoutParent(rootPath, root, b.Target, target); err != nil {
		return b, err
	}
	if err = ctx.Err(); err != nil {
		return b, err
	}
	err = target.link(source, old, name)
	existed := errors.Is(err, os.ErrExist)
	if existed && b.TargetIdentity == "" && !b.allowExisting {
		return b, dvr.ErrRecordingLayoutCollision
	}
	if err != nil && !existed {
		return b, err
	}
	linked, err := target.open(name)
	if err != nil {
		if existed {
			return b, dvr.ErrRecordingLayoutCollision
		}
		return b, err
	}
	defer linked.Close()
	if err = layoutIdentity(linked, b.SourceIdentity); err != nil {
		if existed && b.TargetIdentity == "" {
			return b, dvr.ErrRecordingLayoutCollision
		}
		return b, err
	}
	if b.TargetIdentity != "" {
		if err = layoutIdentity(linked, b.TargetIdentity); err != nil {
			return b, err
		}
	}
	if err = verifyLayoutObject(ctx, linked, b.Object); err != nil {
		return b, err
	}
	// Detect a replaced source name as well as a target that changed at link.
	current, err := source.open(old)
	if err != nil {
		return b, err
	}
	defer current.Close()
	if err = layoutIdentity(current, b.SourceIdentity); err != nil {
		return b, err
	}
	if err = target.file.Sync(); err != nil {
		return b, err
	}
	if err = sameLayoutParent(rootPath, root, b.Target, target); err != nil {
		return b, err
	}
	b.TargetIdentity = b.SourceIdentity
	return b, ctx.Err()
}

// removeLayout removes only the exact recorded target after physical readers
// retire. A replaced root, parent symlink, inode or equal-byte copy is refused.
// Missing target is idempotent; it never authorizes removing anything nearby.
func removeLayout(ctx context.Context, rootPath string, b layoutBinding) error {
	if !b.valid() || b.TargetIdentity == "" {
		return mediaartifact.ErrIdentity
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := openLayoutDirectory(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = layoutIdentity(root.file, b.RootIdentity); err != nil {
		return err
	}
	target, name, err := root.parent(b.Target, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer target.Close()
	f, err := target.open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if err = layoutIdentity(f, b.TargetIdentity); err != nil {
		return err
	}
	if err = artifactlease.Exclusive(f); err != nil {
		return mediaartifact.ErrLeased
	}
	if err = verifyLayoutObject(ctx, f, b.Object); err != nil {
		return err
	}
	current, err := target.open(name)
	if err != nil {
		return err
	}
	defer current.Close()
	if err = layoutIdentity(current, b.TargetIdentity); err != nil {
		return err
	}
	if err = sameLayoutParent(rootPath, root, b.Target, target); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return target.remove(name)
}

// Recheck the current namespace against pinned descriptors. A renamed parent
// can never be silently mistaken for the directory now at the planned path.
func sameLayoutParent(rootPath string, root *layoutDirectory, relative string, parent *layoutDirectory) error {
	current, err := openLayoutDirectory(rootPath)
	if err != nil {
		return err
	}
	defer current.Close()
	a, err := root.file.Stat()
	if err != nil {
		return err
	}
	b, err := current.file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(a, b) {
		return mediaartifact.ErrIdentity
	}
	p, _, err := current.parent(relative, false)
	if err != nil {
		return err
	}
	defer p.Close()
	a, err = parent.file.Stat()
	if err != nil {
		return err
	}
	b, err = p.file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(a, b) {
		return mediaartifact.ErrIdentity
	}
	return nil
}
