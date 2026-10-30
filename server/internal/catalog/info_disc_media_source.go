package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
)

// This file is the bounded source-input builder for one exact selected title's
// timed media. It replaces the canonical writes of the retired
// persistDiscMediaSelectionTx/persistDiscTitleMediaTx/CommitDiscAlternate path
// with typed staged families 10..18 that the source publication pipeline
// installs in raw quanta.
//
// It is source-only: no canonical row is written here, nothing is derived from
// a query result, and the complete technical arrays are never flattened into a
// second in-memory row graph. Every family row factory reads ordinal i from the
// immutable observed Facts captured by the caller, exactly like the structure
// builder in info_disc_source.go.

// discTitleMediaRevision is the pure accepted-title-media digest. It reproduces
// the exact bytes hashed by persistDiscTitleMediaTx (header JSON array first,
// then each stream's JSON, its EncodeDetail text and its breadth JSON), so a
// value produced here equals the legacy canonical revision for identical facts.
// Keeping one helper means a rewritten canonical writer and this source builder
// cannot disagree about whether timed facts changed.
func discTitleMediaRevision(revision, fingerprint string, f assets.Facts) (string, error) {
	if f.DiscSelection == nil || len(f.Streams) == 0 || len(f.Streams) > 128 {
		return "", disc.ErrProtocol
	}
	raw, err := json.Marshal([]any{revision, fingerprint, f.Container, f.VideoCodec, f.AudioCodec, f.Width, f.Height, f.Duration, f.OriginUS, f.TimingKnown, f.DiscSelection.Members})
	if err != nil {
		return "", disc.ErrProtocol
	}
	seen := make(map[int]bool, len(f.Streams))
	for _, st := range f.Streams {
		if st.Index < 0 || seen[st.Index] || (st.Type != "video" && st.Type != "audio" && st.Type != "subtitle") {
			return "", disc.ErrProtocol
		}
		seen[st.Index] = true
		encoded, err := json.Marshal(st)
		if err != nil {
			return "", disc.ErrProtocol
		}
		breadth, err := json.Marshal(st.Detail.StreamBreadth)
		if err != nil {
			return "", disc.ErrProtocol
		}
		raw = append(raw, encoded...)
		raw = append(raw, st.EncodeDetail()...)
		raw = append(raw, breadth...)
	}
	if len(raw) > disc.MaxOutputBytes {
		return "", disc.ErrProtocol
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// infoDiscMediaFact is the exact canonical inventory_disc_facts point row this
// input's children are allowed to speak for. It is read once per builder call
// and re-read by the per-quantum check; no set scan or holder map is involved.
type infoDiscMediaFact struct {
	assetID              string
	sourceRevision       string
	sourceGeneration     int64
	rootIncarnation      string
	kind                 string
	packaging            string
	encryption           string
	fingerprint          string
	ownerTitleKey        string
	choiceRevision       int64
	mediaTitleKey        string
	mediaStreamRevision  int64
	mediaChapterRevision int64
}

// infoDiscMediaFactTx reads one object's disc fact. A missing row is a changed
// observation, not a new fact: structure must already be published.
func infoDiscMediaFactTx(ctx context.Context, tx *sql.Tx, object string) (infoDiscMediaFact, error) {
	var out infoDiscMediaFact
	err := tx.QueryRowContext(ctx, `SELECT asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,owner_title_key,choice_revision,media_title_key,media_stream_revision,media_chapter_revision FROM inventory_disc_facts WHERE object_id=?`, object).
		Scan(&out.assetID, &out.sourceRevision, &out.sourceGeneration, &out.rootIncarnation, &out.kind, &out.packaging, &out.encryption, &out.fingerprint, &out.ownerTitleKey, &out.choiceRevision, &out.mediaTitleKey, &out.mediaStreamRevision, &out.mediaChapterRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrDiscSelectionChanged
	}
	return out, err
}

// admits is the exact fact fence the retired canonical writer used: same asset,
// same source revision/generation/incarnation, same structure fingerprint and
// unencrypted, for a main write and an alternate write alike.
//
// The owner-title clause is main-only, exactly as the canonical writers split
// it. The main timed-media input speaks for no title but the owner's (or any
// title while no owner is chosen): another owner's choice is never overwritten
// by an analysis write. An alternate publication writes only that title's timed
// facts and never touches the main association, and it lets a job request
// another title even after the owner picked a different main; its authorization
// is the persisted inventory_disc_title_requests row proven by
// infoDiscMediaRequestTx, not the owner choice. Applying the owner clause here
// would reject exactly the alternate probe the canonical writer accepts.
func (f infoDiscMediaFact) admits(frame inventoryInfoFrame, revision string, sel *assets.DiscMediaSelection, alternate bool) bool {
	if sel == nil || sel.TitleKey == "" {
		return false
	}
	if f.assetID != frame.token ||
		f.sourceRevision != revision ||
		f.sourceGeneration != frame.source.Generation ||
		f.rootIncarnation != frame.source.Incarnation ||
		f.fingerprint != sel.StructureFingerprint ||
		f.encryption != "none" {
		return false
	}
	return alternate || f.ownerTitleKey == "" || f.ownerTitleKey == sel.TitleKey
}

// infoDiscTitleWitness is the canonical structure row for one selected title,
// plus the chapter endpoint. Chapters are validated from the highest stored
// order only: the structure producer writes consecutive 0..N-1 orders in one
// sealed input, so an endpoint is proof of the count without COUNT(*).
type infoDiscTitleWitness struct {
	titleKey       string
	selectionToken string
	titleNumber    int64
	discOrder      int64
	durationMS     int64
	angleCount     int64
	playlist       *int64
	dvdTitle       int64
	vts            int64
	pgc            int64
	chapters       int64
}

func infoDiscTitleWitnessTx(ctx context.Context, tx *sql.Tx, object, key string) (infoDiscTitleWitness, error) {
	var out infoDiscTitleWitness
	err := tx.QueryRowContext(ctx, `SELECT title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist,dvd_title,vts,pgc FROM inventory_disc_titles WHERE object_id=? AND title_key=?`, object, key).
		Scan(&out.titleKey, &out.selectionToken, &out.titleNumber, &out.discOrder, &out.durationMS, &out.angleCount, &out.playlist, &out.dvdTitle, &out.vts, &out.pgc)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrDiscSelectionChanged
	}
	if err != nil {
		return out, err
	}
	if out.selectionToken == "" || out.titleNumber < 1 || out.titleNumber > disc.MaxTitles {
		return out, disc.ErrProtocol
	}
	var last int64
	err = tx.QueryRowContext(ctx, `SELECT chapter_order FROM inventory_disc_chapters WHERE object_id=? AND title_key=? ORDER BY chapter_order DESC LIMIT 1`, object, key).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if last < 0 {
		return out, disc.ErrProtocol
	}
	out.chapters = last + 1
	return out, nil
}

// admit compares the canonical title with the timed facts the producer
// observed: exact duration (a protocol fault, as the canonical writer treated a
// duration mismatch) and exact chapter count (a changed observation, because
// the title's structure moved under the probe).
func (w infoDiscTitleWitness) admit(f assets.Facts) error {
	if w.durationMS != int64(math.Round(f.Duration*1000)) {
		return disc.ErrProtocol
	}
	if w.chapters != int64(len(f.Chapters)) {
		return ErrDiscSelectionChanged
	}
	return nil
}

// infoDiscTitleMediaRevisionTx is a single canonical point probe. An empty
// string means the selected title currently has no materialized timed facts.
func infoDiscTitleMediaRevisionTx(ctx context.Context, tx *sql.Tx, object, key string) (string, error) {
	var revision string
	err := tx.QueryRowContext(ctx, `SELECT revision FROM inventory_disc_title_media WHERE object_id=? AND title_key=?`, object, key).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return revision, err
}

// infoDiscMediaRequestTx copies the alternate-title request fence from
// CommitDiscAlternate. job is the requesting job when the caller knows it; an
// empty job omits only that predicate, so the builder can prove the request
// identity without inventing a job. The accepting scan must pass its job
// through checkInfoDiscMediaRequestTx in every check.
func infoDiscMediaRequestTx(ctx context.Context, tx *sql.Tx, frame inventoryInfoFrame, job, object, revision string, sel *assets.DiscMediaSelection) error {
	// One statement, one optional leading predicate: the job clause is spliced
	// in so both forms stay byte-identical apart from the requesting job. The
	// EXISTS(...) wrapper is closed at the end; an unbalanced statement fails as
	// an SQL syntax error rather than as a changed selection.
	requestJob := ""
	args := []any{object, sel.TitleKey, revision, sel.StructureFingerprint, frame.source.ID}
	if job != "" {
		requestJob = `r.job_id=? AND `
		args = append([]any{job}, args...)
	}
	q := `SELECT EXISTS(SELECT 1 FROM inventory_disc_title_requests r JOIN inventory_disc_facts d ON d.object_id=r.object_id JOIN inventory_objects o ON o.id=d.object_id JOIN library_sources s ON s.id=o.source_id WHERE ` + requestJob + `r.object_id=? AND r.title_key=? AND r.source_revision=? AND r.fingerprint=? AND d.source_revision=r.source_revision AND d.fingerprint=r.fingerprint AND d.encryption='none' AND o.source_id=? AND o.revision=d.source_revision AND o.retired=0 AND o.state='available' AND d.source_generation=s.generation AND d.root_incarnation=s.incarnation AND o.root_incarnation=s.incarnation)`
	var valid bool
	if err := tx.QueryRowContext(ctx, q, args...).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrDiscSelectionChanged
	}
	return nil
}

// infoDiscMediaFenceTx re-proves everything this input's disc families describe
// and returns the exact canonical rows they were built from. It is point-only:
// one fact row, one title row, one chapter endpoint and, for an alternate, one
// request existence probe. The fact fence carries the owner-title clause only
// for a main write; an alternate is authorized by its request row, exactly like
// CommitDiscAlternate against persistDiscMediaSelectionTx.
func infoDiscMediaFenceTx(ctx context.Context, tx *sql.Tx, frame inventoryInfoFrame, object, revision string, f assets.Facts, alternate bool) (infoDiscMediaFact, infoDiscTitleWitness, error) {
	var fact infoDiscMediaFact
	var title infoDiscTitleWitness
	sel := f.DiscSelection
	if sel == nil || sel.TitleKey == "" {
		return fact, title, disc.ErrProtocol
	}
	if frame.source.Kind != "local" {
		return fact, title, disc.ErrRemoteUnqualified
	}
	var err error
	if fact, err = infoDiscMediaFactTx(ctx, tx, object); err != nil {
		return fact, title, err
	}
	if !fact.admits(frame, revision, sel, alternate) {
		return fact, title, ErrDiscSelectionChanged
	}
	if title, err = infoDiscTitleWitnessTx(ctx, tx, object, sel.TitleKey); err != nil {
		return fact, title, err
	}
	if err = title.admit(f); err != nil {
		return fact, title, err
	}
	if alternate {
		if err = infoDiscMediaRequestTx(ctx, tx, frame, "", object, revision, sel); err != nil {
			return fact, title, err
		}
	}
	return fact, title, nil
}

// checkInfoDiscMediaTx is the per-quantum check for a disc media input. The
// accepting caller runs inventoryInfoFrameTx first (live source/job/policy/
// object frame) and then this helper in every staging transaction and at the
// final acceptance seal, so a fact, title or request that moves under the
// builder cannot be installed. It does no file or network IO.
func checkInfoDiscMediaTx(ctx context.Context, tx *sql.Tx, frame inventoryInfoFrame, object, revision string, f assets.Facts, alternate bool) error {
	_, _, err := infoDiscMediaFenceTx(ctx, tx, frame, object, revision, f, alternate)
	return err
}

// checkInfoDiscMediaRequestTx is the exact alternate-title request fence,
// including the requesting job. An alternate analysis must repeat it in every
// check because buildInfoDiscMediaFamilies does not receive a job id.
func checkInfoDiscMediaRequestTx(ctx context.Context, tx *sql.Tx, frame inventoryInfoFrame, job, object, revision string, f assets.Facts) error {
	if f.DiscSelection == nil || f.DiscSelection.TitleKey == "" || job == "" {
		return disc.ErrProtocol
	}
	return infoDiscMediaRequestTx(ctx, tx, frame, job, object, revision, f.DiscSelection)
}

// infoDiscMediaCheck is the exact per-quantum admission for a disc media input:
// the live source/job/policy/object frame, then the disc fact, title and (for
// an alternate) the original title request including its job. The accepting
// caller passes it straight to compactcatalog.PublishInfoSource, so it is
// repeated in every staging transaction and at the final acceptance seal.
func infoDiscMediaCheck(job, object, revision string, policyRevision int64, refresh bool, f assets.Facts, alternate bool) compactcatalog.InfoSourceCheck {
	return func(ctx context.Context, tx *sql.Tx) error {
		frame, err := inventoryInfoFrameTx(ctx, tx, job, object, revision, policyRevision, refresh)
		if err != nil {
			return err
		}
		if err = checkInfoDiscMediaTx(ctx, tx, frame, object, revision, f, alternate); err != nil {
			return err
		}
		if alternate {
			return checkInfoDiscMediaRequestTx(ctx, tx, frame, job, object, revision, f)
		}
		return nil
	}
}

// AcceptDiscAlternate is the receipt-bearing bounded producer for one requested
// alternate title. It replaces the retired CommitDiscAlternate canonical write:
// the same exact request/fact/title fences run in every staging transaction, but
// the title's timed media is staged as typed families 16..18 and installed only
// by the accepted source publication. It never touches the main association, the
// member list or another title, and it never fans out per-holder resources.
func (s *Service) AcceptDiscAlternate(ctx context.Context, job, object, revision string, policyRevision int64, f assets.Facts) (compactcatalog.InfoAcceptReceipt, error) {
	if f.DiscSelection == nil || !f.DiscAlternate {
		return compactcatalog.InfoAcceptReceipt{}, disc.ErrProtocol
	}
	fingerprint, err := infoInventoryFingerprint(f)
	if err != nil {
		return compactcatalog.InfoAcceptReceipt{}, err
	}
	return compactcatalog.PublishInfoSource(ctx, s.db, infoDiscMediaCheck(job, object, revision, policyRevision, false, f, true), func(ctx context.Context, tx *sql.Tx) (compactcatalog.InfoInputSpec, []compactcatalog.InfoSourceFamily, error) {
		frame, err := inventoryInfoFrameTx(ctx, tx, job, object, revision, policyRevision, false)
		if err != nil {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		families, err := buildInfoDiscMediaFamilies(ctx, tx, frame, object, revision, f, 0, 0, true)
		if err != nil {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		var epoch int64
		if err = tx.QueryRowContext(ctx, `SELECT info_epoch FROM catalog_assets WHERE id=? AND token=?`, frame.asset, frame.token).Scan(&epoch); err != nil {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		witness := compactcatalog.InfoWitness{Kind: compactcatalog.InfoWitnessDisc, JobID: job, SourceID: frame.source.ID, SourceGeneration: frame.source.Generation, RootIncarnation: frame.source.Incarnation, ObjectID: object, ObjectRevision: revision, PolicyRevision: policyRevision, Fingerprint: fingerprint}
		spec := compactcatalog.InfoInputSpec{AssetID: frame.asset, Token: frame.token, ExpectedEpoch: epoch, FamilyMask: infoSourceFamilyMask(families), Fingerprint: fingerprint, Witnesses: []compactcatalog.InfoWitness{witness}}
		return spec, families, nil
	})
}

// buildInfoDiscMediaClearFamilies stages the main-association clear the retired
// canonical writer performed when an analysis observed no disc selection. It
// speaks only for a canonical fact row that still belongs to this exact
// asset/source frame and still names a main title; otherwise it stages nothing.
// Members and timed title rows are retained exactly as that writer retained
// them.
func buildInfoDiscMediaClearFamilies(ctx context.Context, tx *sql.Tx, frame inventoryInfoFrame, object, revision string) ([]compactcatalog.InfoSourceFamily, error) {
	fact, err := infoDiscMediaFactTx(ctx, tx, object)
	if errors.Is(err, ErrDiscSelectionChanged) {
		// No canonical structure row for this object: there is no association.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if fact.assetID != frame.token || fact.sourceRevision != revision || fact.sourceGeneration != frame.source.Generation || fact.rootIncarnation != frame.source.Incarnation {
		// A different observation owns the canonical fact; its own publication
		// replaces the association, so this frame must not speak for it.
		return nil, nil
	}
	if fact.mediaTitleKey == "" && fact.mediaStreamRevision == 0 && fact.mediaChapterRevision == 0 {
		return nil, nil
	}
	row := compactcatalog.InfoDiscFactRow{
		InputOrdinal:         0,
		ObjectID:             object,
		AssetID:              fact.assetID,
		SourceRevision:       fact.sourceRevision,
		SourceGeneration:     fact.sourceGeneration,
		RootIncarnation:      fact.rootIncarnation,
		Kind:                 fact.kind,
		Packaging:            fact.packaging,
		Encryption:           fact.encryption,
		Fingerprint:          fact.fingerprint,
		OwnerTitleKey:        fact.ownerTitleKey,
		ChoiceRevision:       fact.choiceRevision,
		MediaTitleKey:        "",
		MediaStreamRevision:  0,
		MediaChapterRevision: 0,
	}
	return []compactcatalog.InfoSourceFamily{{Number: 10, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
		if i != 0 {
			return nil, compactcatalog.ErrInfoInvariant
		}
		staged := row
		return staged, nil
	}}}, nil
}

// buildInfoDiscMediaFamilies stages one exact selected title's timed media as
// typed normalized source families. streamRevision and chapterRevision are the
// NEW asset fact revisions the caller's same input is installing (or the current
// canonical revisions when unchanged); they are written into the disc fact,
// never read back from a child set. No canonical row is written here and no
// holder is walked: the accepted source publication installs these families.
//
//   - main (alternate=false) may change family10 media_title_key/
//     media_stream_revision/media_chapter_revision and family15's complete
//     current-main member list, and stages family16..18 only when the accepted
//     timed digest differs from the canonical row.
//   - alternate stages family16..18 for that title only. It never changes
//     family10 media fields, family15 or any other global scalar, and requires
//     the original title request row. The requested title may differ from the
//     owner's main choice, as CommitDiscAlternate allows.
//
// An unchanged selection with an unchanged digest returns nil families: the
// truthful no-op, not a rewrite of identical rows.
//
// Families 10 and 11 are always present because the staged foreign keys need
// the canonical fact/title witness rows; only family10 can be a changed family
// (Parent=false) and only in the main path. Family11 keeps the canonical
// selection token, title number, disc order and playlist exactly as stored.
func buildInfoDiscMediaFamilies(ctx context.Context, tx *sql.Tx, frame inventoryInfoFrame, object, revision string, f assets.Facts, streamRevision, chapterRevision int64, alternate bool) ([]compactcatalog.InfoSourceFamily, error) {
	sel := f.DiscSelection
	if sel == nil || sel.TitleKey == "" || len(f.Streams) == 0 || len(f.Streams) > 128 || len(sel.Members) > disc.MaxConsumedMembers {
		return nil, disc.ErrProtocol
	}
	for _, member := range sel.Members {
		if member.Path == "" || member.ObjectIdentity == "" {
			return nil, disc.ErrProtocol
		}
	}
	// A main selection names the exact technical revisions that make it ready.
	// Zero is the fact table's "no association" value and would silently
	// unpublish the title, so it is a producer fault.
	if !alternate && (streamRevision <= 0 || chapterRevision <= 0) {
		return nil, disc.ErrProtocol
	}
	mediaRevision, err := discTitleMediaRevision(revision, sel.StructureFingerprint, f)
	if err != nil {
		return nil, err
	}
	fact, title, err := infoDiscMediaFenceTx(ctx, tx, frame, object, revision, f, alternate)
	if err != nil {
		return nil, err
	}
	current, err := infoDiscTitleMediaRevisionTx(ctx, tx, object, sel.TitleKey)
	if err != nil {
		return nil, err
	}
	timedChanged := current != mediaRevision
	selectedChanged := !alternate && (fact.mediaTitleKey != sel.TitleKey ||
		fact.mediaStreamRevision != streamRevision ||
		fact.mediaChapterRevision != chapterRevision)
	if !timedChanged && !selectedChanged {
		return nil, nil
	}

	families := make([]compactcatalog.InfoSourceFamily, 0, 6)
	// Family10: the canonical disc fact. A changed row carries the structure
	// facts it already had plus this selection's media association; otherwise
	// the identical row is only an FK parent witness.
	factRow := compactcatalog.InfoDiscFactRow{
		ObjectID:             object,
		AssetID:              fact.assetID,
		SourceRevision:       fact.sourceRevision,
		SourceGeneration:     fact.sourceGeneration,
		RootIncarnation:      fact.rootIncarnation,
		Kind:                 fact.kind,
		Packaging:            fact.packaging,
		Encryption:           fact.encryption,
		Fingerprint:          fact.fingerprint,
		OwnerTitleKey:        fact.ownerTitleKey,
		ChoiceRevision:       fact.choiceRevision,
		MediaTitleKey:        fact.mediaTitleKey,
		MediaStreamRevision:  fact.mediaStreamRevision,
		MediaChapterRevision: fact.mediaChapterRevision,
	}
	if selectedChanged {
		factRow.MediaTitleKey = sel.TitleKey
		factRow.MediaStreamRevision = streamRevision
		factRow.MediaChapterRevision = chapterRevision
	}
	families = append(families, compactcatalog.InfoSourceFamily{Number: 10, Count: 1, Parent: !selectedChanged, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
		if i != 0 {
			return nil, compactcatalog.ErrInfoInvariant
		}
		row := factRow
		row.InputOrdinal = 0
		return row, nil
	}})
	// Family11: the exact canonical title this input speaks for.
	titleRow := compactcatalog.InfoDiscTitleRow{
		ObjectID:       object,
		TitleKey:       title.titleKey,
		SelectionToken: title.selectionToken,
		TitleNumber:    title.titleNumber,
		DiscOrder:      title.discOrder,
		DurationMS:     title.durationMS,
		AngleCount:     title.angleCount,
		Playlist:       title.playlist,
		DVDTitle:       title.dvdTitle,
		VTS:            title.vts,
		PGC:            title.pgc,
	}
	families = append(families, compactcatalog.InfoSourceFamily{Number: 11, Count: 1, Parent: true, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
		if i != 0 {
			return nil, compactcatalog.ErrInfoInvariant
		}
		row := titleRow
		row.InputOrdinal = 0
		return row, nil
	}})

	if !timedChanged {
		// Only the main-title association moved. The accepted timed facts are
		// already current, so families 16..18 are omitted entirely; the member
		// list follows the accepted main selection and is restaged as one
		// complete set.
		if selectedChanged {
			families = append(families, infoDiscMediaMemberFamilies(object, sel)...)
		}
		return families, nil
	}

	if !alternate {
		families = append(families, infoDiscMediaMemberFamilies(object, sel)...)
	}
	durationMS := title.durationMS
	boolean := func(v bool) int64 {
		if v {
			return 1
		}
		return 0
	}
	families = append(families,
		compactcatalog.InfoSourceFamily{Number: 16, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			if i != 0 {
				return nil, compactcatalog.ErrInfoInvariant
			}
			return compactcatalog.InfoDiscTitleMediaRow{
				InputOrdinal:   0,
				ObjectID:       object,
				TitleKey:       sel.TitleKey,
				SourceRevision: revision,
				Fingerprint:    sel.StructureFingerprint,
				Revision:       mediaRevision,
				Container:      f.Container,
				VideoCodec:     f.VideoCodec,
				AudioCodec:     f.AudioCodec,
				Width:          int64(f.Width),
				Height:         int64(f.Height),
				DurationMS:     durationMS,
				OriginUS:       f.OriginUS,
				TimingKnown:    boolean(f.TimingKnown),
			}, nil
		}},
		compactcatalog.InfoSourceFamily{Number: 17, Count: int64(len(f.Streams)), Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			if i < 0 || i >= int64(len(f.Streams)) {
				return nil, compactcatalog.ErrInfoInvariant
			}
			st := f.Streams[i]
			breadth, err := json.Marshal(st.Detail.StreamBreadth)
			if err != nil {
				return nil, disc.ErrProtocol
			}
			return compactcatalog.InfoDiscTitleMediaStreamRow{
				InputOrdinal:   i,
				ObjectID:       object,
				TitleKey:       sel.TitleKey,
				StreamIndex:    int64(st.Index),
				Type:           st.Type,
				Codec:          st.Codec,
				Language:       st.Language,
				Title:          st.Title,
				Channels:       int64(st.Channels),
				ChannelLayout:  st.ChannelLayout,
				IsDefault:      boolean(st.Default),
				IsForced:       boolean(st.Forced),
				ColorTransfer:  st.ColorTransfer,
				ColorPrimaries: st.ColorPrimaries,
				DetailJSON:     st.EncodeDetail(),
				BreadthJSON:    string(breadth),
			}, nil
		}},
		compactcatalog.InfoSourceFamily{Number: 18, Count: int64(len(sel.Members)), Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			if i < 0 || i >= int64(len(sel.Members)) {
				return nil, compactcatalog.ErrInfoInvariant
			}
			member := sel.Members[i]
			return compactcatalog.InfoDiscTitleMediaMemberRow{
				InputOrdinal:   i,
				ObjectID:       object,
				TitleKey:       sel.TitleKey,
				Path:           member.Path,
				ObjectIdentity: member.ObjectIdentity,
				Size:           member.Size,
				ModifiedNS:     member.ModifiedNS,
				Required:       boolean(member.Required),
				Directory:      boolean(member.Directory),
			}, nil
		}},
	)
	return families, nil
}

// infoDiscMediaMemberFamilies is family15: the complete current-main consumed
// member list for this object. It is a whole-set family, so it is staged only
// when this input speaks for the main selection's members.
func infoDiscMediaMemberFamilies(object string, sel *assets.DiscMediaSelection) []compactcatalog.InfoSourceFamily {
	boolean := func(v bool) int64 {
		if v {
			return 1
		}
		return 0
	}
	return []compactcatalog.InfoSourceFamily{{Number: 15, Count: int64(len(sel.Members)), Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
		if i < 0 || i >= int64(len(sel.Members)) {
			return nil, compactcatalog.ErrInfoInvariant
		}
		member := sel.Members[i]
		return compactcatalog.InfoDiscMediaMemberRow{
			InputOrdinal:   i,
			ObjectID:       object,
			Path:           member.Path,
			ObjectIdentity: member.ObjectIdentity,
			Size:           member.Size,
			ModifiedNS:     member.ModifiedNS,
			Required:       boolean(member.Required),
			Directory:      boolean(member.Directory),
		}, nil
	}}}
}
