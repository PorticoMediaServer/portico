package subtitles

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// ImportRequest refers only to server-inventoried evidence, never an arbitrary path.
type ImportRequest struct {
	OperationID      string `json:"operationId"`
	SourceID         string `json:"sourceId"`
	DiscoveryID      string `json:"discoveryId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Scope            string `json:"scope"`
	// Render chooses how a styled text track (ASS, SSA) is kept: "text" (the
	// default) keeps the words, their timing and top placement, which every
	// player draws at no cost; "styled" keeps the script as authored, which can
	// only be shown by burning it into a converted picture. Bitmap tracks are
	// always burned in and ignore this.
	Render string `json:"render,omitempty"`
}

func (s *Service) Import(ctx context.Context, p identity.Principal, item string, m ImportRequest) (Receipt, error) {
	if !validID(m.OperationID) || !validID(m.SourceID) || !validID(m.DiscoveryID) || m.ExpectedRevision < 1 || (m.Scope != "personal" && m.Scope != "shared") || !validRender(m.Render) {
		return Receipt{}, ErrInput
	}

	digest := requestDigest([]any{"import", item, m})
	gated, p, e := s.tx(ctx, p, item)
	if e != nil {
		return Receipt{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	old, e := receiptTx(ctx, tx, p, m.OperationID, digest, item)
	if e != nil {
		return Receipt{}, e
	}
	if old != nil {
		return *old, nil
	}
	if m.Scope == "shared" && !owner(p) && ctx.Value(lazyCacheKey{}) == nil {
		return Receipt{}, identity.ErrUnauthorized
	}
	catalog, e := s.ListTx(ctx, tx, p, item, m.SourceID)
	if e != nil {
		return Receipt{}, e
	}
	var track *Discovery
	for i := range catalog.Discovered {
		if catalog.Discovered[i].ID == m.DiscoveryID {
			track = &catalog.Discovered[i]
			break
		}
	}
	if track == nil || track.Revision != m.ExpectedRevision {
		return Receipt{}, ErrConflict
	}
	if !track.Enabled {
		return Receipt{}, ErrUnsupported
	}
	src, e := sourceQuery(ctx, tx, item, m.SourceID)
	if e != nil {
		return Receipt{}, e
	}
	var expectedEvidence string
	e = tx.QueryRowContext(ctx, `SELECT evidence FROM subtitle_inventory_evidence WHERE asset_id=? AND revision=?`, m.SourceID, m.ExpectedRevision).Scan(&expectedEvidence)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return Receipt{}, e
	}
	if e = gated.Commit(); e != nil {
		return Receipt{}, e
	}
	if s.storage.Guard != nil {
		if e = s.storage.Guard(src.path); e != nil {
			return Receipt{}, ErrUnavailable
		}
	}
	var canonical []byte
	var evidence string
	original := ""
	if track.Origin == "embedded" {
		if s.Extractor == nil {
			return Receipt{}, ErrUnavailable
		}
		canonical, evidence, e = s.Extractor.Extract(ctx, item, m.SourceID, track.index, track.Format)
		if e != nil {
			return Receipt{}, e
		}
		if expectedEvidence != "" && evidence != expectedEvidence {
			return Receipt{}, ErrConflict
		}
		original = digestBytes(canonical)
	} else {
		var remoteData, remoteCompanion []byte
		handled := false
		if remote, ok := s.SourceInputs.(SidecarInputs); ok {
			remoteData, remoteCompanion, handled, e = remote.AcquireSubtitleSidecar(ctx, item, m.SourceID, track.locator, track.size, track.modified, track.Format)
			if e != nil {
				return Receipt{}, e
			}
		}
		var raw sidecarBytes
		if handled {
			raw = sidecarBytes{Data: remoteData, Companion: remoteCompanion}
		} else {
			req := sourceRead{Root: src.root, Path: track.locator, Size: track.size, Modified: track.modified, Origin: "sidecar", Format: track.Format, Digest: track.digest}
			if req.Path, e = relative(req.Root, req.Path); e != nil {
				return Receipt{}, e
			}
			payload, _ := json.Marshal(req)
			readCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			cmd := exec.Command(s.helper, "--portico-subtitle-helper")
			cmd.Stdin = bytes.NewReader(payload)
			var payloadBytes []byte
			e = s.storage.Supervisor.Run(readCtx, "subtitle:"+src.ID, cmd, func(r io.Reader) error {
				var e error
				payloadBytes, e = io.ReadAll(io.LimitReader(r, int64(AssetLimitBytes())+1))
				if len(payloadBytes) > AssetLimitBytes() {
					return ErrCapacity
				}
				return e
			})
			if e != nil {
				return Receipt{}, ErrUnavailable
			}
			if json.Unmarshal(payloadBytes, &raw) != nil {
				return Receipt{}, ErrInput
			}
		}
		if SidecarFingerprint(raw.Data, raw.Companion) != track.digest {
			return Receipt{}, ErrConflict
		}
		canonical, e = CanonicalAssetWithLanguage(raw.Data, raw.Companion, track.Format, int64(src.duration*1e6), track.Language)
		if e != nil {
			return Receipt{}, e
		}
		original = SidecarFingerprint(raw.Data, raw.Companion)
	}
	originalAsset := canonical
	canonical = renderChoice(canonical, m.Render, int64(src.duration*1e6))
	format := track.Format
	mutation := Mutation{OperationID: m.OperationID, SourceID: m.SourceID, Scope: m.Scope, Format: format, Language: track.Language, Title: track.Title, Rights: "Imported from the viewer's authorized media source", OffsetUS: "0"}
	pub := publication{originalAsset: originalAsset, source: src, origin: track.Origin, originalDigest: original, evidence: evidence, hearingImpaired: track.HearingImpaired, defaultTrack: track.Default, forcedTrack: track.Forced, discovery: track.ID, discoveryRevision: track.Revision}
	if cache, ok := ctx.Value(lazyCacheKey{}).(lazyCacheTarget); ok {
		if cache.discovery != track.ID || cache.source != src.ID {
			return Receipt{}, ErrInput
		}
		pub.cacheResource, pub.cacheRevision = cache.resource, cache.revision
	}
	return s.publish(ctx, p, item, mutation, digest, pub, canonical)
}

type sourceRead struct {
	Root, Path, Origin, FFmpeg, Digest, Format string
	Size, Modified                             int64
	Stream                                     int
}

// Helper runs only in the existing supervisor's bounded child process. OpenRoot
// confines parent symlink traversal, while descriptor evidence fences final-file
// replacement. A stalled filesystem/decoder retains the supervisor's permit.
func Helper(in io.Reader, out io.Writer) error {
	var r sourceRead
	decoder := json.NewDecoder(io.LimitReader(in, 16385))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&r); e != nil {
		return ErrInput
	}
	if !filepath.IsAbs(r.Root) || filepath.IsAbs(r.Path) || r.Path == "" || r.Size < 0 {
		return ErrInput
	}
	root, e := os.OpenRoot(r.Root)
	if e != nil {
		return e
	}
	defer root.Close()
	before, e := root.Lstat(r.Path)
	if e != nil || !before.Mode().IsRegular() {
		return ErrConflict
	}
	file, e := root.Open(r.Path)
	if e != nil {
		return e
	}
	defer file.Close()
	check := func() error {
		now, e := file.Stat()
		if e != nil {
			return e
		}
		named, e := root.Lstat(r.Path)
		if e != nil {
			return e
		}
		if !os.SameFile(before, now) || !os.SameFile(now, named) || now.Size() != r.Size || now.ModTime().UnixNano() != r.Modified || !named.Mode().IsRegular() {
			return ErrConflict
		}
		return nil
	}
	if e = check(); e != nil {
		return e
	}
	if r.Origin != "sidecar" {
		return ErrUnsupported
	}
	raw, e := io.ReadAll(io.LimitReader(file, int64(BinaryLimitBytes())+1))
	if e != nil {
		return e
	}
	if len(raw) > BinaryLimitBytes() {
		return ErrCapacity
	}
	var companion []byte
	if r.Format == "idx" || r.Format == "vobsub" {
		name := strings.TrimSuffix(r.Path, filepath.Ext(r.Path)) + ".sub"
		info, e := root.Lstat(name)
		if e != nil || !info.Mode().IsRegular() || info.Size() > int64(BinaryLimitBytes()) {
			return ErrInput
		}
		paired, e := root.Open(name)
		if e != nil {
			return e
		}
		defer paired.Close()
		opened, e := paired.Stat()
		if e != nil || !os.SameFile(info, opened) {
			return ErrConflict
		}
		companion, e = io.ReadAll(io.LimitReader(paired, int64(BinaryLimitBytes())+1))
		if e != nil {
			return e
		}
		after, e := paired.Stat()
		if e != nil {
			return e
		}
		named, e := root.Lstat(name)
		if e != nil || !os.SameFile(info, after) || !os.SameFile(after, named) || after.Size() != info.Size() || after.ModTime() != info.ModTime() {
			return ErrConflict
		}
	}
	if SidecarFingerprint(raw, companion) != r.Digest {
		return ErrConflict
	}
	if e = check(); e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(sidecarBytes{raw, companion})
}

type sidecarBytes struct{ Data, Companion []byte }

func SidecarFingerprint(raw, companion []byte) string {
	if len(companion) == 0 {
		return digestBytes(raw)
	}
	return requestDigest([]any{digestBytes(raw), digestBytes(companion)})
}
