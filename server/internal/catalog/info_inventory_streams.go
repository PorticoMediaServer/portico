package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// InventoryStreamInput captures the canonical streams on which a deferred
// observation depends. Only Streams may be updated by the producer. The saved
// head and original inventory admission are re-proven throughout publication.
type InventoryStreamInput struct {
	Streams               []assets.Stream
	job, object, revision string
	policy                int64
	frame                 inventoryInfoFrame
	consent               SourceAnalysisConsent
	headRevision          int64
	headFingerprint       string
}

// InventoryStreams reads one bounded stream set and its head in one snapshot.
// It requires Basic to have applied for this exact inventory revision.
func (s *Service) InventoryStreams(ctx context.Context, job, object, revision string, policy int64) (InventoryStreamInput, error) {
	input := InventoryStreamInput{job: job, object: object, revision: revision, policy: policy}
	tx, close, err := dbwork.BeginRead(ctx, s.db)
	if err != nil {
		return input, err
	}
	defer close()
	input.frame, err = inventoryInfoFrameTx(ctx, tx, job, object, revision, policy, true)
	if err != nil {
		return input, err
	}
	f := input.frame
	input.consent, err = SourceAnalysisConsentTx(ctx, tx, f.source.ID)
	if err != nil {
		return input, err
	}
	if !input.consent.Allowed {
		return input, storage.ErrRemoteAnalysisDisabled
	}
	err = tx.QueryRowContext(ctx, `SELECT revision,fingerprint FROM asset_stream_facts WHERE asset_id=? AND size=? AND modified_ns=? AND detail_version=?`, f.token, f.size, f.modified, assets.StreamDetailVersion).Scan(&input.headRevision, &input.headFingerprint)
	if err != nil {
		return input, err
	}
	breadth, err := assets.ReadStreamBreadth(ctx, tx, f.token)
	if err != nil {
		return input, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json FROM asset_streams WHERE asset_id=? ORDER BY stream_index LIMIT 129`, f.token)
	if err != nil {
		return input, err
	}
	defer rows.Close()
	for rows.Next() {
		var stream assets.Stream
		var detail string
		if err = rows.Scan(&stream.Index, &stream.Type, &stream.Codec, &stream.Language, &stream.Title, &stream.Channels, &stream.ChannelLayout, &stream.Default, &stream.Forced, &stream.ColorTransfer, &stream.ColorPrimaries, &detail); err != nil {
			return input, err
		}
		stream.DecodeDetail(detail)
		stream.Detail.StreamBreadth = breadth[stream.Index]
		input.Streams = append(input.Streams, stream)
	}
	if err = rows.Err(); err != nil {
		return input, err
	}
	if len(input.Streams) == 0 || len(input.Streams) > 128 {
		return input, compactcatalog.ErrInfoInvariant
	}
	return input, nil
}

// AcceptInventoryStreams replaces only stream families 1/2/3, including their
// coherent head fingerprint/revision. Scalars, naming, artwork, chapters,
// subtitles and the scan queue are untouched. The caller must retain the
// accepted-pending receipt and wait for applied before acknowledging its work.
// Timed disc associations require their disc-aware publisher instead.
func (s *Service) AcceptInventoryStreams(ctx context.Context, input InventoryStreamInput) (out compactcatalog.InfoAcceptReceipt, retErr error) {
	if input.headRevision <= 0 || input.Streams == nil {
		return compactcatalog.InfoAcceptReceipt{}, compactcatalog.ErrInfoInvariant
	}
	streamFingerprint, err := infoInventoryFingerprint(assets.Facts{Streams: input.Streams})
	if err != nil {
		return compactcatalog.InfoAcceptReceipt{}, err
	}
	raw, err := json.Marshal([]any{"inventory-streams:1", input.headRevision, input.headFingerprint, streamFingerprint})
	if err != nil {
		return compactcatalog.InfoAcceptReceipt{}, err
	}
	fingerprint := sha256.Sum256(raw)
	f := input.frame
	witness := compactcatalog.InfoWitness{Kind: compactcatalog.InfoWitnessRefresh, JobID: input.job, SourceID: f.source.ID, SourceGeneration: f.source.Generation, RootIncarnation: f.source.Incarnation, ObjectID: input.object, ObjectRevision: input.revision, PolicyRevision: input.policy, Fingerprint: fingerprint}
	// PublishInfoSource owns the handle after Begin, but an initial retry check
	// can refuse before Begin runs. Re-prove this immutable producer's exact
	// prefix on terminal loss so it cannot strand an accepted queued successor.
	defer func() {
		if !(compactcatalog.InfoInputNeedsAbort(retErr) || errors.Is(retErr, context.Canceled) || errors.Is(retErr, context.DeadlineExceeded) || errors.Is(retErr, sql.ErrNoRows)) {
			return
		}
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := dbwork.WithWriteTxContext(clean, s.db, dbwork.ClassBackgroundMedia, func(c context.Context, tx *sql.Tx) error {
			_, err := compactcatalog.AbandonInfoSourceTx(c, tx, f.asset, f.token, witness, fingerprint)
			return err
		}); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()
	check := func(ctx context.Context, tx *sql.Tx) error {
		f, err := inventoryInfoFrameTx(ctx, tx, input.job, input.object, input.revision, input.policy, true)
		if err != nil {
			return err
		}
		old := input.frame
		if f.asset != old.asset || f.token != old.token || f.size != old.size || f.modified != old.modified || f.source.ID != old.source.ID || f.source.Generation != old.source.Generation || f.source.Incarnation != old.source.Incarnation {
			return compactcatalog.ErrInfoInputDrift
		}
		if err = CheckSourceAnalysisConsentTx(ctx, tx, input.consent); err != nil {
			// A changed consent cannot resume this immutable observation. Mark
			// it terminal so PublishInfoSource reclaims its unaccepted prefix.
			return errors.Join(context.Canceled, err)
		}
		var current bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM asset_stream_facts WHERE asset_id=? AND revision=? AND fingerprint=? AND size=? AND modified_ns=? AND detail_version=?)`, f.token, input.headRevision, input.headFingerprint, f.size, f.modified, assets.StreamDetailVersion).Scan(&current)
		if err != nil {
			return err
		}
		if !current {
			return compactcatalog.ErrInfoInputDrift
		}
		// A pending stream replacement must not be overwritten by an observation
		// of the older canonical head. Unrelated-family inputs may precede us.
		var conflict, owned, disc bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_info_changes WHERE asset_id=?1 AND asset_token=?2 AND phase<5 AND family_mask&7<>0 AND fingerprint<>?3),EXISTS(SELECT 1 FROM catalog_info_changes WHERE asset_id=?1 AND asset_token=?2 AND phase=0 AND fingerprint=?3),EXISTS(SELECT 1 FROM inventory_disc_facts WHERE object_id=?4 AND media_title_key<>'')`, f.asset, f.token, fingerprint[:], input.object).Scan(&conflict, &owned, &disc)
		if err != nil {
			return err
		}
		if disc || conflict && owned {
			return compactcatalog.ErrInfoInputDrift
		}
		if conflict {
			return compactcatalog.ErrInfoInputBusy
		}
		return nil
	}
	return compactcatalog.PublishInfoSource(ctx, s.db, check, func(ctx context.Context, tx *sql.Tx) (compactcatalog.InfoInputSpec, []compactcatalog.InfoSourceFamily, error) {
		f := input.frame
		families, err := assets.BuildInfoStreams(ctx, tx, f.asset, f.token, f.size, f.modified, input.Streams)
		if err != nil {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		var epoch int64
		if err = tx.QueryRowContext(ctx, `SELECT info_epoch FROM catalog_assets WHERE id=? AND token=?`, f.asset, f.token).Scan(&epoch); err != nil {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		return compactcatalog.InfoInputSpec{AssetID: f.asset, Token: f.token, ExpectedEpoch: epoch, FamilyMask: infoSourceFamilyMask(families), Fingerprint: fingerprint, Witnesses: []compactcatalog.InfoWitness{witness}}, families, nil
	})
}
