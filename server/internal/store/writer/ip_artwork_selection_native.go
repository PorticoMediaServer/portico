package writer

import (
	"context"
	"encoding/json"
	"strconv"
)

func ipArtSelectionArgs(o ipArtworkDiscoveryOwner, role, choice string, v ipArtworkSelectionValue) []any {
	kind, subject := ipArtNamespace(o)
	return []any{kind, subject, role, choice, v.Revision, v.Digest, v.Provider, v.ImageID, v.Origin, v.Attribution, v.Locale, v.Language, v.SourceFence, v.Actor, ipArtOptionalText(v.ObjectID), ipArtOptionalText(v.ObjectRevision)}
}
func ipArtCheckSelection(ctx context.Context, t *commandTurn, o ipArtworkDiscoveryOwner, role, choice string, v ipArtworkSelectionValue) error {
	if !ipArtSelectionValue(v) {
		return ErrInvalid
	}
	if !v.Present {
		kind, subject := ipArtNamespace(o)
		return ipArtAbsent(ctx, t, ipAMSelectionAbsent, kind, subject, role, choice)
	}
	return ipArtOne(ctx, t, ipAMSelectionExact, ipArtSelectionArgs(o, role, choice, v)...)
}
func ipArtSameSelection(a, b ipArtworkSelectionValue) bool {
	// Observation time/revision/actor do not make a repeated choice a new effect.
	a.Revision, b.Revision = 0, 0
	a.Actor, b.Actor = "", ""
	return a == b
}
func ipArtSelection(ctx context.Context, t *commandTurn, k CommandID, q ipArtworkMaterialRequest, now int64, r *ipArtworkMaterialResult) error {
	s, o := q.Selection, q.Owner
	if e := ipArtCheckSelection(ctx, t, o, s.Role, s.Choice, s.Before); e != nil {
		return e
	}
	if e := ipArtCheckSelection(ctx, t, o, s.Role, "locked", s.Locked); e != nil {
		return e
	}
	r.Role, r.Choice = s.Role, s.Choice
	r.Previous = s.Before.Digest
	r.Selected = s.After.Digest
	r.SelectionRevision = s.Before.Revision
	if r.SelectionRevision == 0 {
		r.SelectionRevision = o.sourceRevision
	}
	if ipArtSameSelection(s.Before, s.After) {
		return nil
	}
	// A matched observation may update only the matched row. The independent
	// locked row is never cleared, overwritten or assigned a child copy.
	if s.After.Present {
		if e := ipArtOne(ctx, t, ipAMSourceReady, s.After.Digest); e != nil {
			// Upload publication is an atomic material+locked selection acceptance.
			if k != IPArtworkUploadAccept {
				return e
			}
		}
	}
	if s.Choice == "matched" && s.After.Present {
		// A ready source is content custody, not proof that this provider candidate
		// is current. Match the native completed image job, immutable publication
		// manifest and ready discovery observation under CURRENT source/policy.
		m, j, a := q.Material, q.Job, s.After
		if s.CandidateStageID <= 0 || m.ID != j.ID || a.Digest != m.SourceDigest || j.State != 4 || j.Role != s.Role || j.Choice != s.Choice || j.SelectionRevision != s.Before.Revision || a.ImageID != j.ImageID || a.Origin != j.Origin || a.Attribution != j.Attribution || a.Locale != j.Locale || a.SourceFence != j.SourceFence || a.ObjectID != j.ObjectID || a.ObjectRevision != j.ObjectRevision {
			return ErrConflict
		}
		if e := ipArtOne(ctx, t, ipAMJob, ipArtJobArgs(q)...); e != nil {
			return e
		}
		state, e := ipArtVerifyIntent(ctx, t, q)
		if e != nil {
			return e
		}
		if state != ipArtPublish {
			return ErrConflict
		}
		if e = ipArtOne(ctx, t, ipAMCandidate, s.CandidateOrdinal, s.CandidateStageID, o.id, o.incarnation[:], q.Policy.Provider, o.sourceIncarnation[:], o.sourceRevision, q.Policy.Revision, q.Policy.Language, s.Role, a.ImageID, a.Origin, a.Locale, a.Attribution); e != nil {
			return e
		}
	}
	if k == IPArtworkUploadAccept {
		// The old/locked selection snapshots were already checked above. Complete
		// material publication with this SAME job/source/policy/selection fence.
		mr := ipArtworkMaterialResult{}
		if e := ipArtMaterial(ctx, t, IPArtworkMaterialPublish, q, now, &mr); e != nil {
			return e
		}
		r.MaterialID, r.StorageRevision = mr.MaterialID, mr.StorageRevision
		r.Action = ipArtPublish
	}
	if s.After.Present {
		if e := ipArtOne(ctx, t, ipAMSourceReady, s.After.Digest); e != nil {
			return e
		}
	}
	// Legacy audit JSON is history only. Current choices remain normalized; GC
	// uses exact history-artwork FKs and never searches serialized text.
	type audit struct {
		Role, Choice string
		Value        ipArtworkSelectionValue
	}
	beforeAudit, e := json.Marshal(audit{s.Role, s.Choice, s.Before})
	if e != nil {
		return e
	}
	afterAudit, e := json.Marshal(audit{s.Role, s.Choice, s.After})
	if e != nil {
		return e
	}
	kind, subject := ipArtNamespace(o)
	at := strconv.FormatInt(now, 10)
	if !s.After.Present {
		if e := ipArtWrite(ctx, t, ipAMSelectionDelete, kind, subject, s.Role, s.Choice, s.Before.Revision, s.Before.Digest); e != nil {
			return e
		}
	} else if !s.Before.Present {
		args := append(ipArtSelectionArgs(o, s.Role, s.Choice, s.After), at)
		if e := ipArtWrite(ctx, t, ipAMSelectionCreate, args...); e != nil {
			return e
		}
	} else {
		a := s.After
		args := []any{a.Revision, a.Digest, a.Provider, a.ImageID, a.Origin, a.Attribution, a.Locale, a.Language, a.SourceFence, a.Actor, ipArtOptionalText(a.ObjectID), ipArtOptionalText(a.ObjectRevision), at, kind, subject, s.Role, s.Choice, s.Before.Revision, s.Before.Digest}
		if e := ipArtWrite(ctx, t, ipAMSelectionUpdate, args...); e != nil {
			return e
		}
	}
	bump := ipAMEntityBump
	if kind == "person" {
		bump = ipAMPersonBump
	}
	if e := ipArtWrite(ctx, t, bump, subject, o.sourceIncarnation[:], o.sourceRevision); e != nil {
		return e
	}
	row, e := ipArtRead(ctx, t, ipAMHistory, kind, subject, strconv.FormatInt(o.sourceRevision, 10), strconv.FormatInt(o.sourceRevision+1, 10), ipArtCommandName(k), s.Actor, at, string(beforeAudit), string(afterAudit))
	if e != nil {
		return e
	}
	if e = row.Scan(&r.HistoryID); e != nil {
		return e
	}
	if r.HistoryID <= 0 {
		return ErrConflict
	}
	if s.Before.Present {
		if e = ipArtWrite(ctx, t, ipAMHistoryPin, r.HistoryID, s.Role, s.Choice, s.Before.Digest); e != nil {
			return e
		}
	}
	if s.After.Present && s.After.Digest != s.Before.Digest {
		if e = ipArtWrite(ctx, t, ipAMHistoryPin, r.HistoryID, s.Role, s.Choice, s.After.Digest); e != nil {
			return e
		}
	}
	r.SelectionRevision = s.Before.Revision + 1
	r.Count = 1
	return nil
}
