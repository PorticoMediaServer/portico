// Package scanbench builds harness-only physical scan fixtures. It opens no
// catalogue and never turns a traversal/probe callback into an Applied receipt.
package scanbench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/diskspace"
)

const FixtureVersion = "scan-library-v1"
const QualificationFiles = 400_000
const filesPerDirectory = 128

// FixtureConfig deliberately requires real caller-supplied media. Payloads are
// duplicated into independent files; hardlinks/sparse files would distort IO.
// This first fixture is homogeneous, not a mixed-media performance claim.
type FixtureConfig struct {
	Files    int    `json:"files"`
	Seed     uint64 `json:"seed"`
	Template string `json:"-"`
}

type Fixture struct {
	Version        string `json:"version"`
	Files          int    `json:"files"`
	Seed           uint64 `json:"seed"`
	Extension      string `json:"extension"`
	TemplateBytes  int64  `json:"template_bytes"`
	TemplateSHA256 string `json:"template_sha256"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ModifiedNS     int64  `json:"modified_ns"`
}

// Generate creates root exclusively. Failure leaves an explicitly incomplete
// fixture for caller-owned cleanup; the final hidden manifest exists ONLY on
// complete success. Nothing already present is overwritten or removed.
func Generate(ctx context.Context, root string, c FixtureConfig) (f Fixture, err error) {
	if !filepath.IsAbs(root) || c.Files < 1 || c.Files > QualificationFiles {
		return f, errors.New("invalid fixture root/count")
	}
	template, err := os.Open(c.Template)
	if err != nil {
		return f, err
	}
	defer template.Close()
	info, err := template.Stat()
	if err != nil {
		return f, err
	}
	ext := strings.ToLower(filepath.Ext(c.Template))
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 64<<20 || !mediaExtension(ext) {
		return f, errors.New("fixture requires a regular 1-byte..64-MiB media template")
	}
	if info.Size() > (1<<63-1)/int64(c.Files) {
		return f, errors.New("fixture size overflow")
	}
	// Include small-file allocation and directory/inode overhead, leaving the
	// shared-host floor intact. This preflight is not a reservation.
	required := int64(c.Files) * (info.Size() + 8192)
	free, err := diskspace.Free(filepath.Dir(root))
	if err != nil {
		return f, err
	}
	if required > free-(20<<30) {
		return f, errors.New("fixture would consume the 20-GiB disk headroom floor")
	}
	buf := make([]byte, 64<<10)
	hash := sha256.New()
	if _, err := io.CopyBuffer(hash, io.NewSectionReader(template, 0, info.Size()), buf); err != nil {
		return f, err
	}
	stamp := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	f = Fixture{Version: FixtureVersion, Files: c.Files, Seed: c.Seed, Extension: ext, TemplateBytes: info.Size(), TemplateSHA256: hex.EncodeToString(hash.Sum(nil)), ModifiedNS: stamp.UnixNano()}
	if err := ctx.Err(); err != nil {
		return f, err
	}
	anchor, err := createFixtureRoot(root)
	if err != nil {
		return f, err
	}
	defer anchor.Close()
	var bucket *fixtureDir
	defer func() {
		if bucket != nil {
			bucket.Close()
		}
	}()
	manifest := sha256.New()
	// Include the entire shape and payload identity, independently of native IDs.
	fmt.Fprintf(manifest, "%s\n%d\n%d\n%s\n%d\n%s\n%d\n", f.Version, f.Files, f.Seed, f.Extension, f.TemplateBytes, f.TemplateSHA256, f.ModifiedNS)
	for i := 0; i < c.Files; i++ {
		if err := ctx.Err(); err != nil {
			return f, err
		}
		relative := fixturePath(i, c.Seed, ext)
		if i%filesPerDirectory == 0 {
			if bucket != nil {
				if err := bucket.Close(); err != nil {
					return f, err
				}
				bucket = nil
			}
			bucket, err = anchor.bucket(filepath.Dir(relative))
			if err != nil {
				return f, err
			}
		}
		out, err := bucket.create(filepath.Base(relative))
		if err != nil {
			return f, err
		}
		hash.Reset()
		n, copyErr := io.CopyBuffer(io.MultiWriter(out, hash), io.NewSectionReader(template, 0, info.Size()), buf)
		stampErr := stampFixtureFile(out, stamp)
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil || stampErr != nil {
			return f, errors.Join(copyErr, closeErr, stampErr)
		}
		if n != info.Size() || hex.EncodeToString(hash.Sum(nil)) != f.TemplateSHA256 {
			return f, errors.New("media template changed during generation")
		}
		fmt.Fprintln(manifest, filepath.ToSlash(relative))
	}
	f.ManifestSHA256 = hex.EncodeToString(manifest.Sum(nil))
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return f, err
	}
	// Publish through the retained root; do not re-resolve the root pathname.
	out, err := anchor.create(".portico-scan-fixture.json")
	if err != nil {
		return f, err
	}
	_, err = out.Write(append(data, '\n'))
	return f, errors.Join(err, out.Close())
}

func fixturePath(i int, seed uint64, ext string) string {
	// Index guarantees uniqueness even for seeds with the same displayed suffix.
	return filepath.Join(fmt.Sprintf("bucket-%06d", i/filesPerDirectory), fmt.Sprintf("Fixture %09d-%016x (%04d)%s", i, seed, 1980+i%45, ext))
}

func mediaExtension(s string) bool {
	switch s {
	case ".mkv", ".mp4", ".avi", ".wav", ".flac", ".mp3", ".m4a", ".epub", ".cbz":
		return true
	}
	return false
}

// ReadFixture admits only a completed manifest of the current exact shape.
// Payloads are not opened here: a traversal-only clock must not hide probing.
func ReadFixture(root string) (f Fixture, err error) {
	if !filepath.IsAbs(root) {
		return f, errors.New("fixture root must be absolute")
	}
	in, err := os.Open(filepath.Join(root, ".portico-scan-fixture.json"))
	if err != nil {
		return f, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return f, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8192 {
		return f, errors.New("oversized/nonregular fixture manifest")
	}
	d := json.NewDecoder(io.LimitReader(in, 8192))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		return f, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return f, errors.New("trailing fixture manifest data")
	}
	if f.Version != FixtureVersion || f.Files < 1 || f.Files > QualificationFiles || !mediaExtension(f.Extension) ||
		f.TemplateBytes < 1 || f.TemplateBytes > 64<<20 || len(f.TemplateSHA256) != 64 || f.ModifiedNS != time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano() {
		return f, errors.New("invalid fixture manifest")
	}
	if _, err := hex.DecodeString(f.TemplateSHA256); err != nil {
		return f, err
	}
	manifest := sha256.New()
	fmt.Fprintf(manifest, "%s\n%d\n%d\n%s\n%d\n%s\n%d\n", f.Version, f.Files, f.Seed, f.Extension, f.TemplateBytes, f.TemplateSHA256, f.ModifiedNS)
	for i := 0; i < f.Files; i++ {
		fmt.Fprintln(manifest, filepath.ToSlash(fixturePath(i, f.Seed, f.Extension)))
	}
	if hex.EncodeToString(manifest.Sum(nil)) != f.ManifestSHA256 {
		return f, errors.New("fixture manifest digest mismatch")
	}
	return f, nil
}
