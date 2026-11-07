package writer

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const mgGuideDiscardOperation stmt.ID = "mg.guide.discard.operation"

func mgGuideDiscardDefinitions() []stmt.Definition {
	txt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	return []stmt.Definition{{ID: mgGuideDiscardOperation, Kind: stmt.Write, SQL: `UPDATE live_operations INDEXED BY sqlite_autoindex_live_operations_1 SET status='superseded',last_command_sequence=?,stage_revision=? WHERE request_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND stage_revision=? AND status='staging'`, Params: []stmt.Param{i("next_sequence"), i("next_revision"), txt("request"), {Name: "incarnation", Type: stmt.Blob}, i("sequence"), i("ack"), i("revision")}, Owner: "writer.mg.guide.discard", CallSites: []string{"writer/mg_guide_discard.go"}, Cohorts: []stmt.Cohort{{Name: "staging", Bindings: []any{int64(2), int64(2), "guide-request", make([]byte, 16), int64(1), int64(1), int64(1)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operations_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "live_operations", Name: "sqlite_autoindex_live_operations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "request_id", Collation: "BINARY"}}}}}}}
}

// Logical withdrawal only. All immutable generation rows and physical pins
// remain. This component deliberately has no reclaim/delete implementation:
// the held actual external-session pin seam must close before that is safe.
func mgDiscardGuideNative(ctx context.Context, t *commandTurn, before mgGuideNative, identity CommandIdentity) (out mgGuideNativeResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgGuideNextNative(t, before, identity); err != nil {
		return out, err
	}
	actual, err := mgReadGuideNative(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if actual != before {
		return out, ErrConflict
	}
	head, err := mgGuideHeadFacts(ctx, t, before.progress.manifest.source)
	if err != nil {
		return out, err
	}
	if head.generation == before.progress.manifest.generation {
		return out, ErrConflict
	}
	applied, err := mgGuideNativeTime(ctx, t)
	if err != nil {
		return out, err
	}
	p := before.progress
	if err = mgGuidePublicationChange(ctx, t, mgGuideDiscardOperation, identity.Sequence, p.revision+1, before.request, before.incarnation[:], p.sequence, before.acknowledged, p.revision); err != nil {
		return out, err
	}
	want := before
	want.status = "superseded"
	want.progress.sequence = identity.Sequence
	want.progress.revision++
	post, err := mgReadGuideNative(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if post != want {
		return out, ErrConflict
	}
	return mgGuideNativeResult{post, head, applied}, nil
}
