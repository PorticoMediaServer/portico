package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Owner birth is deliberately a projection, NOT a capability issuer. W must
// prove current mutation/disclosure scope, original operation admission and
// charged row custody before composing it with command203's original receipt.
// It cannot be reached by a public command or be authorized by a digest.
type ipPersonBirth struct {
	identity CommandIdentity
	owner    ipPersonOwner
	nowMS    int64
}

func ipPersonCreateOwner(ctx context.Context, t *commandTurn, r ipPersonBirth) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != IPProviderClaim || r.identity.Sequence != 1 || r.owner.revision != 1 || r.owner.phase != "claimed" || r.owner.leaseUntilMS <= r.nowMS {
		return ErrInvalid
	}
	q := ipPersonRequest{identity: r.identity, owner: r.owner, nowMS: r.nowMS, nextLeaseOwner: r.owner.leaseOwner, nextLeaseGeneration: r.owner.leaseGeneration, nextLeaseUntilMS: r.owner.leaseUntilMS}
	if !q.valid(IPProviderClaim) || r.nowMS != t.transaction.writer.now().UnixMilli() {
		return ErrInvalid
	}
	// Complete original birth input is bound even though outer W owns its result.
	if _, err = canonicalRequestDigest(IPProviderClaim, 1, r.identity, r); err != nil {
		return err
	}
	o, n := r.owner, r.owner.namespace
	err = ipPersonOne(ctx, t, ipPPCreate, o.id, o.personID, n.provider, o.libraryID, n.subjectID, n.providerID, n.language, n.region, n.shape, o.token, o.personRevision, n.incarnation[:], n.revision, o.policyIncarnation[:], o.policyRevision, o.consentRevision, o.generation, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS, r.identity.Incarnation[:], o.personID, o.token, o.personRevision, n.subjectID, n.provider, n.providerID, n.language, n.region, n.shape, n.incarnation[:], n.revision, o.libraryID, n.provider, o.policyIncarnation[:], o.policyRevision, n.region, o.consentRevision, n.provider)
	if err != nil {
		return err
	}
	// Explicit primary role absence/current coordinates, never inferred from ID.
	return ipPersonFresh(ctx, t, q)
}

type ipPersonRetireRequest struct {
	identity                           CommandIdentity
	ownerID                            string
	personID                           int64
	token                              string
	ownerRevision                      int64
	evidenceID, buildID, publicationID string
	buildRevision                      int64
	nowMS                              int64
}
type ipPersonRetireResult struct {
	afterAlias                int64
	afterExternal             ipPersonExternalID
	deletedRows, deletedBytes int64
	childrenDone              bool
}

func ipPersonRetireValid(r ipPersonRetireRequest) bool {
	return ipPolicyValidIdentity(r.identity) && ipPersonText(r.ownerID, 128, true) && r.personID > 0 && ipPersonText(r.token, 256, true) && r.ownerRevision > 0 && r.ownerRevision < math.MaxInt64 && ipPersonText(r.evidenceID, 128, true) && ipPersonText(r.buildID, 128, true) && r.buildRevision > 0 && r.buildRevision < math.MaxInt64 && r.nowMS >= 0
}
func ipPersonMaintenanceFence(ctx context.Context, t *commandTurn, r ipPersonRetireRequest) error {
	var one int64
	return ipPersonScan(ctx, t, ipPPMaintenance, []any{r.ownerID, r.identity.Incarnation[:], r.ownerRevision, r.personID, r.token}, &one)
}

// Deletes ONE explicitly named obsolete publication after exact head and origin
// exclusion. No removal of current facts and no per-person/child fanout. Old
// publication IDs in immutable accept receipts remain scalar historical results.
func ipPersonRetirePublication(ctx context.Context, t *commandTurn, r ipPersonRetireRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if !ipPersonRetireValid(r) || !ipPersonText(r.publicationID, 128, true) {
		return ErrInvalid
	}
	if err = ipPersonMaintenanceFence(ctx, t, r); err != nil {
		return err
	}
	if _, err = canonicalRequestDigest(IPProviderReclaim, 1, r.identity, r); err != nil {
		return err
	}
	return ipPersonOne(ctx, t, ipPPPublicationDelete, r.publicationID, r.personID, r.ownerID, r.evidenceID)
}

// Detaches the build's exact seal FK only AFTER all accepted publications have
// gone. Evidence remains unchanged and charged. ACK alone never erases a proof:
// bounded receipt reclamation must separately prove no remaining downstream FK.
func ipPersonRetireBuild(ctx context.Context, t *commandTurn, r ipPersonRetireRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if !ipPersonRetireValid(r) {
		return ErrInvalid
	}
	if err = ipPersonMaintenanceFence(ctx, t, r); err != nil {
		return err
	}
	if _, err = canonicalRequestDigest(IPProviderReclaim, 1, r.identity, r); err != nil {
		return err
	}
	if err = ipPersonOne(ctx, t, ipPPBuildRetire, r.nowMS, r.buildID, r.evidenceID, r.ownerID, r.identity.Incarnation[:], r.buildRevision); err != nil {
		return err
	}
	// Retain the terminal owner as the ever-born marker even after body deletion.
	return ipPersonOne(ctx, t, ipPPBuildTerminal, r.ownerID, r.identity.Incarnation[:], r.ownerRevision)
}

// All selected children are bounded by the admitted slice ceiling (32) and
// actual octet preflight. This component removes at most eight aliases OR eight
// external IDs. A live receipt, including ACKed original results, prevents ANY
// body deletion. Proof lifetimes are not shortened by a maintenance request.
// Returned raw keys are observations, never a successful saved command result.
// D needs typed retirement cursor/byte result columns before accepting218 wiring.
func ipPersonRetireChildren(ctx context.Context, t *commandTurn, r ipPersonRetireRequest, b *ipPersonDecode) (out ipPersonRetireResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !ipPersonRetireValid(r) {
		return out, ErrInvalid
	}
	if err = ipPersonMaintenanceFence(ctx, t, r); err != nil {
		return out, err
	}
	var rowsLeft, bytesLeft int64
	if err = ipPersonScan(ctx, t, ipPPRetiring, []any{r.buildID, r.evidenceID, r.ownerID, r.identity.Incarnation[:], r.buildRevision}, &rowsLeft, &bytesLeft); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if rowsLeft < 1 || rowsLeft > ipPersonMaxChildren+1 || bytesLeft < 1 || bytesLeft > ipPersonMaxBytes {
		return out, ErrResolveRequired
	}
	keys, err := ipPersonAliasKeys(ctx, t, r.evidenceID, -1, ipPersonChunk+1)
	if err != nil {
		return out, err
	}
	out.afterAlias = -1
	if len(keys) > 0 {
		if len(keys) > ipPersonChunk {
			keys = keys[:ipPersonChunk]
		}
		for _, key := range keys {
			var n int64
			if err = ipPersonScan(ctx, t, ipPPAliasOctets, []any{r.evidenceID, key}, &n); err != nil {
				return out, err
			}
			if err = b.reserve(n); err != nil {
				return out, err
			}
			// Exact manifest record bytes: tag+ordinal+four LP lengths+text bytes.
			if err = ipPersonOne(ctx, t, ipPPAliasDelete, r.evidenceID, key); err != nil {
				return out, err
			}
			out.deletedRows++
			out.deletedBytes += 48 + n
			out.afterAlias = key
		}
	} else {
		xs, e := ipPersonReadExternals(ctx, t, r.evidenceID, b)
		if e != nil {
			return out, e
		}
		if len(xs) > ipPersonChunk {
			xs = xs[:ipPersonChunk]
		}
		for _, x := range xs {
			if err = ipPersonOne(ctx, t, ipPPExternalDelete, r.evidenceID, x.provider, x.kind, x.id); err != nil {
				return out, err
			}
			out.deletedRows++
			out.deletedBytes += 32 + int64(len(x.provider)+len(x.kind)+len(x.id))
			out.afterExternal = x
		}
	}
	// Native cap1 no-next across BOTH selected families, after actual deletes.
	var empty int64
	if err = ipPersonScan(ctx, t, ipPPNoChildren, []any{r.evidenceID, r.evidenceID}, &empty); err != nil {
		return out, err
	}
	out.childrenDone = empty == 1
	if out.deletedRows > rowsLeft-1 || out.deletedBytes >= bytesLeft {
		return out, ErrResolveRequired
	}
	if err = ipPersonOne(ctx, t, ipPPReclaimAdvance, out.deletedRows, out.deletedRows, out.deletedBytes, out.deletedBytes, r.buildID, r.buildRevision, rowsLeft, bytesLeft); err != nil {
		return out, err
	}
	return out, nil
}

const (
	ipPPBuildBorn         stmt.ID = "ip.person.build_born"
	ipPPBuildTerminal     stmt.ID = "ip.person.build_terminal"
	ipPPCreate            stmt.ID = "ip.person.create"
	ipPPMaintenance       stmt.ID = "ip.person.maintenance"
	ipPPPublicationDelete stmt.ID = "ip.person.publication_delete"
	ipPPBuildRetire       stmt.ID = "ip.person.build_retire"
	ipPPRetiring          stmt.ID = "ip.person.retiring"
	ipPPAliasDelete       stmt.ID = "ip.person.alias_delete"
	ipPPExternalDelete    stmt.ID = "ip.person.external_delete"
	ipPPNoChildren        stmt.ID = "ip.person.no_children"
	ipPPReclaimAdvance    stmt.ID = "ip.person.reclaim_advance"
)

func ipPersonLifecycleDefinitions() []stmt.Definition {
	return []stmt.Definition{
		ipPersonDefinition(ipPPBuildBorn, stmt.Write, `UPDATE metadata_person_operations SET phase='building' WHERE id=? AND command_incarnation=? AND revision=? AND generation=? AND phase='claimed' AND NOT EXISTS(SELECT 1 FROM metadata_person_builds WHERE owner_id=metadata_person_operations.id AND owner_incarnation=metadata_person_operations.command_incarnation)`, "tbii"),
		ipPersonDefinition(ipPPBuildTerminal, stmt.Write, `UPDATE metadata_person_operations SET phase='done',revision=revision+1 WHERE id=? AND command_incarnation=? AND revision=?`, "tbi"),
		ipPersonDefinition(ipPPCreate, stmt.Write, `INSERT INTO metadata_person_operations(id,person_id,provider,library_id,subject_id,subject_kind,provider_id,language,region,request_shape,captured_person_token,captured_person_revision,subject_incarnation,subject_revision,policy_incarnation,policy_revision,consent_revision,generation,revision,phase,lease_owner,lease_generation,lease_until_ms,command_incarnation) SELECT ?,?,?,?,?,'person',?,?,?,?,?,?,?,?,?,?,?,?,1,'claimed',?,?,?,? WHERE EXISTS(SELECT 1 FROM catalog_people WHERE id=? AND token=? AND revision=?) AND EXISTS(SELECT 1 FROM metadata_provider_subjects WHERE id=? AND provider=? AND subject_kind='person' AND provider_id=? AND language=? AND region=? AND request_shape=? AND incarnation=? AND revision=?) AND EXISTS(SELECT 1 FROM metadata_provider_policy WHERE library_id=? AND provider=? AND incarnation=? AND revision=? AND region=? AND enabled=1) AND EXISTS(SELECT 1 FROM screen_metadata_consent WHERE singleton=1 AND revision=? AND confirmed=1) AND EXISTS(SELECT 1 FROM metadata_runtime_settings WHERE singleton=1 AND online_lookups=1) AND EXISTS(SELECT 1 FROM metadata_providers WHERE code=? AND enabled=1)`, "titiitttttibibiiitiibitiitttttbiitbitit"),
		ipPersonDefinition(ipPPMaintenance, stmt.Read, `SELECT 1 FROM metadata_person_operations o JOIN catalog_people p ON p.id=o.person_id WHERE o.id=? AND o.command_incarnation=? AND o.revision=? AND o.person_id=? AND p.token=?`, "tbiit"),
		ipPersonDefinition(ipPPPublicationDelete, stmt.Write, `DELETE FROM metadata_person_publications WHERE id=? AND person_id=? AND operation_id=? AND evidence_id=? AND NOT EXISTS(SELECT 1 FROM metadata_person_sources WHERE person_id=metadata_person_publications.person_id AND provider=metadata_person_publications.provider AND provider_id=metadata_person_publications.provider_id AND accepted_publication_id=metadata_person_publications.id AND accepted_evidence_id=metadata_person_publications.evidence_id) AND NOT EXISTS(SELECT 1 FROM metadata_person_field_origins WHERE person_id=metadata_person_publications.person_id AND provider=metadata_person_publications.provider AND provider_id=metadata_person_publications.provider_id AND publication_id=metadata_person_publications.id AND evidence_id=metadata_person_publications.evidence_id)`, "titt"),
		ipPersonDefinition(ipPPBuildRetire, stmt.Write, `UPDATE metadata_person_builds SET state='retiring',seal_owner=NULL,seal_incarnation=NULL,seal_sequence=NULL,content_revision=content_revision+1,reclaim_due_ms=? WHERE id=? AND evidence_id=? AND owner_id=? AND owner_incarnation=? AND content_revision=? AND state IN('building','ready') AND NOT EXISTS(SELECT 1 FROM metadata_person_publications WHERE evidence_id=metadata_person_builds.evidence_id)`, "itttbi"),
		ipPersonDefinition(ipPPRetiring, stmt.Read, `SELECT staged_rows,staged_bytes FROM metadata_person_builds WHERE id=? AND evidence_id=? AND owner_id=? AND owner_incarnation=? AND content_revision=? AND state='retiring' AND NOT EXISTS(SELECT 1 FROM metadata_person_receipts WHERE evidence_id=metadata_person_builds.evidence_id) AND NOT EXISTS(SELECT 1 FROM metadata_person_publications WHERE evidence_id=metadata_person_builds.evidence_id)`, "tttbi"),
		ipPersonDefinition(ipPPAliasDelete, stmt.Write, `DELETE FROM metadata_evidence_aliases WHERE evidence_id=? AND ordinal=?`, "ti"),
		ipPersonDefinition(ipPPExternalDelete, stmt.Write, `DELETE FROM metadata_evidence_external_ids WHERE evidence_id=? AND provider=? AND subject_kind=? AND provider_id=?`, "tttt"),
		ipPersonDefinition(ipPPNoChildren, stmt.Read, `SELECT NOT EXISTS(SELECT 1 FROM metadata_evidence_aliases WHERE evidence_id=?) AND NOT EXISTS(SELECT 1 FROM metadata_evidence_external_ids WHERE evidence_id=?)`, "tt"),
		ipPersonDefinition(ipPPReclaimAdvance, stmt.Write, `UPDATE metadata_person_builds SET staged_rows=staged_rows-?,cursor=staged_rows-?,staged_bytes=staged_bytes-?,materialized_charge=materialized_charge-?,content_revision=content_revision+1 WHERE id=? AND content_revision=? AND state='retiring' AND staged_rows=? AND staged_bytes=?`, "iiiitiii"),
	}
}

// Remaining typed composition checklist, deliberately not callback extension
// points: 213–215 person publication sets are not admitted by this scalar slice;
// add typed page/header/member manifests only for a genuinely complete family.
// 217 is drafted through ipPersonProviderBody with a distinct closed result
// kind; its accepting receipt still requires D command_kind='delta'.
// 218 needs D typed raw alias/external cursor, deleted rows/bytes, terminal and
// exact owner/build revisions in receipts. Final evidence/header/build deletion
// needs no-next for ALL generic child families, receipt/publication/EN-build/
// provider-subject accepted-evidence exclusions and W custody reconciliation.
// ACK/reclaim250/251 must retain builds/publications' person proof FKs and not
// delete evidence by ACK alone. Terminal-owner retention follows body/publication
// no-next. Terminal owner stays retained forever for this owner/incarnation;
// its removed build never permits rebirth. Root owns fixed-slot251 cleanup.
// These are publication gates, not invented successful placeholders.

// Birth and reclaim are distinct typed inputs. This body binds the COMPLETE
// birth root and saves command203 exactly once; an existing owner can only
// resolve the original receipt, never be silently recreated or reset.
func ipPersonBirthBody(ctx context.Context, t *commandTurn, r ipPersonBirth, b *ipPersonDecode) (out ipPersonOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != IPProviderClaim || !ipPolicyValidIdentity(r.identity) {
		return out, ErrInvalid
	}
	check := ipPersonRequest{identity: r.identity, owner: r.owner, nowMS: r.nowMS, nextLeaseOwner: r.owner.leaseOwner, nextLeaseGeneration: r.owner.leaseGeneration, nextLeaseUntilMS: r.owner.leaseUntilMS}
	if !check.valid(IPProviderClaim) {
		return out, ErrInvalid
	}
	q, err := canonicalRequestDigest(IPProviderClaim, 1, r.identity, r)
	if err != nil {
		return out, err
	}
	key := ipPersonRequest{identity: r.identity, owner: r.owner, nowMS: r.nowMS}
	var inc []byte
	var last, ack int64
	err = ipPersonScan(ctx, t, ipPPOwner, []any{r.owner.id}, &inc, &last, &ack)
	if err == nil {
		out, _, err = ipPersonLookup(ctx, t, IPProviderClaim, key, q, b)
		if err == nil && out.decision == receiptFresh {
			err = ErrConflict
		}
		return out, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err = ipPersonCreateOwner(ctx, t, r); err != nil {
		return out, err
	}
	out.decision = receiptFresh
	out.result = ipPersonResult{revision: 2, appliedMS: r.nowMS}
	if err = ipPersonOne(ctx, t, ipPPOwnerAdvance, r.identity.Sequence, "claimed", r.owner.id, r.identity.Incarnation[:], int64(1), int64(0), int64(0)); err != nil {
		return out, err
	}
	err = ipPersonSave(ctx, t, IPProviderClaim, key, q, out.result)
	return out, err
}
