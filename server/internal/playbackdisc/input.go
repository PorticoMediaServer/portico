package playbackdisc

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
)

func sameReceipt(a, b ownedcapture.Receipt) bool {
	return a.ID == b.ID && a.ArtifactID == b.ArtifactID && a.Binding == b.Binding && a.Object == b.Object && a.Version == b.Version && a.ExpiresAt.Equal(b.ExpiresAt)
}
func (s *Store) captureTx(ctx context.Context, tx *sql.Tx, id, version string) (ownedcapture.Receipt, error) {
	if s.captures == nil {
		return ownedcapture.Receipt{}, ErrCurrent
	}
	var b ownedcapture.Binding
	e := tx.QueryRowContext(ctx, `SELECT s.source_id,s.config_generation,s.object_id,s.object_revision,s.asset_id,s.acquisition_id,s.reservation_id,s.policy_witness,s.authority_witness FROM owned_capture_sources s JOIN owned_capture_receipts r ON r.capture_id=s.capture_id WHERE s.capture_id=? AND r.version_id=?`, id, version).Scan(&b.SourceID, &b.ConfigGeneration, &b.ObjectID, &b.ObjectRevision, &b.AssetID, &b.AcquisitionID, &b.ReservationID, &b.PolicyWitness, &b.AuthorityWitness)
	if e != nil {
		return ownedcapture.Receipt{}, e
	}
	// The capture service reconstructs its own version and all immutable receipt
	// values and repeats the original producer's source/actor proof in this Tx.
	return s.captures.ResolveTx(ctx, tx, id, b)
}
func (s *Store) checkCaptureTx(ctx context.Context, tx *sql.Tx, v Snapshot) error {
	if v.Input.Captured == nil {
		return nil
	}
	expected := v.Input.Captured.Receipt
	r, e := s.captureTx(ctx, tx, expected.ID, expected.Version.ID())
	if e != nil {
		return e
	}
	if !sameReceipt(r, expected) {
		return ErrCurrent
	}
	// A current receipt owned by another viewer is still the wrong acquisition.
	// Do not infer actor equality from matching bytes, version or asset.
	var same bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_source_capture_actor a JOIN remote_source_capture_physical p ON p.capture_id=a.capture_id JOIN remote_source_capture_policy rp ON rp.capture_id=a.capture_id
 JOIN playback_sessions ps ON ps.id=? AND ps.generation=? JOIN catalog_entities item ON item.id=ps.item_id
 WHERE a.item_id=pid(item.public_id) AND a.capture_id=? AND a.server_id=? AND a.authority=? AND a.account_id=? AND a.profile_id=? AND a.family_id=? AND a.device_id=? AND a.grant=? AND a.epoch=? AND a.asset_id=? AND a.authority_witness=?
 AND p.source_id=? AND p.config_generation=? AND p.library_source_id=? AND p.library_id=? AND p.incarnation=? AND p.library_generation=? AND p.root_identity=? AND p.inventory_object_id=? AND p.inventory_revision=? AND p.source_generation=? AND p.size=?
 AND rp.profile_revision=? AND rp.policy_witness=?)`, v.SessionID, v.Generation, r.ID, v.Actor.ServerID, v.Actor.Authority, v.Actor.AccountID, v.Actor.ProfileID, v.Actor.FamilyID, v.Actor.DeviceID, v.Actor.Grant, v.Actor.Epoch, v.Source.AssetID, v.Source.ReadAuthorityWitness,
		v.Source.RemoteSourceID, v.Source.EffectiveConfiguration, v.Source.LibrarySourceID, v.Source.LibraryID, v.Source.Incarnation, v.Source.LibraryGeneration, v.Source.RootIdentity, v.Source.InventoryObjectID, v.Source.InventoryRevision, v.Source.RawConfigurationGeneration, v.Source.Size, v.Source.ProfileRevision, v.Source.ReadPolicyWitness).Scan(&same)
	if e != nil {
		return e
	}
	if !same {
		return ErrCurrent
	}
	return nil
}
func writeInput(ctx context.Context, tx *sql.Tx, v Snapshot) error {
	if c := v.Input.Conditional; c != nil {
		e := c.Version.Evidence()
		_, err := tx.ExecContext(ctx, `INSERT INTO playback_disc_conditional_inputs(snapshot_id,kind,scope,object_id,revision,size,version_id) VALUES(?,?,?,?,?,?,?)`, v.ID, string(e.Kind), e.Scope, e.Object, e.Revision, e.Size, c.Version.ID())
		return err
	}
	r := v.Input.Captured.Receipt
	_, e := tx.ExecContext(ctx, `INSERT INTO playback_disc_capture_inputs(snapshot_id,capture_id,accepted_version_id) VALUES(?,?,?)`, v.ID, r.ID, r.Version.ID())
	return e
}
func (s *Store) readInput(ctx context.Context, tx *sql.Tx, id, kind string) (Input, error) {
	if kind == "conditional" {
		var e mediasource.Evidence
		var version string
		err := tx.QueryRowContext(ctx, `SELECT kind,scope,object_id,revision,size,version_id FROM playback_disc_conditional_inputs WHERE snapshot_id=?`, id).Scan(&e.Kind, &e.Scope, &e.Object, &e.Revision, &e.Size, &version)
		if err != nil {
			return Input{}, err
		}
		v, err := mediasource.NewVersion(e)
		if err != nil || v.ID() != version {
			return Input{}, ErrCurrent
		}
		return Input{Conditional: &Conditional{Version: v}}, nil
	}
	if kind != "captured" {
		return Input{}, ErrInput
	}
	var capture, version string
	if err := tx.QueryRowContext(ctx, `SELECT capture_id,accepted_version_id FROM playback_disc_capture_inputs WHERE snapshot_id=?`, id).Scan(&capture, &version); err != nil {
		return Input{}, err
	}
	r, err := s.captureTx(ctx, tx, capture, version)
	if err != nil {
		return Input{}, err
	}
	return Input{Captured: &Captured{Receipt: r}}, nil
}
