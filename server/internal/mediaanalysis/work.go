package mediaanalysis

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type artifactOutput struct {
	Object         mediaartifact.Object
	RetainedKey    string
	Kind, MIME     string
	Ordinal        int
	StartUS, EndUS int64
	Width, Height  int
}
type produced struct {
	Summary   any
	Artifacts []artifactOutput
	Markers   []Marker
	Sonic     []sonicItemAnalysis
}

// RunOne is a single stage quantum of the EXISTING durable inventory job. It
// never owns a second job scheduler. A false done leaves the scan pending.
func (s *Service) RunOne(ctx context.Context, request Request) (done bool, warning string, err error) {
	select {
	case s.slot <- struct{}{}:
		defer func() { <-s.slot }()
	default:
		return false, "", nil
	}
	physical, err := s.physical.Lock(livechannels.Allocation{ID: analysisLockID("analysis-producer"), Generation: 1})
	if errors.Is(err, livechannels.ErrPhysicalBusy) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	defer physical.Close()
	if err = s.cleanStaging(); err != nil {
		return false, "", err
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return false, "", err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	b, err := s.bindingTx(ctx, tx, request)
	if err != nil {
		return false, "", err
	}
	if !b.Consent.Allowed {
		return true, "", nil
	}
	ctx, read := s.scanReadContext(ctx, b)
	defer read.Close()
	var input Input
	if b.Container == "strm" {
		if err = gated.Commit(); err != nil {
			return false, "", err
		}
		var ready bool
		input, ready, warning, err = s.verifySTRM(ctx, b)
		if err != nil || !ready {
			return warning != "", warning, err
		}
		defer input.Close()
		gated, err = dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
		if err != nil {
			return false, "", err
		}
		tx = gated.Tx()
		defer gated.Rollback()
		b, err = s.bindingTx(ctx, tx, request)
		if err != nil {
			return false, "", err
		}
		if input.Evidence() != b.ProbeEvidence {
			return false, "", ErrConflict
		}
	}
	p, err := catalog.ScanPolicyTx(ctx, tx, b.Source.LibraryID)
	if err != nil {
		return false, "", err
	}
	// due says whether a stage is next for this file: allowed, not done, not
	// given up on, and not waiting for a retry. wait is true for the last.
	due := func(op string) (ready, wait bool, code string, err error) {
		if !p.Allows(op) {
			return false, false, "", nil
		}
		// The source's own page can switch this whole-file stage off for the
		// files found through it; execution follows that, not only enqueueing.
		if allowed, e := sourceStageAllowedTx(ctx, tx, b.Source.ID, op); e != nil {
			return false, false, "", e
		} else if !allowed {
			return false, false, "", nil
		}
		var complete bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM analysis_heads h JOIN analysis_results r ON r.id=h.result_id WHERE h.object_id=? AND h.source_revision=? AND h.stage=? AND r.algorithm=? AND r.root_incarnation=? AND r.configuration_generation=? AND r.retired_ms=0 AND (?='' OR r.evidence=?))`, request.ObjectID, request.SourceRevision, op, s.stageAlgorithm(b, op), b.Source.Incarnation, b.Source.Generation, b.ProbeEvidence, b.ProbeEvidence).Scan(&complete)
		if err != nil || complete {
			return false, false, "", err
		}
		var state string
		var attempt int
		var next int64
		err = tx.QueryRowContext(ctx, `SELECT state,attempt,next_ms,error_code FROM analysis_stage_runs WHERE job_id=? AND object_id=? AND source_revision=? AND policy_revision=? AND stage=? AND algorithm=?`, request.JobID, request.ObjectID, request.SourceRevision, request.PolicyRevision, op, s.stageWorkAlgorithm(b, op)).Scan(&state, &attempt, &next, &code)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, false, "", err
		}
		if state == "failed" || state == "unsupported" {
			return false, false, code, nil
		}
		if next > nowMS() {
			return false, true, "", nil
		}
		return true, false, "", nil
	}
	stage := ""
	for _, op := range catalog.DeepScanOperations {
		if op == catalog.SonicOperation && !s.SonicCanAttempt() {
			continue
		}
		// Episode artwork has its own metadata publication/CAS and runs through
		// ingestion's same durable job after these immutable analysis stages.
		if op == catalog.EpisodeThumbnailOperation {
			continue
		}
		ready, wait, code, e := due(op)
		if e != nil {
			return false, "", e
		}
		if code != "" {
			warning = code
		}
		if wait {
			return false, "", nil
		}
		if ready {
			stage = op
			break
		}
	}
	if stage == "" {
		return true, warning, gated.Commit()
	}
	if s.Admission != nil {
		task := "analysis"
		if stage == "trickplay" {
			task = "trickplay"
		}
		allowed, e := s.Admission(ctx, tx, task)
		if e != nil {
			return false, "", e
		}
		if !allowed {
			return false, "", nil
		}
	}
	// Complete and media-derived Custom stages require Basic on this exact
	// inventory revision. A checksum-only Custom request needs no media probe.
	if stage != "checksum" && b.BasicRevision != b.SourceRevision {
		return false, "", ErrConflict
	}
	// One decode of the audio serves every audio stage that is due with this one.
	stages := []string{stage}
	var group []string
	if isSharedAudioStage(stage) {
		group = sharedAudioStages
	}
	if group != nil {
		for _, op := range group {
			if op == stage {
				continue
			}
			ready, _, _, e := due(op)
			if e != nil {
				return false, "", e
			}
			if ready {
				stages = append(stages, op)
			}
		}
	}
	// The gate is applied again here, in the same snapshot that just selected the
	// stages, so a stage disabled between selection and capture never runs.
	kept := stages[:0]
	for _, op := range stages {
		allowed, e := sourceStageAllowedTx(ctx, tx, b.Source.ID, op)
		if e != nil {
			return false, "", e
		}
		if allowed {
			kept = append(kept, op)
		}
	}
	if len(kept) == 0 || kept[0] != stage {
		return true, warning, gated.Commit()
	}
	stages = kept
	// Restart-safe output identity is recorded by the durable job + source/policy
	// stage attempt. A new attempt cannot overwrite a retired result, including
	// target A → B → A. Acquisition evidence is never inferred from this identity.
	publications := map[string]string{}
	for _, op := range stages {
		var attempt int
		err = tx.QueryRowContext(ctx, `INSERT INTO analysis_stage_runs(job_id,object_id,source_revision,policy_revision,stage,algorithm,state,attempt,updated_ms) VALUES(?,?,?,?,?,?,'running',1,?) ON CONFLICT(job_id,object_id,source_revision,policy_revision,stage,algorithm) DO UPDATE SET state='running',attempt=attempt+1,processed_bytes=0,error_code='',next_ms=0,updated_ms=excluded.updated_ms RETURNING attempt`, request.JobID, request.ObjectID, request.SourceRevision, request.PolicyRevision, op, s.stageWorkAlgorithm(b, op), nowMS()).Scan(&attempt)
		if err != nil {
			return false, "", err
		}
		publications[op] = token(request.JobID, request.ObjectID, request.SourceRevision, fmt.Sprint(request.PolicyRevision), op, s.stageWorkAlgorithm(b, op), fmt.Sprint(attempt))
	}
	if err = gated.Commit(); err != nil {
		return false, "", err
	}
	// Every stage of this run fails, or is put back, together.
	fail := func(cause error) (bool, string, error) {
		var first error
		for _, op := range stages {
			if _, _, e := s.stageFailure(ctx, b, op, cause); e != nil && first == nil {
				first = e
			}
		}
		return false, "", first
	}
	if input == nil {
		input, err = s.options.Open(ctx, b.ItemID, b.AssetID)
		if err != nil {
			return fail(err)
		}
		defer input.Close()
	}
	input = &authorityInput{Input: input, invocation: read}
	if input.Size() <= 0 || input.Size() > s.options.MaxInputBytes {
		return fail(ErrBudget)
	}
	if err = input.Validate(ctx); err != nil {
		return fail(err)
	}
	counted := &countedInput{Input: input, limit: s.options.MaxInputBytes * 4, progress: func(n int64) {
		_, _ = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE analysis_stage_runs SET processed_bytes=?,updated_ms=? WHERE job_id=? AND object_id=? AND source_revision=? AND policy_revision=? AND stage=? AND algorithm=? AND state='running'`, n, nowMS(), request.JobID, request.ObjectID, request.SourceRevision, request.PolicyRevision, stage, s.stageWorkAlgorithm(b, stage))
	}}
	tool := "builtin-sha256"
	if stage != "checksum" {
		tool, err = toolHash(s.options.FFmpeg)
		if err != nil {
			return fail(err)
		}
	}
	var used int64
	if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM (SELECT MAX(size) AS size FROM analysis_artifacts GROUP BY digest)`).Scan(&used); err != nil {
		return fail(err)
	}
	if used >= s.cacheLimit(ctx) {
		return fail(ErrBudget)
	}
	runner := s.options.Decoder(counted)
	decode := func(ctx context.Context, key string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
		spec.PhysicalLock = physical
		return runner(ctx, key, spec, consume)
	}
	results := map[string]produced{}
	started := time.Now()
	switch {
	case stage == catalog.SonicOperation:
		results[stage], err = s.produceSonic(ctx, b, decode, publications[stage], physical)
	case isSharedAudioStage(stage):
		results, err = s.produceSharedAudio(ctx, b, stages, decode, publications)
	default:
		results[stage], err = s.produce(ctx, b, stage, counted, decode, publications[stage])
	}
	defer func() {
		for _, result := range results {
			for _, a := range result.Artifacts {
				if a.RetainedKey != "" {
					_ = s.artifacts.RemoveRetained(a.RetainedKey)
				}
			}
		}
	}()
	if err != nil {
		return fail(err)
	}
	if err = input.Validate(ctx); err != nil {
		return fail(err)
	}
	if stage != "checksum" {
		current, e := toolHash(s.options.FFmpeg)
		if e != nil || current != tool {
			return fail(ErrConflict)
		}
	}
	counted.progress(counted.total.Load())
	for _, op := range stages {
		if err = s.publish(ctx, b, op, publications[op], input.Evidence(), tool, results[op]); err != nil {
			return fail(err)
		}
		if op == catalog.SonicOperation {
			s.recordSonicProgress(b.Source.LibraryID, len(results[op].Sonic), time.Since(started))
		}
	}
	return false, "", nil
}

type countedInput struct {
	Input
	total    atomic.Int64
	last     atomic.Int64
	limit    int64
	progress func(int64)
}

func (c *countedInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	if length < 1 || length > 1<<20 || c.total.Add(length) > c.limit {
		return nil, ErrBudget
	}
	b, e := c.Input.ReadExtent(ctx, offset, length)
	now := nowMS()
	old := c.last.Load()
	if now-old >= 1000 && c.last.CompareAndSwap(old, now) {
		c.progress(c.total.Load())
	}
	return b, e
}
func (s *Service) stageFailure(ctx context.Context, b binding, stage string, cause error) (bool, string, error) {
	state, code := "retry_wait", "analysis_failed"
	if errors.Is(cause, ErrUnsupported) || errors.Is(cause, sonic.ErrUnavailable) || errors.Is(cause, decoder.ErrConfinementUnavailable) || errors.Is(cause, decoder.ErrInvalidConfiguration) {
		state, code = "unsupported", "decoder_or_stream_unsupported"
	}
	if errors.Is(cause, ErrBudget) {
		state, code = "failed", "analysis_budget_exceeded"
	}
	// Preemption, cancelled jobs and changed policy do not consume a failure retry.
	interrupted := errors.Is(ctx.Err(), context.Canceled) || errors.Is(cause, context.Canceled) || errors.Is(cause, storage.ErrBusy)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(cause, context.DeadlineExceeded) {
		state, code = "failed", "analysis_time_budget_exceeded"
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if interrupted {
		_, e := dbwork.ExecWrite(cleanup, s.db, dbwork.ClassBackgroundMedia, `UPDATE analysis_stage_runs SET state='pending',attempt=MAX(0,attempt-1),updated_ms=? WHERE job_id=? AND object_id=? AND source_revision=? AND policy_revision=? AND stage=? AND algorithm=?`, nowMS(), b.JobID, b.ObjectID, b.SourceRevision, b.PolicyRevision, stage, s.stageWorkAlgorithm(b, stage))
		return false, "", e
	}
	if errors.Is(cause, ErrConflict) {
		code = "analysis_source_or_markers_changed"
	}
	_, e := dbwork.ExecWrite(cleanup, s.db, dbwork.ClassBackgroundMedia, `UPDATE analysis_stage_runs SET state=CASE WHEN attempt>=3 AND ?='retry_wait' THEN 'failed' ELSE ? END,error_code=?,next_ms=?+MIN(300000,1000*(1<<MIN(attempt,8))),updated_ms=? WHERE job_id=? AND object_id=? AND source_revision=? AND policy_revision=? AND stage=? AND algorithm=?`, state, state, code, nowMS(), nowMS(), b.JobID, b.ObjectID, b.SourceRevision, b.PolicyRevision, stage, s.stageWorkAlgorithm(b, stage))
	return false, "", e
}
func (s *Service) publish(ctx context.Context, b binding, stage, id, evidence, tool string, out produced) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.publication.Lock()
	defer s.publication.Unlock()
	release, e := s.sealArtifacts(ctx, &out)
	if e != nil {
		return e
	}
	defer release()
	gated3, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	if e = checkReadPublicationTx(ctx, tx); e != nil {
		return e
	}
	current, e := s.bindingTx(ctx, tx, b.Request)
	if e != nil {
		return e
	}
	if current.Consent != b.Consent || current.AssetID != b.AssetID || current.Source.Incarnation != b.Source.Incarnation || current.Source.Generation != b.Source.Generation || current.DurationUS != b.DurationUS || current.ProbeEvidence != b.ProbeEvidence || b.Container == "strm" && evidence != b.ProbeEvidence || stage == catalog.SonicOperation && current.SonicBinding != b.SonicBinding {
		return ErrConflict
	}
	p, e := catalog.ScanPolicyTx(ctx, tx, b.Source.LibraryID)
	if e != nil {
		return e
	}
	if !p.Allows(stage) {
		return context.Canceled
	}
	// Publication is the last boundary: a source page change during a long
	// decode must not publish a stage it has since switched off.
	if allowed, e := sourceStageAllowedTx(ctx, tx, current.Source.ID, stage); e != nil {
		return e
	} else if !allowed {
		return context.Canceled
	}
	if len(out.Markers) > 0 {
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM analysis_markers WHERE object_id=? AND source_revision=? AND source_binding=? AND deleted=0`, b.ObjectID, b.SourceRevision, b.SourceBinding).Scan(&n); e != nil {
			return e
		}
		if n+len(out.Markers) > 512 {
			return ErrBudget
		}
	}
	if len(out.Markers) > 0 && current.MarkerRevision != b.MarkerRevision {
		return ErrConflict
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO analysis_results(id,object_id,asset_id,source_revision,root_incarnation,configuration_generation,policy_revision,stage,algorithm,evidence,tool_digest,duration_us,summary_json,created_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, b.ObjectID, b.AssetID, b.SourceRevision, b.Source.Incarnation, b.Source.Generation, b.PolicyRevision, stage, s.stageAlgorithm(b, stage), evidence, tool, b.DurationUS, jsonString(out.Summary), nowMS()); e != nil {
		return e
	}
	if stage == "loudness" {
		if e = persistFullLoudness(ctx, tx, id, out.Summary); e != nil {
			return e
		}
	}
	if stage == catalog.SonicOperation {
		if e = persistSonic(ctx, tx, id, out.Sonic); e != nil {
			return e
		}
	}
	for _, a := range out.Artifacts {
		if _, e = tx.ExecContext(ctx, `INSERT INTO analysis_artifacts(id,result_id,digest,size,kind,mime,ordinal,start_us,end_us,width,height) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, token(id, fmt.Sprint(a.Ordinal), a.Kind), id, a.Object.Digest, a.Object.Size, a.Kind, a.MIME, a.Ordinal, a.StartUS, a.EndUS, a.Width, a.Height); e != nil {
			return e
		}
	}
	// Replace only the active head. Previous immutable bytes remain until retention
	// and reader leases permit deletion; source edits never rewrite their identity.
	if _, e = tx.ExecContext(ctx, `UPDATE analysis_results SET retired_ms=? WHERE id IN(SELECT result_id FROM analysis_heads WHERE object_id=? AND source_revision=? AND stage=?) AND retired_ms=0`, nowMS(), b.ObjectID, b.SourceRevision, stage); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO analysis_heads(object_id,source_revision,stage,result_id) VALUES(?,?,?,?) ON CONFLICT(object_id,source_revision,stage) DO UPDATE SET result_id=excluded.result_id`, b.ObjectID, b.SourceRevision, stage, id); e != nil {
		return e
	}
	for _, m := range out.Markers {
		// Detector identity is stable across retries and policy changes. An edited or
		// dismissed candidate is never resurrected by a new detector publication.
		mid := token(b.ObjectID, b.SourceRevision, b.SourceBinding, m.Kind, m.StartUS, m.EndUS, m.Provenance)
		if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO analysis_markers(id,object_id,source_revision,source_binding,kind,start_us,end_us,confidence,provenance,result_id,title) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, mid, b.ObjectID, b.SourceRevision, b.SourceBinding, m.Kind, m.start(), m.end(), m.Confidence, m.Provenance, id, m.Title); e != nil {
			return e
		}
	}
	if len(out.Markers) > 0 {
		if _, e = tx.ExecContext(ctx, `UPDATE analysis_marker_sets SET revision=revision+1 WHERE object_id=? AND source_revision=?`, b.ObjectID, b.SourceRevision); e != nil {
			return e
		}
	}
	var raw string
	if e = tx.QueryRowContext(ctx, `SELECT analysis_operations_json FROM inventory_objects WHERE id=?`, b.ObjectID).Scan(&raw); e != nil {
		return e
	}
	var ops []string
	_ = json.Unmarshal([]byte(raw), &ops)
	found := false
	for _, v := range ops {
		found = found || v == stage
	}
	if !found {
		ops = append(ops, stage)
	}
	if _, e = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_operations_json=?,analysis_revision=?,analysis_error='' WHERE id=? AND revision=?`, jsonString(ops), b.SourceRevision, b.ObjectID, b.SourceRevision); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE analysis_stage_runs SET state='complete',error_code='',updated_ms=? WHERE job_id=? AND object_id=? AND source_revision=? AND policy_revision=? AND stage=? AND algorithm=?`, nowMS(), b.JobID, b.ObjectID, b.SourceRevision, b.PolicyRevision, stage, s.stageWorkAlgorithm(b, stage)); e != nil {
		return e
	}
	return gated3.Commit()
}

// sealArtifacts seals every artifact a stage staged and checks the cache
// budget with them counted. The caller holds s.publication and keeps the
// returned release (which closes the object gates) until its rows commit.
func (s *Service) sealArtifacts(ctx context.Context, out *produced) (func(), error) {
	// Obtain every distinct gate before sealing any object. Fail-fast acquisition
	// cannot deadlock other publishers/readers; physical contention is a retry,
	// not evidence that the generated media is invalid.
	gates := map[string]*os.File{}
	release := func() {
		for _, gate := range gates {
			_ = gate.Close()
		}
	}
	fail := func(e error) (func(), error) { release(); return nil, e }
	for _, a := range out.Artifacts {
		if gates[a.Object.Digest] != nil {
			continue
		}
		gate, err := s.custody.exclusive(a.Object.Digest)
		if errors.Is(err, livechannels.ErrPhysicalBusy) {
			return fail(storage.ErrBusy)
		}
		if err != nil {
			return fail(err)
		}
		gates[a.Object.Digest] = gate
	}
	for i := range out.Artifacts {
		a := &out.Artifacts[i]
		if a.RetainedKey == "" {
			return fail(ErrInput)
		}
		writer, e := s.artifacts.ResumeRetained(ctx, a.RetainedKey, a.Object.Size, a.Object.Size)
		if e != nil {
			return fail(e)
		}
		checkpoint, e := writer.Checkpoint()
		if e != nil || checkpoint != a.Object {
			writer.Abort()
			if e != nil {
				return fail(e)
			}
			return fail(ErrConflict)
		}
		object, e := writer.Seal(ctx)
		if e != nil {
			writer.Abort()
			return fail(e)
		}
		a.Object = object
	}
	var stored int64
	if e := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM (SELECT MAX(size) AS size FROM analysis_artifacts GROUP BY digest)`).Scan(&stored); e != nil {
		return fail(e)
	}
	added := map[string]bool{}
	for _, a := range out.Artifacts {
		if added[a.Object.Digest] {
			continue
		}
		added[a.Object.Digest] = true
		var existing bool
		if e := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM analysis_artifacts WHERE digest=?)`, a.Object.Digest).Scan(&existing); e != nil {
			return fail(e)
		}
		if !existing {
			stored += a.Object.Size
		}
	}
	if stored > s.cacheLimit(ctx) {
		return fail(ErrBudget)
	}
	return release, nil
}
