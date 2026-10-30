package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const (
	genreNextTarget = iota
	genreNextMember
	genreMemberPolicy
	genreOwnTerms
	genreInheritedTerms
	genreAddCandidate
)

type genreSourceCursor struct {
	CandidateRole    int
	CandidateScan    genreCandidateCursor
	Phase            int
	Target           int64
	TargetKind       int
	Member           int64
	MemberAfter      bool
	MemberSource     string
	LastMember       int64
	HadMember        bool
	EmptyFallback    bool
	Subject          int64
	TermAfter        bool
	Provider, Source string
	PendingValue     string
	Resume           int
	Policy           FacetBaseItemPolicyCursor
}

type searchGenreProducer struct{}

func (searchGenreProducer) ValidatePublication(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot) error {
	if !s.Scope.valid() || s.Scope.Field != "genre" {
		return ErrFacetBaseInput
	}
	if _, err := genreGrants(s.Scope.Source); err != nil {
		return err
	}
	if err := validateGenreWitness(ctx, tx, s); err != nil {
		return err
	}
	// This is shared library metadata, accepted only with a coherent genre BASE.
	// The enclosing Step checks the global ledger AFTER this first-row charge
	// and BEFORE COMPLETE/activation; capacity failure rolls back all of them.
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_search_genre_accepted_targets(library_id,max_entity_id)
 VALUES(?,max(0,COALESCE((SELECT id FROM catalog_entities INDEXED BY catalog_entities_library_id WHERE library_id=? ORDER BY id DESC LIMIT 1),0)))
 ON CONFLICT(library_id) DO UPDATE SET max_entity_id=excluded.max_entity_id
 WHERE excluded.max_entity_id>catalog_search_genre_accepted_targets.max_entity_id`, s.Scope.LibraryID, s.Scope.LibraryID)
	return err
}
func (p searchGenreProducer) Next(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot, cursor string) (FacetBaseBatch, error) {
	if err := validateGenreWitness(ctx, tx, s); err != nil {
		return FacetBaseBatch{}, err
	}
	restrictions, err := parseFacetBasePolicy(s.Scope.Policy)
	if err != nil {
		return FacetBaseBatch{}, err
	}
	grants, err := genreGrants(s.Scope.Source)
	if err != nil {
		return FacetBaseBatch{}, err
	}
	var c genreSourceCursor
	if cursor != "" && json.Unmarshal([]byte(cursor), &c) != nil {
		return FacetBaseBatch{}, ErrFacetBaseInput
	}
	out := FacetBaseBatch{}
	switch c.Phase {
	case genreNextTarget:
		var target int64
		var kind int
		var retired bool
		err = tx.QueryRowContext(ctx, `SELECT id,kind,retired FROM catalog_entities INDEXED BY catalog_entities_library_id WHERE library_id=? AND id>? ORDER BY id LIMIT 1`, s.Scope.LibraryID, c.Target).Scan(&target, &kind, &retired)
		if errors.Is(err, sql.ErrNoRows) {
			out.Done = true
			break
		}
		if err != nil {
			return out, err
		}
		c.Target = target
		if retired || kind != 1 && kind != 2 {
			break
		}
		var browseKind int
		err = tx.QueryRowContext(ctx, `SELECT kind FROM catalog_browse_rows WHERE entity_id=? AND library_id=?`, target, s.Scope.LibraryID).Scan(&browseKind)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return out, err
		}
		if browseKind != 1 && browseKind != 2 {
			break
		}
		c.TargetKind = browseKind
		c.Member = 0
		c.MemberAfter = false
		c.MemberSource = ""
		c.LastMember = 0
		c.HadMember = false
		c.EmptyFallback = false
		c.Phase = genreNextMember
	case genreNextMember:
		query := `SELECT item_id,source FROM catalog_browse_memberships WHERE entity_id=?`
		args := []any{c.Target}
		if c.MemberAfter {
			query += ` AND (item_id,source)>(?,?)`
			args = append(args, c.Member, c.MemberSource)
		}
		query += ` ORDER BY item_id,source LIMIT 1`
		var item int64
		var source string
		err = tx.QueryRowContext(ctx, query, args...).Scan(&item, &source)
		if errors.Is(err, sql.ErrNoRows) {
			if !c.HadMember {
				c.EmptyFallback = true
				c.Member = c.Target
				c.Subject = c.Target
				c.Phase = genreOwnTerms
				c.TermAfter = false
				c.Provider = ""
				c.Source = ""
			} else {
				c.Phase = genreNextTarget
			}
			break
		}
		if err != nil {
			return out, err
		}
		c.HadMember = true
		c.MemberAfter = true
		c.Member, c.MemberSource = item, source
		if item == c.LastMember {
			break
		}
		c.LastMember = item
		var library int64
		var public string
		var retired, libretired bool
		err = tx.QueryRowContext(ctx, `SELECT e.library_id,l.library_id,e.retired,l.retired FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE e.id=?`, item).Scan(&library, &public, &retired, &libretired)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return out, err
		}
		allowed := false
		for _, g := range grants {
			if g.ID == library && g.Library == public {
				allowed = true
				break
			}
		}
		if retired || libretired || !allowed {
			break
		}
		c.Policy = FacetBaseItemPolicyCursor{Item: item}
		c.Phase = genreMemberPolicy
	case genreMemberPolicy:
		done, visible, err := StepFacetBaseItemPolicy(ctx, tx, restrictions, &c.Policy)
		if err != nil {
			return out, err
		}
		if done {
			c.Phase = genreNextMember
			if visible {
				c.Subject = c.Member
				c.Phase = genreOwnTerms
				c.TermAfter = false
				c.Provider = ""
				c.Source = ""
			}
		}
	case genreOwnTerms, genreInheritedTerms:
		query := `SELECT s.provider,s.source_id,t.vocab,COALESCE(s.label_override,t.label) FROM catalog_term_sources s JOIN catalog_terms t ON t.id=s.term_id WHERE s.entity_id=?`
		args := []any{c.Subject}
		if c.TermAfter {
			query += ` AND (s.provider,s.source_id)>(?,?)`
			args = append(args, c.Provider, c.Source)
		}
		query += ` ORDER BY s.provider,s.source_id LIMIT 1`
		var provider, source string
		var vocab int
		var value sql.NullString
		err = tx.QueryRowContext(ctx, query, args...).Scan(&provider, &source, &vocab, &value)
		if errors.Is(err, sql.ErrNoRows) {
			if c.Phase == genreOwnTerms {
				var show int64
				err = tx.QueryRowContext(ctx, `SELECT show_id FROM catalog_episodes WHERE entity_id=?`, c.Member).Scan(&show)
				if err == nil {
					c.Subject = show
					c.TermAfter = false
					c.Provider = ""
					c.Source = ""
					c.Phase = genreInheritedTerms
					break
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return out, err
				}
			}
			c.Phase = genreNextMember
			if c.EmptyFallback {
				c.Phase = genreNextTarget
			}
			break
		}
		if err != nil {
			return out, err
		}
		c.TermAfter = true
		c.Provider, c.Source = provider, source
		if vocab != 1 || !value.Valid || value.String == "" {
			break
		}
		var last int64
		err = tx.QueryRowContext(ctx, `SELECT r.last_target FROM catalog_search_genre_ready r JOIN catalog_facet_base_values v ON v.id=r.value_id WHERE r.base_id=? AND v.library_id=? AND v.field='genre' AND v.value=?`, s.ID, s.Scope.LibraryID, value.String).Scan(&last)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if last == c.Target {
			break
		}
		f, err := incrementFacetBaseFact(ctx, tx, s, value.String, 1)
		if err != nil {
			return out, err
		}
		out.Facts = []FacetBaseFact{f}
		c.PendingValue = value.String
		c.Resume = c.Phase
		c.Phase = genreAddCandidate
	case genreAddCandidate:
		var id int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM catalog_facet_base_values WHERE library_id=? AND field='genre' AND value=?`, s.Scope.LibraryID, c.PendingValue).Scan(&id); err != nil {
			return out, err
		}
		if c.CandidateRole == 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_search_genre_ready(base_id,value_id,last_target) VALUES(?,?,?) ON CONFLICT(base_id,value_id) DO UPDATE SET last_target=excluded.last_target`, s.ID, id, c.Target); err != nil {
				return out, err
			}
			c.CandidateRole = 1
		}
		done, err := stepGenreCandidate(ctx, tx, s.ID, id, c.Target, c.TargetKind, c.CandidateRole, &c.CandidateScan)
		if err != nil {
			return out, err
		}
		if done {
			c.CandidateRole++
			c.CandidateScan = genreCandidateCursor{}
			if c.CandidateRole > 2 {
				c.CandidateRole = 0
				c.PendingValue = ""
				c.Phase = c.Resume
			}
		}
	default:
		return out, ErrFacetBaseInput
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return out, err
	}
	out.Cursor = string(raw)
	return out, nil
}
