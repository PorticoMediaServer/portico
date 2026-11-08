package writer

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// One copy family's closed contribution, registered from its OWN file so no
// family edits a shared hunk: its turn body, its statements and their
// upper-bound uses inside one copy turn.
type basicCopyFamilySpec struct {
	copy    func(context.Context, *commandTurn, basicCopyTurn) error
	defs    func() []stmt.Definition
	maxUses func(stmt.ID) int
}

var basicCopyFamilies = map[int64]basicCopyFamilySpec{}

// Called only from package-level declarations; a duplicate or incomplete
// registration panics at init, never at runtime.
func registerBasicCopyFamily(family int64, spec basicCopyFamilySpec) bool {
	if family < 1 || family > 9 || spec.copy == nil || spec.defs == nil || spec.maxUses == nil {
		panic(fmt.Sprintf("writer: invalid Basic copy family %d", family))
	}
	if _, dup := basicCopyFamilies[family]; dup {
		panic(fmt.Sprintf("writer: Basic copy family %d registered twice", family))
	}
	basicCopyFamilies[family] = spec
	return true
}

func basicCopyFamilyOrder() []int64 {
	out := make([]int64, 0, len(basicCopyFamilies))
	for f := range basicCopyFamilies {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func basicCopyFamilyDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for _, f := range basicCopyFamilyOrder() {
		defs = append(defs, basicCopyFamilies[f].defs()...)
	}
	return defs
}

// The largest use any ONE registered family makes of id in one copy turn.
func basicCopyFamilyMaxUses(id stmt.ID) int {
	n := 0
	for _, f := range basicCopyFamilyOrder() {
		n = max(n, basicCopyFamilies[f].maxUses(id))
	}
	return n
}

// The exact copy cursor a copy turn enters with and leaves behind. It mirrors
// catalog_info_changes copy_family/copy_phase/copy_id/copy_aux/copy_key{,2,3}.
type basicCopyCursor struct {
	family, phase, id, aux int64
	key, key2, key3        string
}

func (c basicCopyCursor) args() []any {
	return []any{c.family, c.phase, c.id, c.aux, c.key, c.key2, c.key3}
}

// The fixed header facts of one copy turn, from the frozen request.
type basicCopyTurn struct {
	source         basicStreamFactStage
	changeRevision int64 // header job_revision on entry
	entityID       int64 // after_entity == upper_entity
	shapeID        int64
	entry          basicCopyCursor
}

func (c basicCopyTurn) headerArgs() []any {
	s := c.source
	args := []any{s.changeID, s.assetID, s.assetToken, s.expectedEpoch, s.inputRevision, s.witnessFingerprint[:], c.changeRevision, c.entityID, c.entityID}
	args = append(args, c.entry.args()...)
	return append(args, c.shapeID)
}

// checkBasicCopyEntry proves the input is in the copy phase at exactly the
// requested entry cursor. Every copy family body calls this first.
func checkBasicCopyEntry(ctx context.Context, turn *commandTurn, c basicCopyTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if ctx == nil || turn == nil || c.changeRevision <= 0 || c.changeRevision == math.MaxInt64 || c.entityID <= 0 || c.shapeID <= 0 ||
		c.entry.family < 0 || c.entry.family > 9 || c.entry.phase < 0 || c.entry.id < 0 || c.entry.aux < 0 || c.entry.aux > 1 {
		return ErrInvalid
	}
	if err = checkBasicInputAtPhase(ctx, turn, c.source, 2); err != nil {
		return err
	}
	row, err := basicStageRead(ctx, turn, basicCopyHeader, c.headerArgs()...)
	if err != nil {
		return err
	}
	var id int64
	if err = row.Scan(&id); err != nil {
		return basicStageError(err)
	}
	if id != c.source.changeID {
		return ErrConflict
	}
	return nil
}

// advanceBasicCopyCursor is the ONE terminal CAS of a copy turn. phase is 2
// while copying and 3 only when the last masked family completes.
func advanceBasicCopyCursor(ctx context.Context, turn *commandTurn, c basicCopyTurn, phase int64, next basicCopyCursor) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if (phase != 2 && phase != 3) || (phase == 3 && next != (basicCopyCursor{})) || next.family < 0 || next.family > 9 || next.aux < 0 || next.aux > 1 {
		return ErrInvalid
	}
	args := append([]any{phase}, next.args()...)
	args = append(args, c.changeRevision+1)
	row, err := basicStageRead(ctx, turn, basicCopyCursorCAS, append(args, c.headerArgs()...)...)
	if err != nil {
		return err
	}
	var got basicCopyCursor
	var gotPhase, revision int64
	if err = row.Scan(&gotPhase, &got.family, &got.phase, &got.id, &got.aux, &got.key, &got.key2, &got.key3, &revision); err != nil {
		return basicStageError(err)
	}
	if gotPhase != phase || got != next || revision != c.changeRevision+1 {
		return ErrConflict
	}
	return nil
}
