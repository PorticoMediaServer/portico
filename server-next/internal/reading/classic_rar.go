//go:build portico_rar7z

package reading

import (
	"errors"
	"io"

	"github.com/nwaples/rardecode/v2"
)

const rarEnabled = true

func rarMembers(r io.ReaderAt, size int64, wanted string, out io.Writer) ([]comicMember, error) {
	reader, err := rardecode.NewReader(io.NewSectionReader(r, 0, size), rardecode.MaxDictionarySize(maxPartSize))
	if err != nil {
		return nil, errDamaged
	}
	members := []comicMember{}
	seen := map[string]bool{}
	var total uint64
	var page []byte
	for count := 0; ; count++ {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || count >= maxEntries {
			return nil, errDamaged
		}
		if header.Encrypted || header.HeaderEncrypted || header.UnKnownSize || header.LinkType != rardecode.LinkTypeNone || header.UnPackedSize < 0 {
			return nil, errDamaged
		}
		if uint64(header.UnPackedSize) > maxArchiveSize-total {
			return nil, errDamaged
		}
		total += uint64(header.UnPackedSize)
		if header.IsDir || !comicEntry(header.Name) {
			continue
		}
		if seen[header.Name] {
			return nil, errDamaged
		}
		seen[header.Name] = true
		members = append(members, comicMember{header.Name, uint64(header.UnPackedSize)})
		if header.Name == wanted {
			if header.UnPackedSize > maxPartSize {
				return nil, errPageTooLarge
			}
			page, err = io.ReadAll(io.LimitReader(reader, header.UnPackedSize+1))
			if err != nil || int64(len(page)) != header.UnPackedSize {
				return nil, errDamaged
			}
		}
	}
	if wanted != "" {
		if page == nil {
			return nil, errDamaged
		}
		_, err = out.Write(page)
	}
	return members, err
}
