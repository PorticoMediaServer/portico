package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

type inventoryInfoFrame struct {
	source         LibrarySource
	policy         ScanPolicy
	asset          int64
	token          string
	size, modified int64
	kind           string
}

// inventoryInfoFrameTx is the same live admission used by the accepting scan
// writer, repeated for each staged quantum and the final accepted-pending seal.
// A directory ETag or old read transaction never substitutes for this proof.
func inventoryInfoFrameTx(ctx context.Context, tx *sql.Tx, job, object, revision string, policyRevision int64, refresh bool) (inventoryInfoFrame, error) {
	var out inventoryInfoFrame
	source, err := InventoryFenceTx(ctx, tx, job)
	if err != nil {
		return out, err
	}
	out.source = source
	policy, err := ScanPolicyTx(ctx, tx, source.LibraryID)
	if err != nil {
		return out, err
	}
	out.policy = policy
	if policy.Revision != policyRevision || !policy.Allows("probe") {
		return out, context.Canceled
	}
	q := `SELECT a.id,o.asset_id,o.size,o.modified_ns,l.kind FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id AND a.size=o.size AND a.modified_ns=o.modified_ns JOIN libraries l ON l.id=? WHERE o.id=? AND o.source_id=? AND o.root_incarnation=? AND o.revision=? AND o.state='available' AND o.retired=0`
	if refresh {
		q += ` AND o.analysis_revision=o.revision`
	}
	err = tx.QueryRowContext(ctx, q, source.LibraryID, object, source.ID, source.Incarnation, revision).Scan(&out.asset, &out.token, &out.size, &out.modified, &out.kind)
	return out, err
}

// infoInventoryFingerprint binds every immutable input of one analysis frame:
// the public facts, the technical child sets Facts deliberately hides from its
// public JSON (chapters, subtitle evidence and the selected disc occurrence)
// and each stream's own JSON, detail text and breadth. Two frames that differ in
// any of those bytes are different source inputs, so an identical retry can be a
// truthful no-op while a hidden technical change can never collapse into it.
func infoInventoryFingerprint(f assets.Facts) ([32]byte, error) {
	raw, err := json.Marshal([]any{f, f.ChapterStatus, f.Chapters, f.SubtitleInventory, f.OriginUS, f.TimingKnown, f.DiscSelection})
	if err != nil {
		return [32]byte{}, err
	}
	for _, stream := range f.Streams {
		encoded, err := json.Marshal(stream)
		if err != nil {
			return [32]byte{}, err
		}
		breadth, err := json.Marshal(stream.Detail.StreamBreadth)
		if err != nil {
			return [32]byte{}, err
		}
		raw = append(raw, encoded...)
		raw = append(raw, stream.EncodeDetail()...)
		raw = append(raw, breadth...)
	}
	return sha256.Sum256(raw), nil
}

// infoSourceFamilyMask is the changed-family set the phase-2 materializer must
// install. A parent-witness family exists only to satisfy NEW foreign keys and
// must stay outside the mask, because the header mask is the exact contract for
// which canonical families are replaced.
func infoSourceFamilyMask(families []compactcatalog.InfoSourceFamily) int64 {
	var mask int64
	for _, family := range families {
		if family.Parent {
			continue
		}
		mask |= 1 << uint(family.Number-1)
	}
	return mask
}

// infoCanonicalFactRevision is one point read of a technical fact head. Static
// table names only; no caller value enters SQL syntax.
func infoCanonicalFactRevision(ctx context.Context, tx *sql.Tx, table, token string) (int64, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM `+table+` WHERE asset_id=?`, token).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return revision, err
}

// infoStagedStreamRevision is the revision a disc media association must name:
// this input's typed NEW stream fact when it stages one, otherwise the current
// canonical point. The disc fact, the timed title rows and the stream families
// are installed by the same change, so the association always names the stream
// facts that are coherent with it after activation.
func infoStagedStreamRevision(ctx context.Context, tx *sql.Tx, families []compactcatalog.InfoSourceFamily, token string) (int64, error) {
	for _, family := range families {
		if family.Number != 1 {
			continue
		}
		row, err := family.Row(ctx, tx, 0)
		if err != nil {
			return 0, err
		}
		fact, ok := row.(compactcatalog.InfoStreamFactRow)
		if !ok {
			return 0, compactcatalog.ErrInfoInvariant
		}
		return fact.Revision, nil
	}
	return infoCanonicalFactRevision(ctx, tx, "asset_stream_facts", token)
}

// infoStagedChapterRevision is the chapter counterpart of
// infoStagedStreamRevision: typed NEW facts first, then the canonical point.
func infoStagedChapterRevision(ctx context.Context, tx *sql.Tx, families []compactcatalog.InfoSourceFamily, token string) (int64, error) {
	for _, family := range families {
		if family.Number != 4 {
			continue
		}
		row, err := family.Row(ctx, tx, 0)
		if err != nil {
			return 0, err
		}
		fact, ok := row.(compactcatalog.InfoChapterFactRow)
		if !ok {
			return 0, compactcatalog.ErrInfoInvariant
		}
		return fact.Revision, nil
	}
	return infoCanonicalFactRevision(ctx, tx, "asset_chapter_facts", token)
}

// acceptInventoryInfo is the technical source half of scan publication. Naming,
// artwork and queue ACK remain in the original accepting transaction. This
// returns a receipt rather than claiming canonical facts are already applied.
//
// A selected disc title is published through the same finite input: the asset
// stream/chapter families, the disc fact association, the complete main member
// list and one exact title's timed media all seal behind one witness and one
// family mask. No canonical disc row is written here and no holder is walked.
//
// Its fact/title witness is the canonical point row. A structure observation
// that is only accepted-pending is not yet that row, so this input fails closed
// with ErrDiscSelectionChanged until it applies; the scan retries the exact
// observation afterwards instead of publishing timed facts against a tree no
// reader can see. Reading the pending NEW structure instead would be a
// cross-input witness and is deliberately not done here.
func (s *Service) acceptInventoryInfo(ctx context.Context, job, object, revision string, policyRevision int64, f assets.Facts, refresh bool) (compactcatalog.InfoAcceptReceipt, error) {
	if f.InventoryOnly {
		return compactcatalog.InfoAcceptReceipt{}, errors.New("technical metadata was not observed")
	}
	if f.DiscAlternate {
		return compactcatalog.InfoAcceptReceipt{}, errors.New("alternate disc media is published by AcceptDiscAlternate")
	}
	discMedia := !refresh && f.DiscSelection != nil
	if refresh && f.DiscSelection != nil {
		// A local metadata refresh reuses stored probe evidence and never
		// re-reads a disc title, so it cannot speak for timed disc media.
		return compactcatalog.InfoAcceptReceipt{}, errors.New("local refresh cannot speak for disc timed media")
	}
	fingerprint, err := infoInventoryFingerprint(f)
	if err != nil {
		return compactcatalog.InfoAcceptReceipt{}, err
	}
	check := func(ctx context.Context, tx *sql.Tx) error {
		_, err := inventoryInfoFrameTx(ctx, tx, job, object, revision, policyRevision, refresh)
		return err
	}
	if discMedia {
		// The exact disc fact/title witness, the duration/chapter agreement and
		// the main owner-title choice are re-proven in every staging transaction
		// and at the final seal, from point rows only.
		check = infoDiscMediaCheck(job, object, revision, policyRevision, refresh, f, false)
	}
	return compactcatalog.PublishInfoSource(ctx, s.db, check, func(ctx context.Context, tx *sql.Tx) (compactcatalog.InfoInputSpec, []compactcatalog.InfoSourceFamily, error) {
		frame, err := inventoryInfoFrameTx(ctx, tx, job, object, revision, policyRevision, refresh)
		if err != nil {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		if !refresh {
			if (frame.kind == "music" || frame.kind == "audiobook") && f.AudioCodec == "" || frame.kind != "music" && frame.kind != "audiobook" && f.VideoCodec == "" {
				return compactcatalog.InfoInputSpec{}, nil, errors.New("media stream unsupported for library kind")
			}
			if err = compactcatalog.SetAssetTx(ctx, tx, frame.asset, map[string]any{"container": f.Container, "video_codec": f.VideoCodec, "audio_codec": f.AudioCodec, "width": f.Width, "height": f.Height, "duration": f.Duration}); err != nil {
				return compactcatalog.InfoInputSpec{}, nil, err
			}
		}
		var families []compactcatalog.InfoSourceFamily
		var streams, chapters []compactcatalog.InfoSourceFamily
		if !refresh {
			streams, err = assets.BuildInfoStreams(ctx, tx, frame.asset, frame.token, frame.size, frame.modified, f.Streams)
			if err != nil {
				return compactcatalog.InfoInputSpec{}, nil, err
			}
			families = append(families, streams...)
		}
		if !refresh || (frame.kind == "music" || frame.kind == "audiobook") && frame.policy.Allows("local_metadata") {
			chapters, err = assets.BuildInfoChapters(ctx, tx, frame.asset, frame.token, frame.size, frame.modified, f)
			if err != nil {
				return compactcatalog.InfoInputSpec{}, nil, err
			}
			families = append(families, chapters...)
		}
		if frame.policy.Allows("subtitles") {
			subs, err := subtitles.BuildInfoInput(ctx, tx, frame.asset, frame.token, frame.size, frame.modified, f.SubtitleInventory)
			if err != nil {
				return compactcatalog.InfoInputSpec{}, nil, err
			}
			families = append(families, subs...)
		}
		if discMedia {
			// The association names the exact chapter facts this observation saw,
			// so the observation must actually carry a chapter witness. The disc
			// reader always supplies one; a caller without it fails closed instead
			// of borrowing an unrelated canonical revision.
			if f.ChapterStatus != "known" && f.ChapterStatus != "partial" {
				return compactcatalog.InfoInputSpec{}, nil, disc.ErrProtocol
			}
			streamRevision, err := infoStagedStreamRevision(ctx, tx, streams, frame.token)
			if err != nil {
				return compactcatalog.InfoInputSpec{}, nil, err
			}
			chapterRevision, err := infoStagedChapterRevision(ctx, tx, chapters, frame.token)
			if err != nil {
				return compactcatalog.InfoInputSpec{}, nil, err
			}
			media, err := buildInfoDiscMediaFamilies(ctx, tx, frame, object, revision, f, streamRevision, chapterRevision, false)
			if err != nil {
				return compactcatalog.InfoInputSpec{}, nil, err
			}
			families = append(families, media...)
		} else if !refresh {
			// The retired canonical writer cleared the main association when an
			// analysis observed no disc selection. The same clear is staged as a
			// normalized family10 fact, and only while this exact frame still owns
			// a fact row with a main association to clear.
			clear, err := buildInfoDiscMediaClearFamilies(ctx, tx, frame, object, revision)
			if err != nil {
				return compactcatalog.InfoInputSpec{}, nil, err
			}
			families = append(families, clear...)
		}
		var epoch int64
		if err = tx.QueryRowContext(ctx, `SELECT info_epoch FROM catalog_assets WHERE id=? AND token=?`, frame.asset, frame.token).Scan(&epoch); err != nil {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		kind := compactcatalog.InfoWitnessInventory
		if refresh {
			kind = compactcatalog.InfoWitnessRefresh
		}
		witness := compactcatalog.InfoWitness{Kind: kind, JobID: job, SourceID: frame.source.ID, SourceGeneration: frame.source.Generation, RootIncarnation: frame.source.Incarnation, ObjectID: object, ObjectRevision: revision, PolicyRevision: policyRevision, Fingerprint: fingerprint}
		spec := compactcatalog.InfoInputSpec{AssetID: frame.asset, Token: frame.token, ExpectedEpoch: epoch, FamilyMask: infoSourceFamilyMask(families), Fingerprint: fingerprint, Witnesses: []compactcatalog.InfoWitness{witness}}
		return spec, families, nil
	})
}
