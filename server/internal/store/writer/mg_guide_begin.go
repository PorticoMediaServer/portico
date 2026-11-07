package writer

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Already parsed/frozen owned input. Calling this private effect supplies no
// actor/source authority, admission, original receipt resolution or COMMIT.
type mgGuideBirth struct {
	request, input, name string
	tuners               int64
	manifest             mgGuideManifest
	identity             CommandIdentity
	requestDigest        RequestDigest
}

const (
	mgGuideInsertIdentity   stmt.ID = "mg.guide.begin.identity"
	mgGuideInsertGeneration stmt.ID = "mg.guide.begin.generation"
	mgGuideInsertOperation  stmt.ID = "mg.guide.begin.operation"
	mgGuideCapacity         stmt.ID = "mg.guide.source.capacity"
)

func mgGuideBeginDefinitions() []stmt.Definition {
	p := func(n string, typ stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: typ} }
	t, i, b := stmt.Text, stmt.Integer, stmt.Blob
	defs := []stmt.Definition{
		{ID: mgGuideInsertIdentity, Kind: stmt.Write, SQL: `INSERT INTO live_source_identities(id) VALUES(?)`, Params: []stmt.Param{p("source", t)}},
		{ID: mgGuideInsertGeneration, Kind: stmt.Write, SQL: `INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES(?,?,?,?,?,?,?)`, Params: []stmt.Param{p("generation", t), p("source", t), p("name", t), p("tuners", i), p("channels", i), p("programmes", i), p("created", t)}},
		{ID: mgGuideInsertOperation, Kind: stmt.Write, SQL: `INSERT INTO live_operations(request_id,input_hash,source_id,generation_id,expected_revision,status,command_incarnation,create_operation_id,birth_request_digest,expected_generation,expected_choice_count,manifest_digest,stage_digest) VALUES(?,?,?,?,?,'staging',?,?,?,?,?,?,?)`, Params: []stmt.Param{p("request", t), p("input", t), p("source", t), p("generation", t), p("expected_revision", i), p("incarnation", b), p("operation", b), p("birth_digest", b), p("expected_generation", t), p("choices", i), p("manifest", b), p("initial", b)}},
		// Existing product cap is 64 sources. Read at most 64 constant cells,
		// through the PK range; no text hydration or full-table aggregation.
		{ID: mgGuideCapacity, Kind: stmt.Read, SQL: `SELECT 1 FROM live_sources INDEXED BY sqlite_autoindex_live_sources_1 WHERE id>=? ORDER BY id LIMIT 64`, Params: []stmt.Param{p("first", t)}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_sources_1"}, Seeks: []string{"id>?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "live_sources", Name: "sqlite_autoindex_live_sources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}},
	}
	for j := range defs {
		d := &defs[j]
		d.Owner = "writer.mg.guide.begin"
		d.CallSites = []string{"writer/mg_guide_begin.go", "writer/mg_guide_publication_effects.go"}
		args := make([]any, len(d.Params))
		for k, p := range d.Params {
			switch p.Type {
			case stmt.Text:
				args[k] = "missing"
			case stmt.Integer:
				args[k] = int64(1)
			case stmt.Blob:
				args[k] = make([]byte, 32)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing-native-keys", Bindings: args}}
	}
	return defs
}
func mgCheckGuideCapacity(ctx context.Context, t *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	step, err := mgGuidePublicationStep(t, mgGuideCapacity)
	if err != nil {
		return err
	}
	rows, err := t.query(ctx, step, "")
	if err != nil {
		return err
	}
	var count int64
	for rows.Next() {
		var unit int64
		if err = rows.Scan(&unit); err != nil {
			break
		}
		if unit != 1 {
			err = ErrConflict
			break
		}
		count++
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if count >= 64 {
		return ErrConflict
	}
	return nil
}
func mgBeginGuideNative(ctx context.Context, t *commandTurn, b mgGuideBirth) (out mgGuideNativeResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	p := mgGuideProgress{manifest: b.manifest, revision: 1, sequence: 1}
	p.chain = mgGuideInitialChain(p.manifest)
	if !validMGGuideProgress(p) || b.manifest.expectedChain == ([32]byte{}) || !mgGuideReceiptText(b.request, true, 256) || !mgGuideReceiptText(b.input, true, 256) || !mgGuideReceiptText(b.name, true, 256) || !mgGuideReceiptText(b.manifest.source, true, 256) || !mgGuideReceiptText(b.manifest.generation, true, 256) || b.tuners < 0 || b.tuners > 256 || b.identity.Sequence != 1 || b.identity.Incarnation == ([16]byte{}) || b.identity.OperationID == ([16]byte{}) || b.requestDigest == (RequestDigest{}) {
		return out, ErrInvalid
	}
	if err = mgCheckGuideReceiptBirth(ctx, t, b.request); err != nil {
		return out, err
	}
	row, err := mgGuidePublicationQuery(ctx, t, mgGuideIdentity, b.manifest.source)
	if err != nil {
		return out, err
	}
	var removed int64
	err = row.Scan(&removed)
	if errors.Is(err, sql.ErrNoRows) {
		err = mgGuidePublicationChange(ctx, t, mgGuideInsertIdentity, b.manifest.source)
	}
	if err != nil {
		return out, err
	}
	if removed != 0 {
		return out, ErrDisclosureDenied
	}
	head, err := mgGuideCurrentHead(ctx, t, b.manifest.source)
	if err != nil {
		return out, err
	}
	if head.revision != b.manifest.expectedRevision || head.generation != b.manifest.previousGeneration {
		return out, ErrConflict
	}
	if head.revision == 0 {
		if err = mgCheckGuideCapacity(ctx, t); err != nil {
			return out, err
		}
	}
	applied, err := mgGuideNativeTime(ctx, t)
	if err != nil {
		return out, err
	}
	created := time.UnixMilli(applied).UTC().Format("2006-01-02T15:04:05.000Z")
	m := b.manifest
	if err = mgGuidePublicationChange(ctx, t, mgGuideInsertGeneration, m.generation, m.source, b.name, b.tuners, m.expected[0], m.expected[1], created); err != nil {
		return out, err
	}
	if err = mgGuidePublicationChange(ctx, t, mgGuideInsertOperation, b.request, b.input, m.source, m.generation, m.expectedRevision, b.identity.Incarnation[:], b.identity.OperationID[:], b.requestDigest[:], m.previousGeneration, m.expected[2], m.expectedChain[:], p.chain[:]); err != nil {
		return out, err
	}
	post, err := mgReadGuideNative(ctx, t, b.request)
	if err != nil {
		return out, err
	}
	want := mgGuideNative{request: b.request, input: b.input, name: b.name, createdAt: created, status: "staging", tuner: b.tuners, progress: p, incarnation: b.identity.Incarnation, createOperation: b.identity.OperationID, birthDigest: [32]byte(b.requestDigest)}
	if post != want {
		return out, ErrConflict
	}
	return mgGuideNativeResult{post, head, applied}, nil
}
