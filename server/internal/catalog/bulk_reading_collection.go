package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func readingCollectionDenied(err error) bool {
	return errors.Is(err, ErrReadingProjectionChanged) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrContentRestricted) || errors.Is(err, identity.ErrNotVisible)
}
func (s *Service) authorizeReadingCollection(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) error {
	if !identity.CanUsePersonalState(p) || s.BulkCommands.ReadingCollectionAuthorize == nil {
		return identity.ErrForbidden
	}
	if err := s.BulkCommands.ReadingCollectionAuthorize(ctx, tx, p, item); err != nil {
		return err
	}
	// Personal eligibility alone does not prove that this title still has an
	// admitted publication. Reuse the Reading producer's exact current format,
	// publication/link and ready-generation domain at execution and receipt too.
	clone := *s.WithContext(ctx)
	clone.readTx = tx
	var library string
	if err := tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities i JOIN catalog_libraries cl ON cl.id=i.library_id WHERE i.public_id=pid_blob(?) AND i.retired=0 AND cl.retired=0 AND i.kind IN(20,21)`, item).Scan(&library); err != nil {
		return err
	}
	scope, err := clone.readingBrowseScope(library)
	if err != nil {
		return err
	}
	if scope == nil {
		return sql.ErrNoRows
	}
	var eligible bool
	if err = tx.QueryRowContext(ctx, `SELECT `+scope.eligibleSQL("i.id")+` FROM catalog_entities i WHERE i.public_id=pid_blob(?)`, item).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return sql.ErrNoRows
	}
	return nil
}

// At most 200 explicit public identities. Read only their Reading libraries and
// use the existing ready-publication/format predicate; never widen a query job.
func (s *Service) explicitReadingCollectionSQL(p identity.Principal, v Viewer, ids []string) (string, error) {
	tx := dbwork.Snapshot(s.Context())
	if tx == nil {
		tx = s.readTx
	}
	if tx == nil {
		return "", errors.New("Reading selection requires a snapshot")
	}
	rows, err := tx.QueryContext(s.Context(), `SELECT pid(e.public_id),cl.library_id FROM json_each(?) picked JOIN catalog_entities e ON e.public_id=pid_blob(picked.value) JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.kind IN(20,21) AND e.retired=0 AND cl.retired=0`, idsJSON(ids))
	if err != nil {
		return "", err
	}
	type member struct{ id, library string }
	members := []member{}
	for rows.Next() {
		var m member
		if err = rows.Scan(&m.id, &m.library); err != nil {
			break
		}
		members = append(members, m)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return "", err
	}
	admitted := map[string]bool{}
	clauses := []string{}
	for _, m := range members {
		if !v.AllowsLibrary(m.library) {
			return "", sql.ErrNoRows
		}
		if err = s.authorizeReadingCollection(s.Context(), tx, p, m.id); err != nil {
			return "", err
		}
		if admitted[m.library] {
			continue
		}
		admitted[m.library] = true
		scope, err := s.readingBrowseScope(m.library)
		if err != nil {
			return "", err
		}
		if scope == nil {
			return "", sql.ErrNoRows
		}
		clauses = append(clauses, scope.eligibleSQL("i.id"))
	}
	if len(clauses) == 0 {
		return "0", nil
	}
	return strings.Join(clauses, " OR "), nil
}

// Receipt visibility uses current Reading authority for only this job set's
// distinct Reading leaves. Retry jobs retain the original <=200 selection.
func (s *Service) readingCollectionReceiptDenied(ctx context.Context, tx *sql.Tx, p identity.Principal, jobs []string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT pid(e.public_id) FROM json_each(?) chosen JOIN personal_job_membership_outcomes o ON o.job_id=chosen.value JOIN catalog_entities e ON e.public_id=o.item_public_id WHERE e.kind IN(20,21) ORDER BY e.public_id LIMIT 201`, idsJSON(jobs))
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > 200 {
		return nil, jobInvalid("Reading receipt exceeds explicit selection limit")
	}
	denied := []string{}
	for _, id := range ids {
		if err = s.authorizeReadingCollection(ctx, tx, p, id); err != nil {
			if !readingCollectionDenied(err) {
				return nil, err
			}
			denied = append(denied, id)
		}
	}
	return denied, nil
}
