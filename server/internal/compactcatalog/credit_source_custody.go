package compactcatalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrCreditSourceStale refuses a publication based on a superseded source head.
// CreditSourcePublicationMax follows the accepted ScreenRecord credit budget
// (metadataprovider/screen.go); TVDB extended episodes accept at most 64.
const CreditSourcePublicationMax = 128

// Accepted local NFO locators are bounded source record references, not person IDs.
const CreditSourceRecordReferenceMax = 4096

// Existing owner text limits count UTF-16 units; private custody must retain
// their UTF-8 bytes without rejecting already-valid non-ASCII owner credits.
const CreditNameMaxBytes = 1200
const CreditRoleMaxBytes = 2000
const CreditDepartmentMaxBytes = 800

var ErrCreditSourceStale = errors.New("credit source head changed")

// AcceptedCreditTuple is immutable accepted evidence, not a person lookup or
// public credit identity. Empty provider identifiers are preserved verbatim.
// RecordReference is the accepted provider record, not ScreenCredit.ID.
// Target and person scalars deliberately have no foreign key to catalog_people.
type AcceptedCreditTuple struct {
	RecordReference   string
	CreditID          string
	ProviderPersonID  string
	PersonIdentityKey string
	PersonToken       string
	TargetKind        string
	TargetID          string
	CreditedName      string
	Role              string
	Department        string
	SourceOrdinal     int
}

type CreditSourceReferenceStatus string

const (
	CreditSourceNew       CreditSourceReferenceStatus = "new"
	CreditSourceExact     CreditSourceReferenceStatus = "exact"
	CreditSourceAmbiguous CreditSourceReferenceStatus = "ambiguous"
)

type CreditSourceMember struct {
	SourceRowID string
	Position    int
	Status      CreditSourceReferenceStatus
	Original    AcceptedCreditTuple
}

type CreditSourcePublication struct {
	ID              string
	EntityID        int64
	Provider        string
	RecordReference string
	ObservedAt      string
	Revision        int64
	Members         []CreditSourceMember
}

// CreditSourceReader permits source custody reads without exposing a writable
// repository or consulting the effective public projection.
type CreditSourceReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ReadCreditSourceHead reads one current entity/provider seam. A missing head is
// distinct from a successful empty publication (which has a nonzero revision).
func ReadCreditSourceHead(ctx context.Context, q CreditSourceReader, entity int64, provider string) (CreditSourcePublication, error) {
	var out CreditSourcePublication
	out.EntityID, out.Provider = entity, provider
	err := q.QueryRowContext(ctx, `SELECT p.publication_id,p.accepted_record_reference,p.observed_at,h.revision
 FROM credit_source_heads h JOIN credit_source_publications p ON p.publication_id=h.publication_id
 WHERE h.entity_id=? AND h.provider=? AND p.sealed=1`, entity, provider).Scan(&out.ID, &out.RecordReference, &out.ObservedAt, &out.Revision)
	if err != nil {
		return out, err
	}
	rows, err := q.QueryContext(ctx, `SELECT r.source_row_id,m.position,m.reference_status,`+creditSourceTupleColumns+`
 FROM credit_source_publication_members m JOIN credit_source_rows r ON r.source_row_id=m.source_row_id
 WHERE m.publication_id=? ORDER BY m.position LIMIT ?`, out.ID, CreditSourcePublicationMax+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Members = []CreditSourceMember{}
	for rows.Next() {
		var member CreditSourceMember
		args := []any{&member.SourceRowID, &member.Position, &member.Status}
		args = append(args, creditSourceTupleDest(&member.Original)...)
		if err = rows.Scan(args...); err != nil {
			return out, err
		}
		if len(out.Members) >= CreditSourcePublicationMax || !validAcceptedCreditTuple(member.Original) {
			return out, errors.New("invalid stored credit source member")
		}
		out.Members = append(out.Members, member)
	}
	return out, rows.Err()
}

const creditSourceTupleColumns = `r.original_record_reference,r.original_credit_id,r.original_provider_person_id,r.original_person_identity_key,r.original_person_token,r.original_target_kind,r.original_target_id,r.original_credited_name,r.original_role,r.original_department,r.original_source_ordinal`

func creditSourceTupleDest(t *AcceptedCreditTuple) []any {
	return []any{&t.RecordReference, &t.CreditID, &t.ProviderPersonID, &t.PersonIdentityKey, &t.PersonToken, &t.TargetKind, &t.TargetID, &t.CreditedName, &t.Role, &t.Department, &t.SourceOrdinal}
}

func creditSourceTupleDigest(provider string, tuple AcceptedCreditTuple) (string, error) {
	raw, err := json.Marshal(struct {
		Provider string
		Tuple    AcceptedCreditTuple
	}{provider, tuple})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func newCreditCustodyID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

// PublishCreditSourceTx appends private evidence only. It does NOT write
// catalog_credits, owner decisions, people, derived relations or public revisions.
// The caller owns authorization, accepted-record validation and the outer
// transaction. expectedRevision=0 means no source head has yet been accepted.
// A savepoint prevents callers that handle an error from committing partial
// source custody. No name/role/ordinal heuristic can rebind an original row.
func PublishCreditSourceTx(ctx context.Context, tx *sql.Tx, entity int64, provider, recordReference, observedAt string, expectedRevision int64, tuples []AcceptedCreditTuple) (out CreditSourcePublication, err error) {
	if entity <= 0 || !creditCustodyText(provider, 64, true) || !creditCustodyText(recordReference, CreditSourceRecordReferenceMax, true) || expectedRevision < 0 || !creditCustodyText(observedAt, 64, true) || len(tuples) > CreditSourcePublicationMax {
		return out, errors.New("invalid credit source publication")
	}
	for _, tuple := range tuples {
		if tuple.RecordReference != recordReference || !validAcceptedCreditTuple(tuple) {
			return out, errors.New("credit source record reference differs")
		}
	}
	if _, err = tx.ExecContext(ctx, `SAVEPOINT credit_source_publish`); err != nil {
		return out, err
	}
	defer func() {
		if err != nil {
			cleanup := context.WithoutCancel(ctx)
			if _, rollbackErr := tx.ExecContext(cleanup, `ROLLBACK TO credit_source_publish`); rollbackErr != nil {
				// If savepoint rollback cannot be established, retire the outer
				// transaction rather than leave partial evidence committable.
				err = errors.Join(err, rollbackErr, tx.Rollback())
				return
			}
			if _, releaseErr := tx.ExecContext(cleanup, `RELEASE credit_source_publish`); releaseErr != nil {
				err = errors.Join(err, releaseErr, tx.Rollback())
			}
		}
	}()
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM credit_source_heads WHERE entity_id=? AND provider=?`, entity, provider).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
		revision = 0
	}
	if err != nil {
		return out, err
	}
	if revision != expectedRevision {
		return out, ErrCreditSourceStale
	}
	if revision > 0 {
		// Sealed members are immutable, so a refresh that accepted exactly the
		// head's record and ordered tuples would only duplicate them. Keep the
		// head: its rows stay the ones owner overrides already reference.
		var head CreditSourcePublication
		head, err = ReadCreditSourceHead(ctx, tx, entity, provider)
		if err != nil {
			return out, err
		}
		if sameCreditSourcePublication(head, recordReference, tuples) {
			_, err = tx.ExecContext(ctx, `RELEASE credit_source_publish`)
			return head, err
		}
	}
	out = CreditSourcePublication{EntityID: entity, Provider: provider, RecordReference: recordReference, ObservedAt: observedAt, Revision: revision + 1, Members: []CreditSourceMember{}}
	out.ID, err = newCreditCustodyID()
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO credit_source_publications(publication_id,entity_id,provider,accepted_record_reference,observed_at,member_count) VALUES(?,?,?,?,?,?)`, out.ID, entity, provider, recordReference, observedAt, len(tuples)); err != nil {
		return out, err
	}
	counts := make(map[AcceptedCreditTuple]int, len(tuples))
	for _, tuple := range tuples {
		counts[tuple]++
	}
	// Cache candidate results before insertion, so duplicate incoming rows are
	// independent occurrences rather than evidence for each other.
	candidates := make(map[AcceptedCreditTuple][]string, len(counts))
	digests := make(map[AcceptedCreditTuple]string, len(counts))
	for tuple := range counts {
		var digest string
		digest, err = creditSourceTupleDigest(provider, tuple)
		if err != nil {
			return out, err
		}
		digests[tuple] = digest
		candidates[tuple], err = exactCreditSourceCandidates(ctx, tx, entity, provider, digest, tuple)
		if err != nil {
			return out, err
		}
	}
	for position, tuple := range tuples {
		ids := candidates[tuple]
		member := CreditSourceMember{Position: position, Original: tuple, Status: CreditSourceNew}
		if counts[tuple] == 1 && len(ids) == 1 {
			member.SourceRowID, member.Status = ids[0], CreditSourceExact
		} else {
			if counts[tuple] > 1 || len(ids) > 1 {
				member.Status = CreditSourceAmbiguous
			}
			member.SourceRowID, err = newCreditCustodyID()
			if err != nil {
				return out, err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO credit_source_rows(source_row_id,entity_id,provider,tuple_digest,original_record_reference,original_credit_id,original_provider_person_id,original_person_identity_key,original_person_token,original_target_kind,original_target_id,original_credited_name,original_role,original_department,original_source_ordinal) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, member.SourceRowID, entity, provider, digests[tuple], tuple.RecordReference, tuple.CreditID, tuple.ProviderPersonID, tuple.PersonIdentityKey, tuple.PersonToken, tuple.TargetKind, tuple.TargetID, tuple.CreditedName, tuple.Role, tuple.Department, tuple.SourceOrdinal)
			if err != nil {
				return out, err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO credit_source_publication_members(publication_id,entity_id,provider,position,source_row_id,reference_status) VALUES(?,?,?,?,?,?)`, out.ID, entity, provider, position, member.SourceRowID, string(member.Status))
		if err != nil {
			return out, err
		}
		out.Members = append(out.Members, member)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE credit_source_publications SET sealed=1 WHERE publication_id=? AND sealed=0`, out.ID); err != nil {
		return out, err
	}
	if revision == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO credit_source_heads(entity_id,provider,publication_id,revision) VALUES(?,?,?,?)`, entity, provider, out.ID, out.Revision)
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE credit_source_heads SET publication_id=?,revision=? WHERE entity_id=? AND provider=? AND revision=?`, out.ID, out.Revision, entity, provider, expectedRevision)
		if err == nil {
			var changed int64
			changed, err = result.RowsAffected()
			if err == nil && changed != 1 {
				err = ErrCreditSourceStale
			}
		}
	}
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `RELEASE credit_source_publish`)
	return out, err
}

// sameCreditSourcePublication reports whether head already holds exactly this
// accepted record with these tuples in this order.
func sameCreditSourcePublication(head CreditSourcePublication, recordReference string, tuples []AcceptedCreditTuple) bool {
	if head.RecordReference != recordReference || len(head.Members) != len(tuples) {
		return false
	}
	for i, member := range head.Members {
		if member.Position != i || member.Original != tuples[i] {
			return false
		}
	}
	return true
}

func exactCreditSourceCandidates(ctx context.Context, tx *sql.Tx, entity int64, provider, digest string, tuple AcceptedCreditTuple) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT r.source_row_id,`+creditSourceTupleColumns+` FROM credit_source_rows r WHERE r.entity_id=? AND r.provider=? AND r.tuple_digest=?
 AND EXISTS(SELECT 1 FROM credit_source_publication_members accepted_member JOIN credit_source_publications accepted_publication ON accepted_publication.publication_id=accepted_member.publication_id
 WHERE accepted_member.source_row_id=r.source_row_id AND accepted_publication.sealed=1)
 AND r.original_record_reference=? AND r.original_credit_id=? AND r.original_provider_person_id=? AND r.original_person_identity_key=? AND r.original_person_token=? AND r.original_target_kind=? AND r.original_target_id=? AND r.original_credited_name=? AND r.original_role=? AND r.original_department=? AND r.original_source_ordinal=? LIMIT 2`, entity, provider, digest, tuple.RecordReference, tuple.CreditID, tuple.ProviderPersonID, tuple.PersonIdentityKey, tuple.PersonToken, tuple.TargetKind, tuple.TargetID, tuple.CreditedName, tuple.Role, tuple.Department, tuple.SourceOrdinal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		var original AcceptedCreditTuple
		args := append([]any{&id}, creditSourceTupleDest(&original)...)
		if err = rows.Scan(args...); err != nil {
			return nil, err
		}
		// Hash equality is only a candidate lookup; every original scalar must match.
		if original == tuple {
			ids = append(ids, id)
			if len(ids) == 2 {
				break // two exact historical occurrences already establish ambiguity
			}
		}
	}
	return ids, rows.Err()
}

// Bounds preserve the union of existing domains: ScreenRecord 128 rows with
// 300/500/200 byte name/role/department; TVDB episode 512 byte name/character;
// repair relationship record/target IDs 256; provider publication code 64.
// Identity keys allow provider-prefix plus ID, or the accepted local name key.
// Validation never truncates or normalizes captured evidence.
func creditCustodyText(value string, maximum int, required bool) bool {
	if len(value) > maximum || !utf8.ValidString(value) || required && strings.TrimSpace(value) == "" {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) < 0
}

func validAcceptedCreditTuple(t AcceptedCreditTuple) bool {
	return creditCustodyText(t.RecordReference, CreditSourceRecordReferenceMax, true) && creditCustodyText(t.CreditID, 256, false) && creditCustodyText(t.ProviderPersonID, 256, false) && creditCustodyText(t.PersonIdentityKey, 512, false) && creditCustodyText(t.PersonToken, 256, false) && creditCustodyText(t.TargetKind, 64, false) && creditCustodyText(t.TargetID, 256, false) && creditCustodyText(t.CreditedName, CreditNameMaxBytes, false) && creditCustodyText(t.Role, CreditRoleMaxBytes, false) && creditCustodyText(t.Department, CreditDepartmentMaxBytes, false) && t.SourceOrdinal >= 0
}
