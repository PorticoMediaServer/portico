package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// AcceptInventoryDiscProbe is the receipt-bearing bounded structure producer.
// Validation, ordering and pure prefix construction run outside the writer;
// stable title identities are looked up one point at a time in row quanta.
func (s *Service) AcceptInventoryDiscProbe(ctx context.Context, job, object, revision string, policyRevision int64, probe disc.Probe) (compactcatalog.InfoAcceptReceipt, error) {
	raw, err := json.Marshal(probe)
	if err != nil {
		return compactcatalog.InfoAcceptReceipt{}, err
	}
	validated, err := disc.DecodeProbe(raw)
	if err != nil && !errors.Is(err, disc.ErrEncrypted) {
		return compactcatalog.InfoAcceptReceipt{}, err
	}
	digest := sha256.Sum256(append([]byte("disc_structure_v1\x00"), raw...))
	fingerprint := hex.EncodeToString(digest[:])
	titles := validated.OrderedTitles()
	var prefix [3][]int64
	for which := range prefix {
		prefix[which] = make([]int64, len(titles)+1)
		for i, title := range titles {
			n := len(title.Clips)
			if which == 1 {
				n = len(title.Streams)
			} else if which == 2 {
				n = len(title.Chapters)
			}
			prefix[which][i+1] = prefix[which][i] + int64(n)
		}
	}
	locate := func(which int, ordinal int64) (disc.Title, int64) {
		at := sort.Search(len(titles), func(i int) bool { return prefix[which][i+1] > ordinal })
		return titles[at], ordinal - prefix[which][at]
	}
	check := func(ctx context.Context, tx *sql.Tx) error {
		frame, err := inventoryInfoFrameTx(ctx, tx, job, object, revision, policyRevision, false)
		if err != nil {
			return err
		}
		if frame.source.Kind != "local" {
			return disc.ErrRemoteUnqualified
		}
		return nil
	}
	return compactcatalog.PublishInfoSource(ctx, s.db, check, func(ctx context.Context, tx *sql.Tx) (compactcatalog.InfoInputSpec, []compactcatalog.InfoSourceFamily, error) {
		frame, err := inventoryInfoFrameTx(ctx, tx, job, object, revision, policyRevision, false)
		if err != nil {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		var old compactcatalog.InfoDiscFactRow
		err = tx.QueryRowContext(ctx, `SELECT asset_id,source_revision,source_generation,root_incarnation,fingerprint,owner_title_key,choice_revision,media_title_key,media_stream_revision,media_chapter_revision FROM inventory_disc_facts WHERE object_id=?`, object).Scan(&old.AssetID, &old.SourceRevision, &old.SourceGeneration, &old.RootIncarnation, &old.Fingerprint, &old.OwnerTitleKey, &old.ChoiceRevision, &old.MediaTitleKey, &old.MediaStreamRevision, &old.MediaChapterRevision)
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		// A real observation may now belong to a replacement asset. Its exact
		// fresh probe is a new structure; the materializer releases any prior
		// accepted owner before replacing the shared object tree.
		replacement := exists && old.AssetID != frame.token
		same := exists && !replacement && old.SourceRevision == revision && old.SourceGeneration == frame.source.Generation && old.RootIncarnation == frame.source.Incarnation && old.Fingerprint == fingerprint
		var epoch int64
		if err = tx.QueryRowContext(ctx, `SELECT info_epoch FROM catalog_assets WHERE id=? AND token=?`, frame.asset, frame.token).Scan(&epoch); err != nil {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		spec := compactcatalog.InfoInputSpec{AssetID: frame.asset, Token: frame.token, ExpectedEpoch: epoch, Fingerprint: digest, Witnesses: []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessDisc, JobID: job, SourceID: frame.source.ID, SourceGeneration: frame.source.Generation, RootIncarnation: frame.source.Incarnation, ObjectID: object, ObjectRevision: revision, PolicyRevision: policyRevision, Fingerprint: digest}}}
		if same {
			return spec, nil, nil
		}
		choice := int64(1)
		if exists && !replacement {
			choice = old.ChoiceRevision
			if old.SourceRevision != revision || old.Fingerprint != fingerprint {
				choice++
			}
		}
		fact := compactcatalog.InfoDiscFactRow{AssetID: frame.token, ObjectID: object, SourceRevision: revision, SourceGeneration: frame.source.Generation, RootIncarnation: frame.source.Incarnation, Kind: validated.Kind, Packaging: validated.Packaging, Encryption: validated.Encryption, Fingerprint: fingerprint, ChoiceRevision: choice}
		families := []compactcatalog.InfoSourceFamily{{Number: 10, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := fact
			row.InputOrdinal = i
			return row, nil
		}}}
		// A source revision-only reaffirmation retains the unchanged title tree.
		changedTree := !exists || replacement || old.Fingerprint != fingerprint || old.RootIncarnation != frame.source.Incarnation
		if changedTree {
			rotate := replacement || (exists && old.RootIncarnation != frame.source.Incarnation)
			families = append(families,
				compactcatalog.InfoSourceFamily{Number: 11, Count: int64(len(titles)), Row: func(ctx context.Context, tx *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
					title := titles[i]
					key := title.Key(validated.Kind)
					var token string
					// A sealed predecessor's NEW identity also survives while its canonical
					// title row has not yet materialized. This is at most two header probes.
					err := tx.QueryRowContext(ctx, `SELECT n.selection_token FROM catalog_info_changes c JOIN catalog_info_new_disc_titles n ON n.change_id=c.id JOIN catalog_info_new_disc_facts f ON f.change_id=c.id AND f.object_id=n.object_id WHERE c.asset_id=? AND c.asset_token=? AND c.family_mask&1024<>0 AND n.object_id=? AND n.title_key=? AND f.root_incarnation=? ORDER BY c.slot DESC LIMIT 1`, frame.asset, frame.token, object, key, frame.source.Incarnation).Scan(&token)
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return nil, err
					}
					if token == "" && !rotate {
						err = tx.QueryRowContext(ctx, `SELECT selection_token FROM inventory_disc_titles WHERE object_id=? AND title_key=?`, object, key).Scan(&token)
						if err != nil && !errors.Is(err, sql.ErrNoRows) {
							return nil, err
						}
					}
					if token == "" {
						token = identity.Token()
					}
					var playlist *int64
					if title.Playlist != nil {
						v := int64(*title.Playlist)
						playlist = &v
					}
					return compactcatalog.InfoDiscTitleRow{InputOrdinal: i, ObjectID: object, TitleKey: key, SelectionToken: token, TitleNumber: i + 1, DiscOrder: int64(title.DiscOrder), DurationMS: title.DurationMs, AngleCount: int64(title.AngleCount), Playlist: playlist, DVDTitle: int64(title.Title), VTS: int64(title.VTS), PGC: int64(title.PGC)}, nil
				}},
				compactcatalog.InfoSourceFamily{Number: 12, Count: prefix[0][len(titles)], Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
					title, j := locate(0, i)
					clip := title.Clips[j]
					return compactcatalog.InfoDiscClipRow{InputOrdinal: i, ObjectID: object, TitleKey: title.Key(validated.Kind), Sequence: int64(clip.Sequence), File: clip.File, InTime90k: clip.InTime90k, OutTime90k: clip.OutTime90k}, nil
				}},
				compactcatalog.InfoSourceFamily{Number: 13, Count: prefix[1][len(titles)], Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
					title, j := locate(1, i)
					stream := title.Streams[j]
					boolean := func(v bool) int64 {
						if v {
							return 1
						}
						return 0
					}
					var base *int64
					if stream.DolbyVisionBasePID != nil {
						v := int64(*stream.DolbyVisionBasePID)
						base = &v
					}
					return compactcatalog.InfoDiscStreamRow{InputOrdinal: i, ObjectID: object, TitleKey: title.Key(validated.Kind), StreamOrder: j, ClipSequence: int64(stream.ClipSequence), PID: int64(stream.PID), Kind: stream.Kind, Coding: int64(stream.Coding), Language: stream.Language, DVEnhancement: boolean(stream.DolbyVisionEnhancementLayer), DVEnhancementKnown: boolean(stream.DolbyVisionEnhancementKnown), DVBasePID: base, VideoRole: stream.VideoRole}, nil
				}},
				compactcatalog.InfoSourceFamily{Number: 14, Count: prefix[2][len(titles)], Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
					title, j := locate(2, i)
					return compactcatalog.InfoDiscChapterRow{InputOrdinal: i, ObjectID: object, TitleKey: title.Key(validated.Kind), ChapterOrder: j, StartMS: title.Chapters[j].StartMs}, nil
				}},
			)
			// A new structure/root invalidates old timed-title proof. Retire those
			// incompatible source children explicitly in raw quanta, not a FK cascade.
			for _, number := range []int{15, 16, 17, 18} {
				families = append(families, compactcatalog.InfoSourceFamily{Number: number, Count: 0})
			}
		}
		for _, family := range families {
			spec.FamilyMask |= 1 << uint(family.Number-1)
		}
		return spec, families, nil
	})
}
