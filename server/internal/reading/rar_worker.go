//go:build portico_rar7z

package reading

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/PorticoMediaServer/portico/server/internal/archive7z"
)

// rarWorkerMembers lists a compressed or solid CBR's admitted members through
// the bounded worker. The parent never runs the RAR4 LZ/PPM or solid decoders:
// a worker refusal or kill is the unsupported-format error, while a cancelled
// request keeps its own cancellation.
func rarWorkerMembers(ctx context.Context, ra io.ReaderAt, size int64) ([]archiveMember, error) {
	body, err := runCB7Helper(ctx, ra, size, "rar-list")
	if err != nil {
		return nil, rarWorkerFailure(ctx)
	}
	var reply cb7ListReply
	if err = json.Unmarshal(body, &reply); err != nil {
		return nil, archiveFormatError("cbr")
	}
	if reply.Error != "" || len(reply.Members) == 0 || len(reply.Members) > MaxEntries {
		return nil, archiveFormatError("cbr")
	}
	out := make([]archiveMember, 0, len(reply.Members))
	seen := map[string]bool{}
	var expanded int64
	for _, member := range reply.Members {
		metadata := member.Name == "ComicInfo.xml"
		if !safePath(member.Name) || member.Size <= 0 ||
			(metadata && member.Size > archive7z.MaxComicInfo) ||
			(!metadata && (!isImageMember(member.Name) || member.Size > MaxMember)) {
			return nil, archiveFormatError("cbr")
		}
		// The parent repeats the identity and whole-publication budget checks:
		// a duplicate name is not two pages, and admitted members must fit
		// MaxExpanded together.
		if seen[member.Name] || int64(MaxExpanded)-expanded < member.Size {
			return nil, archiveFormatError("cbr")
		}
		seen[member.Name] = true
		expanded += member.Size
		name, memberSize := member.Name, member.Size
		out = append(out, archiveMember{Name: name, Size: memberSize, Open: func() (io.ReadCloser, error) {
			body, err := rarWorkerMemberBytes(ctx, ra, size, name)
			if err != nil {
				return nil, err
			}
			return io.NopCloser(bytes.NewReader(body)), nil
		}})
	}
	return out, nil
}

// rarWorkerMemberBytes reads one page or the bounded root ComicInfo.xml through
// the worker under the same deadline.
func rarWorkerMemberBytes(ctx context.Context, ra io.ReaderAt, size int64, name string) ([]byte, error) {
	metadata := name == "ComicInfo.xml"
	if !safePath(name) || (!metadata && !isImageMember(name)) {
		return nil, ErrInvalid
	}
	body, err := runCB7Helper(ctx, ra, size, "rar-page", name)
	if err != nil {
		return nil, rarWorkerFailure(ctx)
	}
	limit := int64(MaxMember)
	if metadata {
		limit = archive7z.MaxComicInfo
	}
	if len(body) == 0 || int64(len(body)) > limit {
		return nil, archiveFormatError("cbr")
	}
	if !metadata {
		if _, err = inspectImage(body); err != nil {
			return nil, archiveFormatError("cbr")
		}
	}
	return body, nil
}

// rarWorkerFailure keeps the request's own cancellation and turns every other
// worker outcome — refusal, crash or its own deadline kill — into the clean
// unsupported-format answer.
func rarWorkerFailure(ctx context.Context) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return archiveFormatError("cbr")
}
