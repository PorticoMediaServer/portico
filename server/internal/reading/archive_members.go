package reading

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ErrArchiveFormat reports an archive this server cannot read. It is returned
// before any member is opened, never as a reason to guess or relabel a format.
var ErrArchiveFormat = errors.New("reading: unsupported archive format")

// Bounded fixed-page archive reading. CBR (RAR) and CB7 (7z) reuse the ZIP
// publication's exact admission: source size, directory/raw-entry checks,
// member count and size, MaxExpanded work, canonical paths and no duplicates.
const maxArchiveDepth = 32

// The RAR decoder's dictionary ceiling is the publication's member budget; a
// larger dictionary means this file is not a fixed-page comic this server will
// decode.
const maxRARDictionary = MaxMember

type archiveMember struct {
	Name string
	Size int64
	Open func() (io.ReadCloser, error)
}

type memberArchive interface {
	Members() []archiveMember
	Close() error
}

// openMembers maps the publication's format onto the same bounded reader the
// ZIP publication already uses. RAR requires its pinned build-tagged decoder;
// CB7 requires the local-only bounded worker. Unsupported inputs are refused.
func openMembers(ctx context.Context, format string, ra io.ReaderAt, size int64) (memberArchive, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if size < 22 || size > MaxSource {
		return nil, ErrLimit
	}
	switch strings.ToLower(format) {
	case "cbz":
		// Ordinary CBZ keeps its canonical openArchive admission; this alternate
		// reader must never become a weaker ZIP path.
		return nil, archiveFormatError("cbz")
	case "cbr":
		return openClassicArchive(ctx, format, ra, size)
	case "cb7":
		members, err := cb7Members(ctx, ra, size)
		if err != nil {
			return nil, err
		}
		return &zipMembers{files: members}, nil
	default:
		return nil, ErrArchiveFormat
	}
}

type zipMembers struct{ files []archiveMember }

func (z *zipMembers) Members() []archiveMember { return z.files }
func (z *zipMembers) Close() error             { return nil }

// memberName counts every encountered header and refuses duplicate names, so a
// filtered-out member cannot hide extra work.
func memberName(name string, seen map[string]bool) bool {
	if len(name) > 1024 {
		return false
	}
	if seen[name] {
		return false
	}
	seen[name] = true
	return true
}

// safeMember keeps only a canonical relative image member inside the budget;
// the path policy is the archive package's own safePath, not a weaker copy.
func safeMember(name string, dir bool, size uint64) bool {
	if dir || size == 0 || size > MaxMember {
		return false
	}
	if !safePath(name) {
		return false
	}
	return isImageMember(name)
}

func isImageMember(name string) bool {
	lower := strings.ToLower(name)
	// Only formats the fixed-page renderer actually decodes; a member the image
	// inspector would reject is never listed as a page.
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// boundedMemberRead reads exactly the header's declared size under the request's
// cancellation and the aggregate work budget, refusing a short or oversized
// stream rather than trusting metadata alone.
func boundedMemberRead(ctx context.Context, member archiveMember, expanded *int64) ([]byte, error) {
	if member.Size <= 0 || member.Size > MaxMember || *expanded+member.Size > MaxExpanded {
		return nil, ErrLimit
	}
	body, err := member.Open()
	if err != nil {
		return nil, archiveFailure(err)
	}
	defer body.Close()
	out, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, r: body}, member.Size+1))
	if err != nil {
		return nil, archiveFailure(err)
	}
	if int64(len(out)) != member.Size {
		return nil, ErrInvalid
	}
	*expanded += member.Size
	return out, nil
}

// sortMembers orders pages with the archive package's one natural comparator.
func sortMembers(members []archiveMember) {
	sort.SliceStable(members, func(i, j int) bool { return naturalLess(members[i].Name, members[j].Name) })
}

func archiveFormatError(format string) error {
	return fmt.Errorf("%w: %s", ErrArchiveFormat, format)
}
