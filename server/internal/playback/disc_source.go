package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func discPlanInputTx(ctx context.Context, tx *sql.Tx, input *DeliveryInput, ref *catalog.DiscTitleRef) error {
	var known bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_disc_facts WHERE asset_id=?)`, input.SourceID).Scan(&known); err != nil {
		return err
	}
	if !known {
		if ref != nil {
			return ErrStaleOffer
		}
		return nil
	}
	d, err := catalog.ReadDiscSelection(ctx, tx, input.SourceID)
	if err != nil {
		return err
	}
	if d == nil {
		return ErrIncompatible
	}
	if ref == nil {
		if err = d.Ready(ctx, tx); err != nil {
			return err
		}
	} else {
		_, facts, _, e := catalog.ResolveDiscTitleTx(ctx, tx, d, *ref)
		if e != nil {
			return e
		}
		source := discSourceFacts(d.Asset, facts)
		input.Source = &source
		input.Container, input.VideoCodec, input.AudioCodec, input.Duration, input.Height = facts.Container, facts.VideoCodec, facts.AudioCodec, facts.Duration, facts.Height
	}
	input.Disc = true
	// An ISO contains several titles; dividing its size by one title's duration
	// is not that title's bitrate. Keep only actual selected-stream evidence.
	if input.Source != nil {
		rate := int64(0)
		known := true
		if input.Source.Video != nil {
			rate = input.Source.Video.BitRate
			known = rate > 0
		}
		for _, a := range input.Source.Audio {
			rate += a.BitRate
			known = known && a.BitRate > 0
		}
		input.Source.BitRate = 0
		if known {
			input.Source.BitRate = rate
		}
	}
	return nil
}
func pinDiscSessionTx(ctx context.Context, tx *sql.Tx, id, asset string, ref *catalog.DiscTitleRef) error {
	d, err := catalog.ReadDiscSelection(ctx, tx, asset)
	if err != nil {
		return err
	}
	if d == nil {
		if ref != nil {
			return ErrStaleOffer
		}
		return nil
	}
	key, streamRevision, chapterRevision, factsRevision := d.MediaKey, d.StreamRevision, d.ChapterRevision, ""
	if ref == nil {
		if err = d.Ready(ctx, tx); err != nil {
			return err
		}
	} else {
		title, _, revision, e := catalog.ResolveDiscTitleTx(ctx, tx, d, *ref)
		if e != nil {
			return e
		}
		key = title.Key(d.Structure.Kind)
		factsRevision = revision
		streamRevision, chapterRevision = 0, 0
	}
	if factsRevision == "" {
		_, factsRevision, err = catalog.ReadDiscTitleMedia(ctx, tx, d, key)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO playback_disc_pins(session_id,object_id,title_key,source_revision,fingerprint,stream_revision,chapter_revision,title_facts_revision) VALUES(?,?,?,?,?,?,?,?)`, id, d.Object, key, d.Revision, d.Fingerprint, streamRevision, chapterRevision, factsRevision)
	return err
}

// The selection is checked alongside the occurrence's existing physical-source
// pin. A later scan may not silently change a running title or its track indices.
func checkDiscSessionTx(ctx context.Context, tx *sql.Tx, id string) error {
	if _, _, private, err := privateDiscBindingTx(ctx, tx, id); err != nil {
		return err
	} else if private {
		// Only the service's immutable snapshot/ordinary authority composition
		// can check a private presentation. The local helper cannot qualify it.
		return disc.ErrRemoteUnqualified
	}
	if err := disc.CheckPlaybackSelectionTx(ctx, tx, id); err != nil {
		if errors.Is(err, disc.ErrProtocol) {
			return identity.ErrUnauthorized
		}
		return err
	}
	return nil
}

// PresentationDiscTitleTx captures the actual selected title for a presentation
// being accepted. The caller owns actor/occurrence admission in this same Tx.
func (s *Service) PresentationDiscTitleTx(ctx context.Context, tx *sql.Tx, id string, generation int) (*catalog.DiscTitleRef, error) {
	if key, snapshotID, private, err := privateDiscBindingTx(ctx, tx, id); err != nil {
		return nil, err
	} else if private {
		if key.Generation != generation {
			return nil, ErrStaleOffer
		}
		if borrow, ok := ctx.Value(discPlayBorrowKey{}).(*discPlayBorrow); ok {
			return borrow.pendingTitleTx(ctx, tx, s, key, snapshotID)
		}
		snapshot, _, err := s.activePrivateDiscSnapshotTx(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		return measuredDiscTitleRef(snapshot)
	}
	var asset string
	if err := tx.QueryRowContext(ctx, `SELECT asset_id FROM playback_sessions WHERE id=? AND generation=? AND state NOT IN('stopped','ended','failed')`, id, generation).Scan(&asset); err != nil {
		return nil, err
	}
	var key, revision string
	err := tx.QueryRowContext(ctx, `SELECT title_key,title_facts_revision FROM playback_disc_pins WHERE session_id=?`, id).Scan(&key, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = checkDiscSessionTx(ctx, tx, id); err != nil {
		return nil, err
	}
	return catalog.PinnedDiscTitleRefTx(ctx, tx, asset, key, revision)
}

func (h *HLS) openDiscInput(ctx context.Context, id string) (*assets.PlaybackInput, bool, error) {
	if input, handled, err := h.openPrivateDiscInput(ctx, id); handled {
		return input, true, err
	}
	tx, done, err := dbwork.BeginRead(ctx, h.db)
	if err != nil {
		return nil, true, err
	}
	var asset, titleKey, factsRevision string
	err = tx.QueryRowContext(ctx, `SELECT ps.asset_id,p.title_key,p.title_facts_revision FROM playback_sessions ps JOIN playback_disc_pins p ON p.session_id=ps.id WHERE ps.id=?`, id).Scan(&asset, &titleKey, &factsRevision)
	if errors.Is(err, sql.ErrNoRows) {
		done()
		return nil, false, nil
	}
	if err != nil {
		done()
		return nil, true, err
	}
	if err = checkDiscSessionTx(ctx, tx, id); err != nil {
		done()
		return nil, true, err
	}
	d, err := catalog.ReadDiscSelection(ctx, tx, asset)
	if err != nil || d == nil {
		done()
		return nil, true, ErrIncompatible
	}
	title, ok := d.MainTitle()
	members := []storage.DiscMember{}
	if factsRevision != "" {
		facts, revision, e := catalog.ReadDiscTitleMedia(ctx, tx, d, titleKey)
		if e != nil || revision != factsRevision {
			done()
			return nil, true, ErrStaleOffer
		}
		members = facts.DiscSelection.Members
		ok = false
		for _, candidate := range d.Structure.Titles {
			if candidate.Key(d.Structure.Kind) == titleKey {
				title = candidate
				ok = true
				break
			}
		}
	}
	if !ok {
		done()
		return nil, true, ErrStaleOffer
	}

	var rows *sql.Rows
	if factsRevision == "" {
		rows, err = tx.QueryContext(ctx, `SELECT path,object_identity,size,modified_ns,required,directory FROM inventory_disc_media_members WHERE object_id=? ORDER BY path LIMIT ?`, d.Object, disc.MaxConsumedMembers+1)
		if err != nil {
			done()
			return nil, true, err
		}
		for rows.Next() {
			var m storage.DiscMember
			if err = rows.Scan(&m.Path, &m.ObjectIdentity, &m.Size, &m.ModifiedNS, &m.Required, &m.Directory); err != nil {
				break
			}
			members = append(members, m)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
	}
	done()
	if err != nil {
		return nil, true, err
	}
	directory := d.Structure.Packaging == "folder"
	if h.SourceStorage == nil || len(members) > disc.MaxConsumedMembers || directory && len(members) == 0 {
		return nil, true, ErrIncompatible
	}
	req := storage.InventoryRequest{Root: d.Root, RootIdentity: d.RootIdentity, RelativePath: d.Relative, FollowSymlinks: d.FollowSymlinks}
	path := filepath.Join(d.Root, d.Relative)
	var pinnedFile *os.File
	validate := func(check context.Context) error {
		current, release, e := dbwork.BeginRead(check, h.db)
		if e != nil {
			return e
		}
		// A start may prepare its selected subtitle before publishing its V1
		// presentation. Only the sealed, exact-source selection invocation may
		// continue that still-live private preparation; ordinary playback cannot.
		if invocation, ok := subtitles.DiscExtractionInvocationFromContext(ctx); ok {
			if invocation.Facts().SessionID != id {
				e = identity.ErrUnauthorized
			} else {
				e = invocation.CheckTx(check, current)
				if e == nil {
					e = checkSessionOccurrenceModeTx(check, current, id, true)
				}
			}
		} else {
			e = checkSessionOccurrenceTx(check, current, id)
		}
		release()
		if e != nil {
			return e
		}
		if h.SourceStorage.Guard != nil {
			if e = h.SourceStorage.Guard(path); e != nil {
				return e
			}
		}
		if pinnedFile != nil && !directory {
			if e = h.SourceStorage.ValidatePlaybackDescriptor(check, pinnedFile, d.Size, d.ModifiedNS); e != nil {
				return e
			}
		}
		if directory {
			required, optional, dirs := []string{}, []string{}, []string{}
			// Include absent optional members too. Their later appearance changes the
			// accepted read set and must not pass by omission from the stored witness.
			consumed, e := disc.ConsumedMembers(d.Structure)
			if e != nil {
				return e
			}
			for _, m := range consumed {
				switch {
				case m.Directory:
					dirs = append(dirs, m.Path)
				case m.Required:
					required = append(required, m.Path)
				default:
					optional = append(optional, m.Path)
				}
			}
			actual, e := h.SourceStorage.DiscMemberManifest(check, d.Source, req, required, optional, dirs)
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(actual, members) {
				return ErrStaleOffer
			}
		}
		return nil
	}
	readCtx := ctx
	if err = validate(ctx); err != nil {
		return nil, true, err
	}
	file, err := h.SourceStorage.OpenDiscPlaybackDescriptor(readCtx, req, d.ObjectIdentity, d.Size, d.ModifiedNS, directory)
	if err != nil {
		return nil, true, err
	}
	pinnedFile = file
	input := &assets.PlaybackInput{File: file, Inherit: true, Validate: validate}
	failed := func(e error) (*assets.PlaybackInput, bool, error) { input.Close(); return nil, true, e }
	tool := disc.Tool{Binary: filepath.Join(filepath.Dir(h.binary), "portico-disc")}
	actual, err := tool.ProbeDescriptor(ctx, file, directory)
	if err != nil {
		return failed(err)
	}
	if !reflect.DeepEqual(actual, d.Structure) {
		return failed(ErrStaleOffer)
	}
	if !ok || title.Key(d.Structure.Kind) != titleKey {
		return failed(ErrStaleOffer)
	}
	input.InputAt = func(check context.Context, ms int64) ([]string, string, int64, error) {
		if e := validate(check); e != nil {
			return nil, "", 0, e
		}
		var seek *disc.Seek
		if ms > 0 && d.Structure.Kind == "bluray" {
			value, e := tool.SeekDescriptor(check, file, directory, *title.Playlist, ms)
			if e != nil {
				return nil, "", 0, e
			}
			seek = &value
		}
		in, e := d.Structure.FFmpegInput(disc.DescriptorPath(directory), title, ms, seek)
		if e != nil {
			return nil, "", 0, e
		}
		if e = validate(check); e != nil {
			return nil, "", 0, e
		}
		return in.Options, in.URL, in.TimestampOffsetMs, nil
	}
	input.Options, input.Argument, _, err = input.InputAt(ctx, 0)
	if err != nil {
		return failed(err)
	}
	if directory {
		input.MountFD = 3
		input.MountPath = "/disc"
	}
	// FFmetadata is an inherited pipe protocol input. It is an unlinked private
	// file descriptor, never a sidecar path in the source library.
	metadata, err := title.ChapterMetadata()
	if err != nil {
		return failed(err)
	}
	dir := h.outputDir(id)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return failed(err)
	}
	chapter, err := os.CreateTemp(dir, "disc-chapters-")
	if err != nil {
		return failed(err)
	}
	os.Remove(chapter.Name())
	if _, err = chapter.Write(metadata); err != nil {
		chapter.Close()
		return failed(err)
	}
	if _, err = chapter.Seek(0, 0); err != nil {
		chapter.Close()
		return failed(err)
	}
	input.Extra = []*os.File{chapter}
	if err = validate(ctx); err != nil {
		return failed(err)
	}
	return input, true, nil
}

// Disc cut lists belong to the exact selected presentation. The ordinary
// asset/size/mtime cache cannot distinguish two titles in one unchanged ISO.
func (h *HLS) discTimeline(ctx context.Context, id string) (*copyTimeline, bool) {
	var raw string
	if err := dbwork.QueryRow(ctx, h.db, `SELECT timeline FROM playback_disc_pins WHERE session_id=?`, id).Scan(&raw); err != nil {
		return nil, false
	}
	var t copyTimeline
	if raw == "" || len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &t) != nil || t.count() == 0 {
		return nil, true
	}
	return &t, true
}
func (h *HLS) storeDiscTimeline(ctx context.Context, id string, t *copyTimeline) {
	raw, err := json.Marshal(t)
	if err != nil || len(raw) > 1<<20 {
		return
	}
	_, _ = dbwork.ExecWrite(ctx, h.db, dbwork.ClassEstablishedPlayback, `UPDATE playback_disc_pins SET timeline=? WHERE session_id=?`, string(raw), id)
}

func discSourceFacts(asset string, f assets.Facts) DeliverySource {
	out := DeliverySource{ID: asset, Container: f.Container, Duration: f.Duration, DetailKnown: true}
	for _, st := range f.Streams {
		if observed := st.Detail.ObservedInterlaced(); observed != nil {
			st.Detail.Interlaced = *observed
		}
		if st.Type == "video" && out.Video == nil {
			out.Video = &SourceVideo{Index: st.Index, Codec: videoFamily(st.Codec), StreamDetail: st.Detail}
			if out.Video.DynamicRange == "" {
				out.Video.DynamicRange = assets.DynamicRangeOf(st.ColorTransfer, 0)
			}
		}
		if st.Type == "audio" {
			variant := st.Detail.AudioVariant
			if variant == "" {
				variant = assets.AudioVariant(st.Codec, st.Detail.Profile, st.Detail.ObjectAudio)
			}
			st.Detail.AudioVariant = variant
			out.Audio = append(out.Audio, SourceAudio{Lossless: knownAudioEncodingLossless(st.Codec, st.Detail.Profile, variant), Index: st.Index, Ordinal: len(out.Audio), Codec: audioFamily(st.Codec), NativeCodec: st.Codec, Title: publicAudioText(st.Title, 96), Channels: st.Channels, Layout: st.ChannelLayout, Language: st.Language, Default: st.Default, Commentary: st.Commentary, AudioDescription: st.Detail.AudioDescription, StreamDetail: st.Detail})
		}
	}
	known := true
	if out.Video != nil {
		out.BitRate = out.Video.BitRate
		out.MaxBitRate = out.Video.MaxBitRate
		known = out.Video.BitRate > 0
	}
	for _, a := range out.Audio {
		out.BitRate += a.BitRate
		known = known && a.BitRate > 0
		if out.MaxBitRate > 0 {
			out.MaxBitRate += max(a.BitRate, a.MaxBitRate)
		}
	}
	if !known {
		out.BitRate = 0
	}

	return out
}
func loadLocalSessionDeliverySource(ctx context.Context, tx *sql.Tx, session, asset string) (DeliverySource, error) {
	if source, ok, err := remoteSessionSourceTx(ctx, tx, session, asset); err != nil || ok {
		return source, err
	}
	if _, _, private, err := privateDiscBindingTx(ctx, tx, session); err != nil {
		return DeliverySource{}, err
	} else if private {
		return DeliverySource{}, disc.ErrRemoteUnqualified
	}
	var key, revision string
	err := tx.QueryRowContext(ctx, `SELECT title_key,title_facts_revision FROM playback_disc_pins WHERE session_id=?`, session).Scan(&key, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return loadDeliverySource(ctx, tx, asset)
	}
	if err != nil {
		return DeliverySource{}, err
	}
	if err = checkDiscSessionTx(ctx, tx, session); err != nil {
		return DeliverySource{}, err
	}
	if revision == "" {
		return loadDeliverySource(ctx, tx, asset)
	}
	d, err := catalog.ReadDiscSelection(ctx, tx, asset)
	if err != nil || d == nil {
		return DeliverySource{}, ErrStaleOffer
	}
	f, current, err := catalog.ReadDiscTitleMedia(ctx, tx, d, key)
	if err != nil {
		return DeliverySource{}, err
	}
	if current != revision {
		return DeliverySource{}, ErrStaleOffer
	}
	return discSourceFacts(asset, f), nil
}

func hasDiscSessionTx(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var found bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_pins WHERE session_id=?) OR EXISTS(SELECT 1 FROM playback_disc_snapshots WHERE session_id=?)`, id, id).Scan(&found)
	return found, err
}
