package livechannels

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"strings"
	"time"
)

// Remote guides remain private files throughout preview and publication. Only
// their digest/path is parked in the expiring preview; XML never enters a large
// JSON snapshot or the main database's writer as an opaque provider document.
func readGuideFile(ctx context.Context, locator string, policy remotemedia.Policy, limit int64) (string, string, error) {
	c, e := remotemedia.New(ctx, locator, policy)
	if e != nil {
		return "", "", sourceFetchError(e)
	}
	defer c.Close()
	response, e := c.Open(ctx, "GET", "")
	if e != nil {
		return "", "", sourceFetchError(e)
	}
	defer response.Body.Close()
	if limit <= 0 || limit > 1<<30 || response.StatusCode != 200 || response.ContentLength > limit {
		return "", "", ErrSourceUnavailable
	}
	compressed := &io.LimitedReader{R: response.Body, N: limit + 1}
	buffer := bufio.NewReader(compressed)
	prefix, _ := buffer.Peek(2)
	var reader io.Reader = buffer
	encoding := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" && encoding != "gzip" {
		return "", "", ErrSourceUnavailable
	}
	if encoding == "gzip" || len(prefix) == 2 && prefix[0] == 0x1f && prefix[1] == 0x8b {
		g, e := gzip.NewReader(buffer)
		if e != nil {
			return "", "", ErrSourceUnavailable
		}
		defer g.Close()
		reader = g
	}
	file, e := os.CreateTemp(os.TempDir(), "portico-guide-*.xml")
	if e != nil {
		return "", "", e
	}
	path := file.Name()
	keep := false
	defer func() {
		file.Close()
		if !keep {
			os.Remove(path)
		}
	}()
	digest := sha256.New()
	n, e := io.CopyBuffer(io.MultiWriter(file, digest), io.LimitReader(reader, limit+1), make([]byte, 64<<10))
	if e != nil || n == 0 || n > limit || compressed.N <= 0 {
		return "", "", ErrSourceUnavailable
	}
	if e = ctx.Err(); e != nil {
		return "", "", e
	}
	if e = file.Sync(); e != nil {
		return "", "", e
	}
	keep = true
	return path, hex.EncodeToString(digest.Sum(nil)), nil
}
func (in SourceInput) openGuide() (io.ReadCloser, error) {
	if in.guideFile == "" {
		return io.NopCloser(strings.NewReader(in.Guide)), nil
	}
	parent, e := filepath.EvalSymlinks(os.TempDir())
	if e != nil {
		return nil, e
	}
	canonical, e := filepath.EvalSymlinks(in.guideFile)
	if e != nil || filepath.Dir(canonical) != parent || !strings.HasPrefix(filepath.Base(canonical), "portico-guide-") || filepath.Ext(canonical) != ".xml" {
		return nil, ErrInvalid
	}
	info, e := os.Lstat(in.guideFile)
	if e != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > int64(in.Limits.effective().Bytes-len(in.Playlist)) {
		return nil, ErrInvalid
	}
	file, e := os.Open(in.guideFile)
	if e != nil {
		return nil, e
	}
	actual, e := file.Stat()
	if e != nil || !os.SameFile(info, actual) {
		file.Close()
		return nil, ErrInvalid
	}
	return file, nil
}
func (in SourceInput) removeGuide() {
	if in.guideFile != "" {
		os.Remove(in.guideFile)
	}
}

// A crash can leave only disposable intermediate files, never half a published
// guide. Preview expires after twenty minutes and import after twenty minutes;
// a full day's grace keeps another server's currently owned files untouched.
func cleanupGuideFiles() {
	for _, pattern := range []string{"portico-guide-*.xml", "portico-programmes-*.sqlite"} {
		paths, _ := filepath.Glob(filepath.Join(os.TempDir(), pattern))
		for _, path := range paths {
			info, e := os.Lstat(path)
			if e == nil && info.Mode().IsRegular() && time.Since(info.ModTime()) > 24*time.Hour {
				os.Remove(path)
				os.Remove(path + "-journal")
			}
		}
	}
}
