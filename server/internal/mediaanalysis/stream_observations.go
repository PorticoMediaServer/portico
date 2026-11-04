package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Packet peak is internal work under an existing deep grant, never a new
// public permission or an implication of the frame-only operation.
const packetPeakStage = "packet_peak"

func streamStage(stage string) bool {
	return stage == catalog.PlaybackFramesOperation || stage == packetPeakStage
}

type streamObservation struct {
	binding                          binding
	input                            catalog.InventoryStreamInput
	receipt                          compactcatalog.InfoAcceptReceipt
	accepted                         bool
	stage, grant, id, evidence, tool string
	retry                            bool
	abort                            error
}

func (s *Service) stageGrantTx(ctx context.Context, tx *sql.Tx, b binding, p catalog.ScanPolicy, stage string) (string, error) {
	if stage != packetPeakStage {
		if !p.Allows(stage) {
			return "", nil
		}
		allowed, err := sourceStageAllowedTx(ctx, tx, b.Source.ID, stage)
		if err != nil || !allowed {
			return "", err
		}
		return stage, nil
	}
	if !p.Allows("probe") {
		return "", nil
	}
	for _, op := range catalog.DeepScanOperations {
		if op == catalog.PlaybackFramesOperation || !p.Allows(op) {
			continue
		}
		allowed, err := sourceStageAllowedTx(ctx, tx, b.Source.ID, op)
		if err != nil {
			return "", err
		}
		if allowed {
			return op, nil
		}
	}
	return "", nil
}

func (s *Service) streamMarkerTx(ctx context.Context, tx *sql.Tx, b binding, stage string) (bool, error) {
	column := "playback_frames_revision"
	if stage == packetPeakStage {
		column = "packet_peak_revision"
	}
	var current bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM asset_stream_facts f JOIN asset_streams st ON st.asset_id=f.asset_id JOIN asset_stream_breadth br ON br.asset_id=st.asset_id AND br.stream_index=st.stream_index WHERE f.asset_id=? AND f.size=? AND f.modified_ns=? AND st.stream_index=(SELECT MIN(v.stream_index) FROM asset_streams v WHERE v.asset_id=f.asset_id AND v.type='video') AND br.`+column+`=? ORDER BY st.stream_index LIMIT 1)`, b.AssetID, b.SourceSize, b.SourceModifiedNS, s.stageAlgorithm(b, stage)).Scan(&current)
	return current, err
}

// runStreamStage borrows bytes only while probing. Pending publication retains
// at most the normalized stream set and exact receipt, never an open input.
func (s *Service) runStreamStage(ctx context.Context, b binding, stage, grant, id string, physical *os.File, read *ReadInvocation) (bool, string, error) {
	fail := func(err error) (bool, string, error) { return s.stageFailure(ctx, b, stage, err) }
	// Timed discs have their own family10/17/18 witnesses. STRM never falls
	// through to an ordinary path; its target admission remains independent.
	if b.TimedDisc || b.Container == "strm" || b.Container == "disc" || b.Container == "iso" || b.Container == "bluray" || b.Container == "dvd" || b.VideoCodec == "" {
		err := s.publish(ctx, b, stage, id, "", "", produced{Summary: map[string]any{"applicable": false}, grant: grant})
		return false, "", err
	}
	if len(s.streamPending) >= 32 {
		for key, old := range s.streamPending {
			if old.accepted {
				delete(s.streamPending, key)
				break
			}
			var running bool
			if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs j JOIN inventory_runs r ON r.job_id=j.id JOIN library_sources s ON s.id=r.source_id JOIN inventory_objects o ON o.id=? LEFT JOIN library_scan_policies p ON p.library_id=s.library_id WHERE j.id=? AND j.status='running' AND s.id=? AND s.enabled=1 AND s.generation=? AND s.incarnation=? AND r.source_generation=s.generation AND r.root_incarnation=s.incarnation AND r.root_identity=s.root_identity AND o.source_id=s.id AND o.revision=? AND o.root_incarnation=s.incarnation AND o.retired=0 AND o.state='available' AND COALESCE(p.revision,1)=?)`, old.binding.ObjectID, old.binding.JobID, old.binding.Source.ID, old.binding.Source.Generation, old.binding.Source.Incarnation, old.binding.SourceRevision, old.binding.PolicyRevision).Scan(&running); err != nil {
				return false, "", err
			}
			// A replaced job/object/source/policy witness is dead to source41 recovery.
			if !running {
				delete(s.streamPending, key)
			}
		}
		if len(s.streamPending) >= 32 {
			return false, "", nil
		}
	}
	captured, err := s.catalog.InventoryStreams(ctx, b.JobID, b.ObjectID, b.SourceRevision, b.PolicyRevision, grant)
	if errors.Is(err, compactcatalog.ErrInfoInputBusy) || errors.Is(err, compactcatalog.ErrInfoInputDeferred) {
		return false, "", nil
	}
	if err != nil {
		return fail(err)
	}
	video := -1
	for i := range captured.Streams {
		if captured.Streams[i].Type == "video" {
			video = i
			break
		}
	}
	if video < 0 {
		return fail(ErrUnsupported)
	}
	stream := &captured.Streams[video]
	current := stream.Detail.PlaybackFramesRevision == assets.PlaybackFramesRevision
	if stage == packetPeakStage {
		current = stream.Detail.PacketPeakRevision == assets.PacketPeakRevision
	}
	work := &streamObservation{binding: b, input: captured, stage: stage, grant: grant, id: id}
	if current {
		// A previous process applied source41 but lost its final stage ACK. The
		// canonical normalized marker, not ephemeral memory, proves completion.
		work.accepted = true
		work.receipt.State = compactcatalog.InfoInputApplied
		// The canonical recipe proves completion, but cannot reconstruct a lost tool digest.
	} else {
		if s.options.Probe == nil {
			return fail(ErrUnsupported)
		}
		if err := read.Retain(func(ctx context.Context, tx *sql.Tx) error {
			policy, e := catalog.ScanPolicyTx(ctx, tx, b.Source.LibraryID)
			if e != nil {
				return e
			}
			if policy.Revision != b.PolicyRevision || !policy.Allows(grant) {
				return context.Canceled
			}
			allowed, e := sourceStageAllowedTx(ctx, tx, b.Source.ID, grant)
			if e != nil {
				return e
			}
			if !allowed {
				return context.Canceled
			}
			return nil
		}, func() error { return nil }); err != nil {
			return fail(err)
		}
		input, err := s.options.Open(ctx, b.ItemID, b.AssetID)
		if err != nil {
			return fail(err)
		}
		defer input.Close()
		input = &authorityInput{Input: input, invocation: read}
		if input.Size() <= 0 || input.Size() > s.options.MaxInputBytes {
			return fail(ErrBudget)
		}
		if err = input.Validate(ctx); err != nil {
			return fail(err)
		}
		work.tool, err = toolHash(s.options.FFprobe)
		if err != nil {
			return fail(err)
		}
		// The same extent budget and retained input cover every subprocess.
		counted := &countedInput{Input: input, limit: s.options.MaxInputBytes * 4, progress: func(int64) {}}
		probe := s.options.Probe(counted)
		run := func(kind decoder.AnalysisProbeKind) ([]byte, error) {
			return probe(ctx, id, decoder.AnalysisProbeSpec{Kind: kind, PhysicalLock: physical})
		}
		if stage == catalog.PlaybackFramesOperation {
			raw, e := run(decoder.ProbeFrameCadence)
			if e != nil {
				return fail(e)
			}
			if e = assets.ApplyFrameCadence(raw, &stream.Detail); e != nil {
				return fail(e)
			}
			if assets.NeedsFrameHDR(*stream) {
				raw, e = run(decoder.ProbeFrameHDR)
				if e != nil {
					return fail(e)
				}
				if e = assets.ApplyFrameHDR(raw, &stream.Detail); e != nil {
					return fail(e)
				}
			}
			stream.Detail.PlaybackFramesRevision = assets.PlaybackFramesRevision
		} else {
			raw, e := run(decoder.ProbePacketPeak)
			if e != nil {
				return fail(e)
			}
			peak := assets.PacketPeak(raw)
			if peak <= 0 {
				return fail(assets.ErrPlaybackObservation)
			}
			stream.Detail.MaxBitRate = peak
			stream.Detail.PacketPeakRevision = assets.PacketPeakRevision
		}
		if err = input.Validate(ctx); err != nil {
			return fail(err)
		}
		tool, e := toolHash(s.options.FFprobe)
		if e != nil || tool != work.tool {
			return fail(ErrConflict)
		}
		work.evidence = input.Evidence()
	}
	s.streamPending[b.Request] = work
	return s.resumeStreamObservation(ctx, work)
}

func (s *Service) abortStreamObservation(ctx context.Context, work *streamObservation, cause error) (bool, string, error) {
	work.abort = cause
	if !work.accepted {
		if err := s.catalog.AbandonInventoryStreams(ctx, work.input); err != nil {
			return false, "", err
		}
	}
	delete(s.streamPending, work.binding.Request)
	return s.stageFailure(ctx, work.binding, work.stage, cause)
}

func terminalStreamPublication(err error) bool {
	return compactcatalog.InfoInputNeedsAbort(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrConflict) || errors.Is(err, storage.ErrRemoteAnalysisDisabled) || errors.Is(err, storage.ErrRemoteChanged)
}

func (s *Service) resumeStreamObservation(ctx context.Context, work *streamObservation) (bool, string, error) {
	if work.abort != nil {
		return s.abortStreamObservation(ctx, work, work.abort)
	}
	if work.retry {
		var state string
		var next int64
		err := s.db.QueryRowContext(ctx, `SELECT state,next_ms FROM analysis_stage_runs WHERE job_id=? AND object_id=? AND source_revision=? AND policy_revision=? AND stage=? AND algorithm=?`, work.binding.JobID, work.binding.ObjectID, work.binding.SourceRevision, work.binding.PolicyRevision, work.stage, s.stageWorkAlgorithm(work.binding, work.stage)).Scan(&state, &next)
		if err != nil {
			return false, "", err
		}
		if state == "failed" || state == "unsupported" {
			return s.abortStreamObservation(ctx, work, errors.New("stream publication retry budget exhausted"))
		}
		if next > nowMS() {
			return false, "", nil
		}
		_, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE analysis_stage_runs SET state='running',attempt=attempt+1,updated_ms=? WHERE job_id=? AND object_id=? AND source_revision=? AND policy_revision=? AND stage=? AND algorithm=? AND state='retry_wait' AND next_ms<=?`, nowMS(), work.binding.JobID, work.binding.ObjectID, work.binding.SourceRevision, work.binding.PolicyRevision, work.stage, s.stageWorkAlgorithm(work.binding, work.stage), nowMS())
		if err != nil {
			return false, "", err
		}
		work.retry = false
	}
	fail := func(err error) (bool, string, error) {
		if terminalStreamPublication(err) {
			return s.abortStreamObservation(ctx, work, err)
		}
		work.retry = true
		return s.stageFailure(ctx, work.binding, work.stage, err)
	}
	if !work.accepted {
		receipt, err := s.catalog.AcceptInventoryStreams(ctx, work.input)
		if errors.Is(err, compactcatalog.ErrInfoInputBusy) || errors.Is(err, compactcatalog.ErrInfoInputDeferred) || errors.Is(err, storage.ErrBusy) {
			return false, "", nil
		}
		if err != nil {
			return fail(err)
		}
		work.receipt, work.accepted = receipt, true
	}
	tx, close, err := dbwork.BeginRead(ctx, s.db)
	if err != nil {
		return false, "", err
	}
	applied, err := s.catalog.InventoryStreamsAppliedTx(ctx, tx, work.input, work.receipt)
	close()
	if err != nil {
		return fail(err)
	}
	if !applied {
		return false, "", nil
	}
	err = s.publish(ctx, work.binding, work.stage, work.id, work.evidence, work.tool, produced{Summary: map[string]any{"applicable": true}, streams: work, grant: work.grant})
	if err != nil {
		return fail(err)
	}
	delete(s.streamPending, work.binding.Request)
	return false, "", nil
}

func (s *Service) stageGrant(ctx context.Context, b binding, p catalog.ScanPolicy, stage string) (string, error) {
	tx, close, err := dbwork.BeginRead(ctx, s.db)
	if err != nil {
		return "", err
	}
	defer close()
	grant, err := s.stageGrantTx(ctx, tx, b, p, stage)
	if err == nil && grant == "" {
		err = context.Canceled
	}
	return grant, err
}
