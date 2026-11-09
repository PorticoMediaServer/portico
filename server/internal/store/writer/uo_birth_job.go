package writer

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/mutationreceipt"
)

// uoBirthJobSource is the narrow local MP-20 seam. MP-20 owns item/source/recipe
// permission, selection validation, the outside-SQL encode work and the actual
// prepared-job row/worker-owner birth. UO receives only the exact native
// identity it must bind beside the public preparedmedia.Job: the item PK and
// stored public id, its catalogue kind/library, and the selected source
// revision. No MP semantics, recipe rules or worker lease are invented here.
type uoBirthJobSource struct {
	item           int64
	public         [16]byte
	library        int64
	key            string
	kind           int64
	sourceRevision string
}

func (uoBirthJobSource) Format(s fmt.State, _ rune) { formatUSPreparation(s, "uoBirthJobSource") }

func (s uoBirthJobSource) valid() bool {
	if s.item <= 0 || s.library <= 0 || s.kind <= 0 || s.public == [16]byte{} {
		return false
	}
	for _, v := range []string{s.key, s.sourceRevision} {
		if len(v) == 0 || len(v) > 256 || !utf8.ValidString(v) || strings.IndexByte(v, 0) >= 0 {
			return false
		}
	}
	return true
}

type uoBirthJobLookup struct {
	turn     *commandTurn
	root     uoRootLookup
	source   uoBirthJobSource
	original uoBirthJobResult
	saved    bool
}

func (uoBirthJobLookup) Format(s fmt.State, _ rune) { formatUSPreparation(s, "uoBirthJobLookup") }

// UO owns this user-root original. The root and command owner must already be
// registered; a missing retry owner never becomes a new birth. MP-20 owns the
// prepared-job row birth and independent worker owner, in this SAME frame after
// Fresh; no job row or mutable head is consulted to reconstruct a replay.
func lookupUOBirthJob(ctx context.Context, turn *commandTurn, a *usCurrentSessionAuthorization, q uoRootReference) (out *uoBirthJobLookup, err error) {
	defer func() { uoPublicReadingPoison(turn, err) }()
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.birthJob != nil || q.kind != 740 || !mutationreceipt.ValidRootID(q.root) || !q.validFor(turn) {
		return nil, ErrInvalid
	}
	proof, err := bindUSCurrentUORoot(ctx, turn, a)
	if err != nil {
		return nil, err
	}
	binding, err := bindUORoot(turn, nil, proof)
	if err != nil {
		return nil, err
	}
	out = &uoBirthJobLookup{turn: turn, root: uoRootLookup{turn: turn, binding: &binding, reference: q}}
	turn.transaction.birthJob = out
	root, err := lookupUORootCommand(ctx, turn, &binding, q)
	if err != nil {
		return nil, err
	}
	out.root = root
	if root.decision == receiptReplay {
		payload, digest, e := readUORootPayload(ctx, turn, root)
		if e != nil {
			return nil, e
		}
		out.original, err = decodeUOBirthJob(q.identity, q.request, q.root, payload, digest)
		if err != nil {
			return nil, err
		}
		if root.resultCount != 0 || root.appliedMS != out.original.appliedMS {
			return nil, ErrResolveRequired
		}
		out.saved = true
	} else if root.decision == receiptRetired {
		out.saved = true
	} else if root.decision != receiptFresh {
		return nil, ErrConflict
	}
	turn.transaction.birthJob = out
	return out, nil
}

// This finalizer is not a prepared-job constructor. MP-20 must create the
// actual selected row and independent worker owner after Fresh. Here exact
// native birth facts, the immutable user original, and root counters co-commit;
// ignored failures/unsaved Fresh cannot cross the physical terminal.
func persistUOBirthJob(ctx context.Context, turn *commandTurn, lookup *uoBirthJobLookup, src uoBirthJobSource, v uoBirthJobResult) (out ResultDigest, err error) {
	defer func() { uoPublicReadingPoison(turn, err) }()
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if lookup == nil || lookup.turn != turn || turn == nil || turn.transaction == nil || turn.transaction.birthJob != lookup || lookup.saved || lookup.root.decision != receiptFresh {
		return out, ErrInvalid
	}
	q := lookup.root.reference
	if err = preflightUOBirthJob(v); err != nil {
		return out, err
	}
	if !src.valid() || entityid.Encode(src.public[:]) != v.itemID || v.root != q.root || v.worker == q.identity.Incarnation {
		return out, ErrConflict
	}
	lookup.source = src
	out, err = persistUORootCanonical(ctx, turn, lookup.root, v)
	if err != nil {
		return out, err
	}
	lookup.original = v
	lookup.saved = true
	return out, nil
}

func (t *transaction) checkUOBirthJobCommit() error {
	v := t.birthJob
	if v == nil {
		return nil
	}
	if v.turn == nil || v.turn.transaction != t || !v.saved {
		return ErrConflict
	}
	if err := v.root.binding.check(t.ctx, v.turn); err != nil {
		return err
	}
	state, err := v.root.binding.root(t.ctx, v.turn, v.root.reference.root)
	if err != nil {
		return err
	}
	current, err := lookupUORootCommand(t.ctx, v.turn, v.root.binding, v.root.reference)
	if err != nil {
		return err
	}
	if v.root.decision == receiptRetired {
		if current.decision != receiptRetired {
			return ErrConflict
		}
		return nil
	}
	if current.decision != receiptReplay {
		return ErrConflict
	}
	payload, digest, err := readUORootPayload(t.ctx, v.turn, current)
	if err != nil {
		return err
	}
	original, err := decodeUOBirthJob(v.root.reference.identity, v.root.reference.request, v.root.reference.root, payload, digest)
	if err != nil || original != v.original {
		return ErrConflict
	}
	if v.root.decision == receiptFresh {
		if current.owner.rows != v.root.owner.rows+1 || current.owner.variableBytes != v.root.owner.variableBytes+current.variableBytes || current.owner.revision != v.root.owner.revision+1 {
			return ErrConflict
		}
		if state != "active" {
			return ErrUORootGone
		}
		return checkUOBirthJobNative(t.ctx, v.turn, v, v.original)
	}
	return nil
}

func checkUOBirthJobNative(ctx context.Context, turn *commandTurn, lookup *uoBirthJobLookup, v uoBirthJobResult) error {
	if turn == nil || turn.transaction == nil || lookup == nil || lookup.root.binding == nil || lookup.root.binding.current == nil || lookup.root.binding.current.session == nil || !lookup.source.valid() || entityid.Encode(lookup.source.public[:]) != v.itemID {
		return ErrInvalid
	}
	a := lookup.root.binding.current.session
	i := a.proof.witness.issuer
	now := turn.transaction.writer.now().UnixMilli()
	if v.createdMS < turn.transaction.start.UnixMilli() || v.updatedMS < v.createdMS || v.appliedMS < v.updatedMS || v.appliedMS > now {
		return ErrConflict
	}
	if err := lookup.root.binding.check(ctx, turn); err != nil {
		return err
	}
	row, err := wc008DraftQuery(ctx, turn, uoBirthJobNative,
		v.id, lookup.source.item, lookup.source.public[:], lookup.source.kind, lookup.source.library, lookup.source.key, lookup.source.sourceRevision,
		v.sourceID, v.profileID, v.targetID, v.state, v.phase, v.generation, v.bytes, v.errorCode, v.revision, v.createdMS, v.updatedMS, v.versionID,
		v.worker[:], lookup.root.reference.root, i.memberIncarnation, i.profileLifetime)
	if err != nil {
		return err
	}
	var public []byte
	if err = row.Scan(&public); err != nil {
		return err
	}
	if entityid.Encode(public) != v.itemID {
		return ErrConflict
	}
	return nil
}
