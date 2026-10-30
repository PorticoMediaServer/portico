package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// currentArtistIdentityRootTx identifies real current custody, not a callback
// or a grant from an operation ID. The unique identity root index bounds it.
func currentArtistIdentityRootTx(ctx context.Context, tx *sql.Tx, library int64, p compactcatalog.ProviderIdentity) (ref compactcatalog.MutationRef, found bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT m.mutation_id,m.generation FROM catalog_entity_mutations m INDEXED BY catalog_entity_mutations_artist_identity JOIN catalog_entities e ON e.id=m.entity_id AND e.kind=5 AND e.library_id=m.library_id AND pid(e.public_id)=m.public_id JOIN catalog_external_ids x ON x.entity_id=e.id AND x.provider=m.provider AND x.provider_kind=m.provider_kind AND x.provider_id=m.provider_id JOIN console_operations o ON o.kind='catalog-mutation' AND o.resource=m.mutation_id AND o.state IN('queued','running','reconciling') WHERE m.library_id=? AND m.provider=? AND m.provider_kind=? AND m.provider_id=? AND m.entity_kind=5 AND m.owner_kind='identity' AND m.mode='adopt' AND m.parent_mutation_id IS NULL AND m.status IN('active','blocked')`, library, p.Provider, p.Kind, p.ID).Scan(&ref.ID, &ref.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return ref, false, nil
	}
	if err != nil {
		return ref, false, err
	}
	return ref, true, nil
}

// This is a binding-only handoff of already published cause. It cannot call a
// drain, merge or absorb helper. Every live FK remains until this root's own
// terminal writer. Physical Artist source claims still see waiting custody.
func artistHandoffStepTx(ctx context.Context, tx *sql.Tx, ref compactcatalog.MutationRef) (compactcatalog.MutationStep, error) {
	m, err := compactcatalog.CheckEntityMutationBindingsTx(ctx, tx, ref)
	if errors.Is(err, compactcatalog.ErrMutationStale) || errors.Is(err, sql.ErrNoRows) {
		return staleMutationStepTx(ctx, tx, m)
	}
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if m.Status != "waiting" || m.OwnerKind != "mb-adoption" || m.Mode != "adopt" || m.Kind != compactcatalog.Album || m.Phase != "artist-identity" || m.Child != "" {
		return compactcatalog.MutationStep{}, compactcatalog.ErrMutationStale
	}
	if err = adoptionCurrentTx(ctx, tx, m); err != nil {
		if errors.Is(err, compactcatalog.ErrMutationStale) {
			return staleMutationStepTx(ctx, tx, m)
		}
		return compactcatalog.MutationStep{}, err
	}
	var artist int64
	var provider string
	err = tx.QueryRowContext(ctx, `SELECT a.artist_id,c.artist_id FROM catalog_albums a JOIN mb_release_credits c ON c.revision_id=? AND c.ordinal=0 WHERE a.entity_id=? AND (SELECT count(*) FROM mb_release_credits WHERE revision_id=?)=1 AND c.artist_id<>'' AND NOT EXISTS(SELECT 1 FROM catalog_external_ids x WHERE x.entity_id=a.artist_id AND x.provider='musicbrainz' AND x.provider_kind='artist' AND x.provider_id<>c.artist_id)`, m.AcceptedRevision, m.Entity, m.AcceptedRevision).Scan(&artist, &provider)
	if errors.Is(err, sql.ErrNoRows) {
		return compactcatalog.FinishEntityMutationTx(ctx, tx, ref, "retained")
	}
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	p := compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "artist", ID: provider}
	active, exists, err := currentArtistIdentityRootTx(ctx, tx, m.Library, p)
	if err != nil {
		return compactcatalog.MutationStep{}, err
	}
	if exists {
		var known bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_external_ids WHERE entity_id=? AND provider=? AND provider_kind=? AND provider_id=?)`, artist, p.Provider, p.Kind, p.ID).Scan(&known); err != nil {
			return compactcatalog.MutationStep{}, err
		}
		if !known {
			return compactcatalog.MutationStep{More: true, Outcome: "pending"}, nil
		}
		live, e := compactcatalog.CheckEntityMutationTx(ctx, tx, active)
		if e != nil || live.Status != "active" {
			if e == nil || errors.Is(e, compactcatalog.ErrMutationStale) || errors.Is(e, compactcatalog.ErrEntityMutating) || errors.Is(e, sql.ErrNoRows) {
				return compactcatalog.MutationStep{More: true, Outcome: "pending"}, nil
			}
			return compactcatalog.MutationStep{}, e
		}
		if e = adoptionCurrentTx(ctx, tx, live); e != nil {
			if errors.Is(e, compactcatalog.ErrMutationStale) {
				return compactcatalog.MutationStep{More: true, Outcome: "pending"}, nil
			}
			return compactcatalog.MutationStep{}, e
		}
		// That actual operation owns the frozen live index containing this known
		// artist. Completing this read-only cause releases its lifetime debt.
		return compactcatalog.FinishEntityMutationTx(ctx, tx, ref, "retained")
	}
	if err = compactcatalog.RecordProviderIdentityTx(ctx, tx, artist, p); err != nil {
		if errors.Is(err, compactcatalog.ErrEntityMutating) {
			return compactcatalog.MutationStep{More: true, Outcome: "pending"}, nil
		}
		return compactcatalog.MutationStep{}, err
	}
	if err = enqueueIdentityAdoptionTx(ctx, tx, artist, p, m.CauseOperation); err != nil {
		return compactcatalog.MutationStep{}, err
	}
	return compactcatalog.FinishEntityMutationTx(ctx, tx, ref, "retained")
}
