package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type Mutation struct {
	Target
	ExpectedRevision int64  `json:"expectedRevision"`
	RequestID        string `json:"requestId"`
	Action           string `json:"action"`
	MarkerID         string `json:"markerId"`
	Kind             string `json:"kind"`
	Title            string `json:"title"`
	StartUS          string `json:"startUS"`
	EndUS            string `json:"endUS"`
}
type MutationResult struct {
	MarkerRevision int64  `json:"markerRevision"`
	MarkerID       string `json:"markerId"`
}

func (s *Service) Mutate(ctx context.Context, a Access, m Mutation) (MutationResult, error) {
	var out MutationResult
	if !a.Owner || a.Authority != "local" {
		return out, identity.ErrUnauthorized
	}
	if m.SourceRevision == "" || m.MappingRevision == "" || m.ExpectedRevision < 1 || len(m.RequestID) < 16 || len(m.RequestID) > 128 || !utf8.ValidString(m.Title) || len([]rune(m.Title)) > 120 {
		return out, ErrInput
	}
	switch m.Action {
	case "create", "edit", "approve", "dismiss":
	default:
		return out, ErrInput
	}
	a, release, e := s.Capture(ctx, a, m.Target)
	if e != nil {
		return out, e
	}
	defer release()
	gated, e := s.begin(ctx, a)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	v, e := s.resolve(ctx, tx, a, m.Target)
	if e != nil {
		return out, e
	}
	if v.NeedsProbe {
		return out, ErrConflict
	}
	actor := token(a.Authority, a.AccountID, a.ProfileID)
	digest := token(jsonString(m), v.ObjectID, actor)
	var priorDigest string
	e = tx.QueryRowContext(ctx, `SELECT mutation_digest,marker_revision,marker_id FROM analysis_marker_receipts WHERE actor=? AND request_id=?`, actor, m.RequestID).Scan(&priorDigest, &out.MarkerRevision, &out.MarkerID)
	if e == nil {
		if priorDigest != digest {
			return MutationResult{}, ErrConflict
		}
		return out, gated.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	_, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO analysis_marker_sets(object_id,source_revision) VALUES(?,?)`, v.ObjectID, v.Revision)
	if e != nil {
		return out, e
	}
	var revision int64
	e = tx.QueryRowContext(ctx, `SELECT revision FROM analysis_marker_sets WHERE object_id=? AND source_revision=?`, v.ObjectID, v.Revision).Scan(&revision)
	if e != nil {
		return out, e
	}
	if revision != m.ExpectedRevision {
		return out, ErrConflict
	}
	id := m.MarkerID
	if m.Action == "create" || m.Action == "edit" {
		switch m.Kind {
		case "intro", "recap", "credits", "commercial", "outro", "chapter":
		default:
			return out, ErrInput
		}
		x, xe := parseUS(m.StartUS)
		y, ye := parseUS(m.EndUS)
		if xe != nil || ye != nil || x >= y || y > v.End-v.Start {
			return out, ErrInput
		}
		title := strings.TrimSpace(m.Title)
		if title == "" {
			title = m.Kind
		}
		if m.Action == "create" {
			if m.MarkerID != "" {
				return out, ErrInput
			}
			var count int
			e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM analysis_markers WHERE object_id=? AND source_revision=? AND source_binding=? AND deleted=0`, v.ObjectID, v.Revision, v.SourceBinding).Scan(&count)
			if e != nil {
				return out, e
			}
			if count >= 512 {
				return out, ErrBudget
			}
			id = token(v.ObjectID, v.Revision, v.SourceBinding, actor, m.RequestID)
			_, e = tx.ExecContext(ctx, `INSERT INTO analysis_markers(id,object_id,source_revision,source_binding,kind,start_us,end_us,confidence,provenance,result_id,approved,edited,title) VALUES(?,?,?,?,?,?,?,1,'owner:manual','',1,1,?)`, id, v.ObjectID, v.Revision, v.SourceBinding, m.Kind, v.Start+x, v.Start+y, title)
		} else {
			var result sql.Result
			result, e = tx.ExecContext(ctx, `UPDATE analysis_markers SET kind=?,start_us=?,end_us=?,title=?,approved=1,edited=1 WHERE id=? AND object_id=? AND source_revision=? AND source_binding=? AND deleted=0`, m.Kind, v.Start+x, v.Start+y, title, id, v.ObjectID, v.Revision, v.SourceBinding)
			if e == nil {
				n, _ := result.RowsAffected()
				if n != 1 {
					e = ErrConflict
				}
			}
		}
	} else {
		var result sql.Result
		result, e = tx.ExecContext(ctx, `UPDATE analysis_markers SET approved=?,deleted=?,edited=1 WHERE id=? AND object_id=? AND source_revision=? AND source_binding=? AND deleted=0 AND start_us>=? AND end_us<=?`, m.Action == "approve", m.Action == "dismiss", id, v.ObjectID, v.Revision, v.SourceBinding, v.Start, v.End)
		if e == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				e = ErrConflict
			}
		}
	}
	if e != nil {
		return out, e
	}
	next := revision + 1
	_, e = tx.ExecContext(ctx, `UPDATE analysis_marker_sets SET revision=? WHERE object_id=? AND source_revision=? AND revision=?`, next, v.ObjectID, v.Revision, revision)
	if e != nil {
		return out, e
	}
	// Keep the original detector confidence/provenance on edits. History records
	// the owner's action separately; approval never rewrites an inference as fact.
	_, e = tx.ExecContext(ctx, `INSERT INTO analysis_marker_history(object_id,source_revision,revision,actor,mutation_json,created_ms) VALUES(?,?,?,?,?,?)`, v.ObjectID, v.Revision, next, actor, jsonString(m), nowMS())
	if e != nil {
		return out, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO analysis_marker_receipts(actor,request_id,mutation_digest,marker_revision,marker_id,created_ms) VALUES(?,?,?,?,?,?)`, actor, m.RequestID, digest, next, id, nowMS())
	if e != nil {
		return out, e
	}
	out = MutationResult{next, id}
	return out, gated.Commit()
}

// MarkerTime converts only API microseconds; it never accepts a float clock.
func MarkerTime(us int64) string { return fmt.Sprint(us) }
