package compactcatalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

var ErrEntityMutating = &apierror.Error{Code: "request_in_progress", Cause: errors.New("catalog entity has an admitted mutation")}
var ErrEntityPinned = &apierror.Error{Code: "request_in_progress", Cause: errors.New("catalog entity still has MusicBrainz pins")}
var ErrMutationStale = &apierror.Error{Code: "revision_mismatch", Cause: errors.New("catalog mutation binding is stale")}

type MutationRef struct {
	ID         string
	Generation int64
}
type MutationStep struct {
	Complete, More, PinDrained, Progressed bool
	Outcome                                string
}

func PendingMutationStep() MutationStep {
	return MutationStep{More: true, Progressed: true, Outcome: "pending"}
}

type MutationIntent struct {
	OwnerKind, OwnerID                                                   string
	OwnerGeneration                                                      int64
	Entity, Target                                                       int64
	Mode, Parent, Family                                                 string
	Provider, ProviderKind, ProviderID, AcceptedRevision, CauseOperation string
}
type EntityMutation struct {
	Ref                                                                  MutationRef
	Family, Parent, OwnerKind, OwnerID                                   string
	OwnerGeneration                                                      int64
	Entity, Subject                                                      int64
	Public                                                               string
	Kind                                                                 Kind
	Library                                                              int64
	Incarnation, PublicationIncarnation, SourceDigest                    string
	Target, TargetSubject                                                int64
	TargetPublic, TargetIncarnation, TargetPublicationIncarnation        string
	Mode, Phase, Status, Outcome                                         string
	Cursor                                                               int64
	Child                                                                string
	CurrentKey                                                           int64
	Provider, ProviderKind, ProviderID, AcceptedRevision, CauseOperation string
}

const mutationColumns = `mutation_id,generation,family_id,COALESCE(parent_mutation_id,''),owner_kind,owner_id,owner_generation,COALESCE(entity_id,0),subject_id,public_id,entity_kind,library_id,entity_incarnation,publication_incarnation,source_digest,COALESCE(target_id,0),target_subject_id,target_public_id,target_incarnation,target_publication_incarnation,mode,phase,status,outcome,cursor,COALESCE(current_child,''),current_key,provider,provider_kind,provider_id,accepted_revision,cause_operation`

func ReadEntityMutationTx(ctx context.Context, tx *sql.Tx, ref MutationRef) (m EntityMutation, err error) {
	err = tx.QueryRowContext(ctx, `SELECT `+mutationColumns+` FROM catalog_entity_mutations WHERE mutation_id=? AND generation=?`, ref.ID, ref.Generation).Scan(&m.Ref.ID, &m.Ref.Generation, &m.Family, &m.Parent, &m.OwnerKind, &m.OwnerID, &m.OwnerGeneration, &m.Entity, &m.Subject, &m.Public, &m.Kind, &m.Library, &m.Incarnation, &m.PublicationIncarnation, &m.SourceDigest, &m.Target, &m.TargetSubject, &m.TargetPublic, &m.TargetIncarnation, &m.TargetPublicationIncarnation, &m.Mode, &m.Phase, &m.Status, &m.Outcome, &m.Cursor, &m.Child, &m.CurrentKey, &m.Provider, &m.ProviderKind, &m.ProviderID, &m.AcceptedRevision, &m.CauseOperation)
	return
}
func MutationRefTx(ctx context.Context, tx *sql.Tx, id string) (r MutationRef, err error) {
	err = tx.QueryRowContext(ctx, `SELECT mutation_id,generation FROM catalog_entity_mutations WHERE mutation_id=?`, id).Scan(&r.ID, &r.Generation)
	return
}
func FindEntityMutationTx(ctx context.Context, tx *sql.Tx, owner, id string, generation, subject int64, mode string) (r MutationRef, err error) {
	err = tx.QueryRowContext(ctx, `SELECT mutation_id,generation FROM catalog_entity_mutations WHERE owner_kind=? AND owner_id=? AND owner_generation=? AND subject_id=? AND mode=?`, owner, id, generation, subject, mode).Scan(&r.ID, &r.Generation)
	return
}

type mutationFamilyKey struct{}

// WithMutationFamily is only for catalog continuation of an already checked
// mutation. Provider publication must never use it.
func WithMutationFamily(ctx context.Context, family string) context.Context {
	return context.WithValue(ctx, mutationFamilyKey{}, family)
}
func mutationFamily(ctx context.Context) string {
	v, _ := ctx.Value(mutationFamilyKey{}).(string)
	return v
}

const mutationAncestorsSQL = `WITH RECURSIVE ancestors(id,depth) AS (SELECT ?1,0 UNION ALL SELECT e.parent_id,a.depth+1 FROM ancestors a JOIN catalog_entities e ON e.id=a.id WHERE e.parent_id IS NOT NULL AND a.depth<4) `

func RequireEntityAcquirableTx(ctx context.Context, tx *sql.Tx, entity int64) error {
	if entity == 0 {
		return nil
	}
	var busy bool
	err := tx.QueryRowContext(ctx, mutationAncestorsSQL+`SELECT EXISTS(SELECT 1 FROM ancestors a JOIN catalog_entity_mutations m ON (m.entity_id=a.id OR m.target_id=a.id) WHERE m.status<>'terminal' AND m.family_id<>?2)`, entity, mutationFamily(ctx)).Scan(&busy)
	if err != nil {
		return err
	}
	if busy {
		return ErrEntityMutating
	}
	return nil
}
func RequireNoEntityMBPinsTx(ctx context.Context, tx *sql.Tx, entity int64) error {
	var pinned bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mb_song_links WHERE item_id=?1) OR EXISTS(SELECT 1 FROM mb_album_links WHERE album_id=?1)`, entity).Scan(&pinned); err != nil {
		return err
	}
	if pinned {
		return ErrEntityPinned
	}
	return nil
}
func entityLifetimeTx(ctx context.Context, tx *sql.Tx, entity int64) (public string, kind Kind, library int64, incarnation, publication string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT pid(e.public_id),e.kind,e.library_id,COALESCE(ah.incarnation,sh.incarnation,''),COALESCE(ph.incarnation,'') FROM catalog_entities e LEFT JOIN mb_album_heads ah ON ah.album_id=e.id LEFT JOIN mb_song_heads sh ON sh.item_id=e.id LEFT JOIN metadata_publication_heads ph ON ph.item_id=e.id WHERE e.id=?`, entity).Scan(&public, &kind, &library, &incarnation, &publication)
	return
}

// EntityMutationSourceDigestTx binds actual attachment and inventory custody,
// including source/root incarnations and source generation. It stores no file list.
func EntityMutationSourceDigestTx(ctx context.Context, tx *sql.Tx, entity int64) (string, error) {
	h := sha256.New()
	enc := json.NewEncoder(h)
	var public string
	var kind, library int64
	var parent sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT pid(public_id),kind,library_id,parent_id FROM catalog_entities WHERE id=?`, entity).Scan(&public, &kind, &library, &parent); err != nil {
		return "", err
	}
	if err := enc.Encode([]any{public, kind, library, parent}); err != nil {
		return "", err
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.token,a.path,a.size,a.modified_ns,l.part_index,l.start_seconds,l.end_seconds,l.edition,
 COALESCE(o.id,''),COALESCE(o.revision,''),COALESCE(o.source_id,''),COALESCE(o.root_incarnation,''),COALESCE(o.object_identity,''),COALESCE(o.state,''),COALESCE(o.retired,0),COALESCE(o.missing_since,''),
 COALESCE(s.generation,0),COALESCE(s.incarnation,''),COALESCE(s.root_identity,''),COALESCE(s.root,''),COALESCE(s.enabled,0),COALESCE(s.health,'')
 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id LEFT JOIN inventory_objects o ON o.asset_id=a.token LEFT JOIN library_sources s ON s.id=o.source_id WHERE l.entity_id=? ORDER BY a.token,o.id`, entity)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var token, path, edition, object, revision, source, rootInc, objectID, state, missing, sourceInc, rootID, root, health string
		var size, modified, generation int64
		var part, retired, enabled int
		var start float64
		var end sql.NullFloat64
		if err = rows.Scan(&token, &path, &size, &modified, &part, &start, &end, &edition, &object, &revision, &source, &rootInc, &objectID, &state, &retired, &missing, &generation, &sourceInc, &rootID, &root, &enabled, &health); err != nil {
			return "", err
		}
		if err = enc.Encode([]any{token, path, size, modified, part, start, end, edition, object, revision, source, rootInc, objectID, state, retired, missing, generation, sourceInc, rootID, root, enabled, health == "offline" || health == "root_changed" || health == "removing"}); err != nil {
			return "", err
		}
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func BeginEntityMutationTx(ctx context.Context, tx *sql.Tx, in MutationIntent) (MutationRef, error) {
	if in.OwnerGeneration <= 0 || in.OwnerID == "" {
		return MutationRef{}, ErrMutationStale
	}
	old, err := FindEntityMutationTx(ctx, tx, in.OwnerKind, in.OwnerID, in.OwnerGeneration, in.Entity, in.Mode)
	if err == nil {
		return old, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return old, err
	}
	var raw [16]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return MutationRef{}, err
	}
	ref := MutationRef{ID: hex.EncodeToString(raw[:]), Generation: 1}
	family := in.Family
	if family == "" {
		family = ref.ID
	}
	ctx = WithMutationFamily(ctx, family)
	if err = RequireEntityAcquirableTx(ctx, tx, in.Entity); err != nil {
		return ref, err
	}
	if err = RequireEntityAcquirableTx(ctx, tx, in.Target); err != nil {
		return ref, err
	}
	public, kind, library, inc, pub, err := entityLifetimeTx(ctx, tx, in.Entity)
	if err != nil {
		return ref, err
	}
	if (kind == Album || kind == Track) && inc == "" {
		return ref, ErrMutationStale
	}
	if kind == Track && pub == "" {
		return ref, ErrMutationStale
	}
	var artistSurvivor int64
	if in.Mode == "adopt" && kind == Artist && in.OwnerKind == "identity" && in.Parent == "" && in.Target == 0 && in.ProviderKind == "artist" {
		var identity bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_external_ids WHERE entity_id=? AND provider=? AND provider_kind=? AND provider_id=?)`, in.Entity, in.Provider, in.ProviderKind, in.ProviderID).Scan(&identity); err != nil {
			return ref, err
		}
		if !identity {
			return ref, ErrMutationStale
		}
		artistSurvivor = in.Entity
	}
	if err = requireNoReverseMutationOverlapTx(ctx, tx, family, in.Entity, in.Target, artistSurvivor); err != nil {
		return ref, err
	}
	digest, err := EntityMutationSourceDigestTx(ctx, tx, in.Entity)
	if err != nil {
		return ref, err
	}
	var tp, ti, tpi string
	if in.Target != 0 {
		var tk Kind
		var tl int64
		tp, tk, tl, ti, tpi, err = entityLifetimeTx(ctx, tx, in.Target)
		if err != nil {
			return ref, err
		}
		if (tk == Album || tk == Track) && ti == "" {
			return ref, ErrMutationStale
		}
		if tk == Track && tpi == "" {
			return ref, ErrMutationStale
		}
		if tk != kind || tl != library || in.Target == in.Entity {
			return ref, ErrMutationStale
		}
	}
	if in.Target != 0 {
		targetDigest, e := EntityMutationSourceDigestTx(ctx, tx, in.Target)
		if e != nil {
			return ref, e
		}
		digest = mutationPairDigest(digest, targetDigest)
	}
	if in.Mode == "adopt" && kind == Artist && in.Provider == "musicbrainz" && in.CauseOperation != "" {
		digest, err = artistAuthorityDigestTx(ctx, tx, library, digest)
		if err != nil {
			return ref, err
		}
	}
	var parent, target any
	if in.Parent != "" {
		parent = in.Parent
	}
	if in.Target != 0 {
		target = in.Target
	}
	subjectAncestors, err := mutationAncestorsTx(ctx, tx, in.Entity)
	if err != nil {
		return ref, err
	}
	targetAncestors, err := mutationAncestorsTx(ctx, tx, in.Target)
	if err != nil {
		return ref, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_entity_mutations(mutation_id,generation,family_id,parent_mutation_id,owner_kind,owner_id,owner_generation,entity_id,subject_id,public_id,entity_kind,library_id,entity_incarnation,publication_incarnation,source_digest,target_id,target_subject_id,target_public_id,target_incarnation,target_publication_incarnation,mode,phase,provider,provider_kind,provider_id,accepted_revision,cause_operation,subject_ancestor_1,subject_ancestor_2,subject_ancestor_3,subject_ancestor_4,target_ancestor_1,target_ancestor_2,target_ancestor_3,target_ancestor_4) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'admitted',?,?,?,?,?,?,?,?,?,?,?,?,?)`, ref.ID, ref.Generation, family, parent, in.OwnerKind, in.OwnerID, in.OwnerGeneration, in.Entity, in.Entity, public, kind, library, inc, pub, digest, target, in.Target, tp, ti, tpi, in.Mode, in.Provider, in.ProviderKind, in.ProviderID, in.AcceptedRevision, in.CauseOperation, subjectAncestors[0], subjectAncestors[1], subjectAncestors[2], subjectAncestors[3], targetAncestors[0], targetAncestors[1], targetAncestors[2], targetAncestors[3])
	if err != nil {
		return ref, err
	}
	// Revise the bounded subject only; ancestors fence descendants without fanout.
	if in.Mode != "adopt" {
		for _, q := range []string{`UPDATE mb_album_heads SET revision=revision+1 WHERE album_id=?`, `UPDATE mb_song_heads SET revision=revision+1 WHERE item_id=?`, `UPDATE metadata_publication_heads SET source_revision=source_revision+1 WHERE item_id=?`, `UPDATE mb_jobs SET generation=generation+1,status='pending',lease='',lease_until='' WHERE entity_id=?`} {
			if _, err = tx.ExecContext(ctx, q, in.Entity); err != nil {
				return ref, err
			}
		}
	}
	return ref, nil
}
func CheckEntityMutationBindingsTx(ctx context.Context, tx *sql.Tx, ref MutationRef) (EntityMutation, error) {
	m, err := ReadEntityMutationTx(ctx, tx, ref)
	if err != nil {
		return m, err
	}
	if m.Status == "terminal" {
		return m, nil
	}
	p, k, l, i, pi, err := entityLifetimeTx(ctx, tx, m.Entity)
	if err != nil {
		return m, err
	}
	if p != m.Public || k != m.Kind || l != m.Library || i != m.Incarnation || pi != m.PublicationIncarnation {
		return m, ErrMutationStale
	}
	digest, err := EntityMutationSourceDigestTx(ctx, tx, m.Entity)
	if err != nil {
		return m, err
	}
	if m.Target != 0 {
		targetDigest, e := EntityMutationSourceDigestTx(ctx, tx, m.Target)
		if e != nil {
			return m, e
		}
		digest = mutationPairDigest(digest, targetDigest)
	}
	if m.Mode == "adopt" && m.Kind == Artist && m.Provider == "musicbrainz" && m.CauseOperation != "" {
		digest, err = artistAuthorityDigestTx(ctx, tx, m.Library, digest)
		if err != nil {
			return m, err
		}
	}
	if digest != m.SourceDigest {
		return m, ErrMutationStale
	}
	if m.Target != 0 {
		p, k, l, i, pi, err = entityLifetimeTx(ctx, tx, m.Target)
		if err != nil {
			return m, err
		}
		if p != m.TargetPublic || k != m.Kind || l != m.Library || i != m.TargetIncarnation || pi != m.TargetPublicationIncarnation {
			return m, ErrMutationStale
		}
	}
	return m, nil
}
func CheckEntityMutationTx(ctx context.Context, tx *sql.Tx, ref MutationRef) (EntityMutation, error) {
	m, err := CheckEntityMutationBindingsTx(ctx, tx, ref)
	if err != nil || m.Status == "terminal" {
		return m, err
	}
	if m.Status == "waiting" {
		return m, ErrEntityMutating
	}
	familyCtx := WithMutationFamily(ctx, m.Family)
	if err = RequireEntityAcquirableTx(familyCtx, tx, m.Entity); err != nil {
		return m, err
	}
	if err = RequireEntityAcquirableTx(familyCtx, tx, m.Target); err != nil {
		return m, err
	}
	return m, nil
}
func SetMutationPhaseTx(ctx context.Context, tx *sql.Tx, ref MutationRef, phase string) error {
	r, e := tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET phase=? WHERE mutation_id=? AND generation=? AND status='active'`, phase, ref.ID, ref.Generation)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrMutationStale
	}
	return nil
}
func FinishEntityMutationTx(ctx context.Context, tx *sql.Tx, ref MutationRef, outcome string) (MutationStep, error) {
	r, e := tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET status='terminal',phase='terminal',outcome=?,entity_id=NULL,target_id=NULL,subject_ancestor_1=NULL,subject_ancestor_2=NULL,subject_ancestor_3=NULL,subject_ancestor_4=NULL,target_ancestor_1=NULL,target_ancestor_2=NULL,target_ancestor_3=NULL,target_ancestor_4=NULL WHERE mutation_id=? AND generation=? AND status<>'terminal' AND current_child IS NULL`, outcome, ref.ID, ref.Generation)
	if e != nil {
		return MutationStep{}, e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return MutationStep{}, ErrMutationStale
	}
	return MutationStep{Complete: true, Progressed: true, Outcome: outcome}, nil
}
func DrainEntityMBPinTx(ctx context.Context, tx *sql.Tx, ref MutationRef) (MutationStep, error) {
	m, err := CheckEntityMutationTx(ctx, tx, ref)
	if err != nil {
		return MutationStep{}, err
	}
	if m.Status != "active" {
		return MutationStep{Complete: m.Status == "terminal", More: m.Status != "terminal", Outcome: m.Outcome}, nil
	}
	return pinAccountingStepTx(ctx, tx, m)
}

// PinFreeMutationContextTx checks live custody before allowing catalog's final
// state transfer. The FK release remains in the caller's final transaction.
func PinFreeMutationContextTx(ctx context.Context, tx *sql.Tx, ref MutationRef) (context.Context, EntityMutation, error) {
	m, err := CheckEntityMutationTx(ctx, tx, ref)
	if err != nil {
		return ctx, m, err
	}
	if m.Status != "active" || m.Phase != "finalize" {
		return ctx, m, fmt.Errorf("%w: not finalizable", ErrMutationStale)
	}
	if err = RequireNoEntityMBPinsTx(ctx, tx, m.Entity); err != nil {
		return ctx, m, err
	}
	return WithMutationFamily(ctx, m.Family), m, nil
}

// RequireMBPublicationMutationCurrentTx is the final OWN-WRITE fence. The
// publisher checked RequireEntityAcquirableTx before enqueue; only its exact
// accepted survivor adoption root can have been added by its own transaction.
// Outgoing intents, targets, another cause, another incarnation or pin always
// conflict. This grants no exemption to a provider before enqueue.
func RequireMBPublicationMutationCurrentTx(ctx context.Context, tx *sql.Tx, entity int64, cause string) error {
	var busy bool
	err := tx.QueryRowContext(ctx, mutationAncestorsSQL+`SELECT EXISTS(SELECT 1 FROM ancestors a JOIN catalog_entity_mutations m ON (m.entity_id=a.id OR m.target_id=a.id) WHERE m.status<>'terminal' AND NOT (
 a.depth=0 AND m.entity_id=?1 AND m.target_id IS NULL AND m.mode='adopt' AND m.owner_kind='mb-adoption' AND m.provider='musicbrainz' AND m.provider_kind='release' AND m.parent_mutation_id IS NULL AND m.cause_operation=?2 AND ?2<>''
 AND m.public_id=(SELECT pid(public_id) FROM catalog_entities WHERE id=?1)
 AND m.entity_incarnation=(SELECT incarnation FROM mb_album_heads WHERE album_id=?1)
 AND m.accepted_revision=(SELECT release_revision FROM mb_album_links WHERE album_id=?1)
 AND m.provider_id=(SELECT release_id FROM mb_album_links WHERE album_id=?1)))`, entity, cause).Scan(&busy)
	if err != nil {
		return err
	}
	if busy {
		return ErrEntityMutating
	}
	return nil
}

func mutationPairDigest(source, target string) string {
	h := sha256.Sum256([]byte(source + ":" + target))
	return hex.EncodeToString(h[:])
}

var mutationAncestorColumns = []string{"subject_ancestor_1", "subject_ancestor_2", "subject_ancestor_3", "subject_ancestor_4", "target_ancestor_1", "target_ancestor_2", "target_ancestor_3", "target_ancestor_4"}

func mutationAncestorsTx(ctx context.Context, tx *sql.Tx, entity int64) (ancestors [4]any, err error) {
	if entity == 0 {
		return ancestors, nil
	}
	var ids [4]sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT e.parent_id,p.parent_id,g.parent_id,h.parent_id FROM catalog_entities e LEFT JOIN catalog_entities p ON p.id=e.parent_id LEFT JOIN catalog_entities g ON g.id=p.parent_id LEFT JOIN catalog_entities h ON h.id=g.parent_id WHERE e.id=?`, entity).Scan(&ids[0], &ids[1], &ids[2], &ids[3])
	for i, id := range ids {
		if id.Valid {
			ancestors[i] = id.Int64
		}
	}
	return
}
func requireNoReverseMutationOverlapTx(ctx context.Context, tx *sql.Tx, family string, entity, target int64, artistDestination ...int64) error {
	for _, endpoint := range []int64{entity, target} {
		if endpoint == 0 {
			continue
		}
		for _, column := range mutationAncestorColumns {
			var overlap bool
			// Each source-owned column has a full-prefix FK index. A fixed set of
			// equality probes, independent of unrelated active work or terminal history.
			status := `status<>'terminal'`
			// Only this exact captured Artist edge can be a destination-only
			// handoff. The waiting CHECK + entry/parent/artist guards prove
			// current Artist == subject_ancestor_1 for every such cause.
			// Seek active/blocked separately; never scan compatible waiters.
			if len(artistDestination) == 1 && artistDestination[0] != 0 && endpoint == artistDestination[0] && column == "subject_ancestor_1" {
				status = `status IN('active','blocked')`
			}
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entity_mutations WHERE `+column+`=? AND `+status+` AND family_id<>?)`, endpoint, family).Scan(&overlap); err != nil {
				return err
			}
			if overlap {
				return ErrEntityMutating
			}
		}
	}
	return nil
}
func RestartRetainedMutationTx(ctx context.Context, tx *sql.Tx, ref MutationRef) (MutationRef, error) {
	m, err := ReadEntityMutationTx(ctx, tx, ref)
	if err != nil {
		return ref, err
	}
	if m.Status != "terminal" || m.Outcome != "retained" || m.Mode != "force" || m.OwnerKind != "library" {
		return ref, ErrMutationStale
	}
	p, k, l, i, pi, err := entityLifetimeTx(ctx, tx, m.Subject)
	if err != nil {
		return ref, err
	}
	if p != m.Public || k != m.Kind || l != m.Library || i != m.Incarnation || pi != m.PublicationIncarnation {
		return ref, ErrMutationStale
	}
	if err = RequireEntityAcquirableTx(ctx, tx, m.Subject); err != nil {
		return ref, err
	}
	if err = requireNoReverseMutationOverlapTx(ctx, tx, m.Family, m.Subject, 0); err != nil {
		return ref, err
	}
	ancestors, err := mutationAncestorsTx(ctx, tx, m.Subject)
	if err != nil {
		return ref, err
	}
	digest, err := EntityMutationSourceDigestTx(ctx, tx, m.Subject)
	if err != nil {
		return ref, err
	}
	r, err := tx.ExecContext(ctx, `UPDATE catalog_entity_mutations SET generation=generation+1,entity_id=subject_id,source_digest=?,subject_ancestor_1=?,subject_ancestor_2=?,subject_ancestor_3=?,subject_ancestor_4=?,status='active',phase='admitted',outcome='pending' WHERE mutation_id=? AND generation=? AND status='terminal' AND outcome='retained'`, digest, ancestors[0], ancestors[1], ancestors[2], ancestors[3], ref.ID, ref.Generation)
	if err != nil {
		return ref, err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ref, ErrMutationStale
	}
	ref.Generation++
	return ref, nil
}

func artistAuthorityDigestTx(ctx context.Context, tx *sql.Tx, library int64, digest string) (string, error) {
	var incarnation string
	var revision int64
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT p.incarnation,p.revision,p.enabled FROM mb_provider_policies p JOIN catalog_libraries cl ON cl.library_id=p.library_id WHERE cl.id=? AND cl.retired=0`, library).Scan(&incarnation, &revision, &enabled)
	if err != nil {
		return "", err
	}
	proof, err := json.Marshal([]any{digest, incarnation, revision, enabled})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(proof)
	return hex.EncodeToString(sum[:]), nil
}
