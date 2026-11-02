package archive7z

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/nwaples/rardecode/v2"
)

// RAR pages that need a real decoder — RAR4 LZ/PPM and solid archives — are
// decoded here, inside the same limited worker as CB7. The parent stages the
// archive, this process spools it privately, and the reply shape is the 7z
// modes' shape.
const rarSpoolFile = "archive.rar"

func rarList(stdin io.Reader, stdout io.Writer) error {
	members, _, err := walkRAR(stdin, "")
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(reply{Members: members})
}

func rarPage(stdin io.Reader, stdout io.Writer, name string) error {
	if !plainName(name) {
		return errors.New("invalid member")
	}
	_, body, err := walkRAR(stdin, name)
	if err != nil {
		return err
	}
	if _, err = stdout.Write(body); err != nil {
		return errors.New("output failed")
	}
	return nil
}

// walkRAR reads every header in order, admitting exactly the set list serves and
// decoding the selected member's bytes when name is set. Pages are always read
// from the first file forwards, so a solid member's window and tables come from
// the files before it; listing a solid archive decodes those files too, because
// rardecode refuses to skip them.
func walkRAR(stdin io.Reader, name string) ([]Member, []byte, error) {
	path, size, err := spoolArchive(stdin, rarSpoolFile)
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(filepath.Dir(path))
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, errors.New("unreadable archive")
	}
	defer file.Close()
	if size < 22 {
		return nil, nil, errors.New("archive too small")
	}
	reader, err := rardecode.NewReader(file, rardecode.MaxDictionarySize(MaxMember))
	if err != nil {
		return nil, nil, rarRefusal(err)
	}
	members := []Member{}
	seen := map[string]bool{}
	var expanded uint64
	var body []byte
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, rarRefusal(err)
		}
		// Encryption, links and an unknown unpacked size are refused whole:
		// none of them has a bounded, inspectable page behind it.
		if header.Encrypted || header.HeaderEncrypted || header.UnKnownSize || header.LinkType != rardecode.LinkTypeNone {
			return nil, nil, errors.New("archive not supported")
		}
		if header.IsDir || header.UnPackedSize <= 0 {
			continue
		}
		if !plainMember(header.Name, false, uint64(header.UnPackedSize)) {
			continue
		}
		if len(members) >= MaxMembers {
			return nil, nil, errors.New("member count out of bounds")
		}
		if seen[header.Name] {
			return nil, nil, errors.New("duplicate member name")
		}
		seen[header.Name] = true
		if uint64(MaxExpanded)-expanded < uint64(header.UnPackedSize) {
			return nil, nil, errors.New("member bytes out of bounds")
		}
		expanded += uint64(header.UnPackedSize)
		members = append(members, Member{Name: header.Name, Size: header.UnPackedSize})
		if header.Name != name || body != nil {
			continue
		}
		out, err := io.ReadAll(io.LimitReader(reader, header.UnPackedSize+1))
		if err != nil {
			return nil, nil, errors.New("member refused")
		}
		if int64(len(out)) != header.UnPackedSize || len(out) > MaxMember {
			return nil, nil, errors.New("member out of bounds")
		}
		body = out
	}
	if name != "" && body == nil {
		return nil, nil, errors.New("member not found")
	}
	if len(members) == 0 {
		return nil, nil, errors.New("no members")
	}
	return members, body, nil
}

// rarRefusal keeps every decoder-level refusal in the same typed vocabulary the
// parent already handles: an archive this worker will not read, never a crash.
func rarRefusal(err error) error {
	switch {
	case errors.Is(err, rardecode.ErrArchiveEncrypted), errors.Is(err, rardecode.ErrArchivedFileEncrypted):
		return errors.New("encrypted archive")
	case errors.Is(err, rardecode.ErrMultiVolume):
		return errors.New("multi-volume archive")
	case errors.Is(err, rardecode.ErrSolidOpen):
		return errors.New("solid archive")
	default:
		return errors.New("archive refused")
	}
}
