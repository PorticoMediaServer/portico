//go:build portico_cb7

package reading

import (
	"encoding/binary"
	"io"

	"github.com/bodgit/sevenzip"
)

const sevenEnabled = true

func sevenMembers(r io.ReaderAt, size int64, wanted string, out io.Writer) ([]comicMember, error) {
	var header [32]byte
	if size < 32 {
		return nil, errDamaged
	}
	if _, err := r.ReadAt(header[:], 0); err != nil || string(header[:6]) != "7z\xbc\xaf'\x1c" {
		return nil, errDamaged
	}
	offset, n := binary.LittleEndian.Uint64(header[12:20]), binary.LittleEndian.Uint64(header[20:28])
	if offset > uint64(size)-32 || n == 0 || n > 16<<20 || n > uint64(size)-32-offset {
		return nil, errDamaged
	}
	var marker [1]byte
	if _, err := r.ReadAt(marker[:], int64(offset)+32); err != nil || marker[0] != 1 {
		return nil, errDamaged
	}
	reader, err := sevenzip.NewReader(r, size)
	if err != nil || len(reader.File) > maxEntries {
		return nil, errDamaged
	}
	members := []comicMember{}
	seen := map[string]bool{}
	var total uint64
	var selected *sevenzip.File
	for _, f := range reader.File {
		if f.UncompressedSize > maxArchiveSize-total {
			return nil, errDamaged
		}
		total += f.UncompressedSize
		if !f.Mode().IsRegular() || !comicEntry(f.Name) {
			continue
		}
		if seen[f.Name] {
			return nil, errDamaged
		}
		seen[f.Name] = true
		members = append(members, comicMember{f.Name, f.UncompressedSize})
		if f.Name == wanted {
			selected = f
		}
	}
	if wanted != "" {
		if selected == nil {
			return nil, errDamaged
		}
		if selected.UncompressedSize > maxPartSize {
			return nil, errPageTooLarge
		}
		rc, err := selected.Open()
		if err != nil {
			return nil, errDamaged
		}
		defer rc.Close()
		body, err := io.ReadAll(io.LimitReader(rc, int64(selected.UncompressedSize)+1))
		if err != nil || uint64(len(body)) != selected.UncompressedSize {
			return nil, errDamaged
		}
		if _, err = out.Write(body); err != nil {
			return nil, err
		}
	}
	return members, nil
}
