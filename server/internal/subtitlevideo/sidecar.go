package subtitlevideo

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func (r *Runtime) AcquireSubtitleSidecar(ctx context.Context, item, source, path string, size, modified int64, format string) ([]byte, []byte, bool, error) {
	if r.RemoteStorage == nil || !r.RemoteStorage.Handles(path) {
		return nil, nil, false, nil
	}
	s, e := r.selection(ctx, item, source, "")
	if e != nil {
		return nil, nil, true, e
	}
	stem := strings.TrimSuffix(filepath.Base(s.path), filepath.Ext(s.path))
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	directory := filepath.Dir(path)
	nested := filepath.Dir(directory) == filepath.Dir(s.path) && (strings.EqualFold(filepath.Base(directory), "Subs") || strings.EqualFold(filepath.Base(directory), "Subtitles"))
	if (directory != filepath.Dir(s.path) && !nested) || !(strings.EqualFold(stem, name) || strings.HasPrefix(strings.ToLower(name), strings.ToLower(stem)+".")) {
		return nil, nil, true, subtitles.ErrInput
	}
	read := func(path string, expectedSize, expectedModified, limit int64) ([]byte, error) {
		input, e := r.RemoteStorage.Open(ctx, item, source, path, expectedSize, expectedModified)
		if e != nil {
			return nil, e
		}
		defer input.Close()
		if input.Size() < 1 || input.Size() > limit {
			return nil, subtitles.ErrCapacity
		}
		data := make([]byte, 0, int(input.Size()))
		for int64(len(data)) < input.Size() {
			part, e := input.ReadExtent(ctx, int64(len(data)), min(int64(256<<10), input.Size()-int64(len(data))))
			if e != nil {
				return nil, e
			}
			if len(part) == 0 {
				return nil, subtitles.ErrConflict
			}
			data = append(data, part...)
		}
		if e = input.Validate(ctx); e != nil {
			return nil, e
		}
		return data, nil
	}
	limit := int64(subtitles.MaxInputBytes)
	if format == "sup" || format == "pgs" {
		limit = int64(subtitles.BinaryLimitBytes())
	}
	data, e := read(path, size, modified, limit)
	if e != nil {
		return nil, nil, true, e
	}
	var companion []byte
	if format == "idx" || format == "vobsub" {
		companion, e = read(strings.TrimSuffix(path, filepath.Ext(path))+".sub", -1, 0, int64(subtitles.BinaryLimitBytes()))
		if e != nil {
			return nil, nil, true, e
		}
	}
	if e = r.stillSelected(ctx, s); e != nil {
		return nil, nil, true, e
	}
	return data, companion, true, nil
}
