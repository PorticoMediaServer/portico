package mediaanalysis

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

type Result struct {
	Stage           string          `json:"stage"`
	Algorithm       string          `json:"algorithm"`
	OriginAssurance string          `json:"originAssurance"`
	CreatedMS       int64           `json:"createdMS"`
	Summary         json.RawMessage `json:"summary"`
	ArtifactID      string          `json:"artifactId,omitempty"`
}
type Preview struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	StartUS string `json:"startUS"`
	EndUS   string `json:"endUS"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}
type StageJob struct {
	JobID          string `json:"jobId"`
	JobStatus      string `json:"jobStatus"`
	Stage          string `json:"stage"`
	State          string `json:"state"`
	Attempt        int    `json:"attempt"`
	ProcessedBytes string `json:"processedBytes"`
	ErrorCode      string `json:"errorCode"`
	UpdatedMS      int64  `json:"updatedMS"`
}
type SourceChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type View struct {
	Sources        []SourceChoice     `json:"sources"`
	Scope          Scope              `json:"scope"`
	Source         Source             `json:"source"`
	CanManage      bool               `json:"canManage"`
	Policy         catalog.ScanPolicy `json:"policy"`
	Budgets        map[string]any     `json:"budgets"`
	Results        []Result           `json:"results"`
	Markers        []Marker           `json:"markers"`
	MarkerRevision int64              `json:"markerRevision"`
	Previews       []Preview          `json:"previews"`
	Jobs           []StageJob         `json:"jobs"`
}

func (s *Service) View(ctx context.Context, a Access, t Target, position string) (View, error) {
	out := View{Scope: a.scope(t), CanManage: a.Owner && a.Authority == "local", Results: []Result{}, Markers: []Marker{}, Previews: []Preview{}, Jobs: []StageJob{}}
	a, release, e := s.Capture(ctx, a, t)
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
	v, e := s.resolve(ctx, tx, a, t)
	if e != nil {
		return out, e
	}
	out.Source = v
	out.Sources = []SourceChoice{}
	choices, e := tx.QueryContext(ctx, `SELECT DISTINCT a.token,link.part_index,a.width,a.height,a.video_codec,a.audio_codec FROM catalog_entities item CROSS JOIN catalog_asset_links link ON link.entity_id=item.id CROSS JOIN catalog_assets a ON a.id=link.asset_id CROSS JOIN inventory_objects o INDEXED BY inventory_objects_asset ON o.asset_id=a.token JOIN library_sources src ON src.id=o.source_id WHERE item.public_id=pid_blob(?) AND src.library_id=? AND a.available=1 AND o.state='available' AND o.retired=0 AND src.enabled=1 AND o.root_incarnation=src.incarnation ORDER BY link.part_index,a.token LIMIT 33`, a.ItemID, a.LibraryID)
	if e != nil {
		return out, e
	}
	for choices.Next() {
		var c SourceChoice
		var part, width, height int
		var video, audio string
		if e = choices.Scan(&c.ID, &part, &width, &height, &video, &audio); e != nil {
			break
		}
		c.Label = fmt.Sprintf("Source %d · %s", len(out.Sources)+1, audio)
		if video != "" {
			c.Label = fmt.Sprintf("Source %d · %dp %s", len(out.Sources)+1, height, video)
		}
		out.Sources = append(out.Sources, c)
	}
	if e == nil {
		e = choices.Err()
	}
	choices.Close()
	if e != nil {
		return out, e
	}
	if len(out.Sources) > 32 {
		return out, ErrBudget
	}
	pos := int64(0)
	if position != "" {
		pos, e = parseUS(position)
		if e != nil || pos > v.End-v.Start {
			return out, ErrInput
		}
	}
	out.Policy, e = catalog.ScanPolicyTx(ctx, tx, a.LibraryID)
	if e != nil {
		return out, e
	}
	if out.CanManage {
		out.Budgets = map[string]any{"maxInputBytes": fmt.Sprint(s.options.MaxInputBytes), "maxGeneratedBytesPerStage": fmt.Sprint(s.stageLimit(ctx)), "maxPreviewFrames": s.options.MaxPreviewFrames, "retainDays": s.options.RetainDays, "maxCacheBytes": fmt.Sprint(s.cacheLimit(ctx))}
	}
	rows, e := tx.QueryContext(ctx, `SELECT r.stage,r.algorithm,r.evidence,r.created_ms,r.summary_json,COALESCE((SELECT id FROM analysis_artifacts a WHERE a.result_id=r.id AND a.kind IN ('waveform','fingerprint') LIMIT 1),'') FROM analysis_heads h JOIN analysis_results r ON r.id=h.result_id WHERE h.object_id=? AND h.source_revision=? AND r.root_incarnation=? AND r.configuration_generation=? AND r.retired_ms=0 AND (?='' OR r.evidence=?) ORDER BY r.stage LIMIT 16`, v.ObjectID, v.Revision, v.Incarnation, v.Configuration, v.Evidence, v.Evidence)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var r Result
		var evidence, summary string
		if e = rows.Scan(&r.Stage, &r.Algorithm, &evidence, &r.CreatedMS, &summary, &r.ArtifactID); e != nil {
			break
		}
		if e = sessionEvidence(ctx, tx, t, v, evidence); e != nil {
			break
		}
		r.OriginAssurance = assurance(evidence)
		if r.Stage == "waveform" || r.Stage == "fingerprint" {
			var small map[string]any
			if e = json.Unmarshal([]byte(summary), &small); e != nil {
				break
			}
			delete(small, "points")
			delete(small, "signatures")
			summary = jsonString(small)
		}
		r.Summary = json.RawMessage(summary)
		out.Results = append(out.Results, r)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return out, e
	}
	// Results and owner edits have a single CAS revision. Even a view without
	// detections has a stable revision for creating the first manual marker.
	_, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO analysis_marker_sets(object_id,source_revision) VALUES(?,?)`, v.ObjectID, v.Revision)
	if e != nil {
		return out, e
	}
	e = tx.QueryRowContext(ctx, `SELECT revision FROM analysis_marker_sets WHERE object_id=? AND source_revision=?`, v.ObjectID, v.Revision).Scan(&out.MarkerRevision)
	if e != nil {
		return out, e
	}
	rows, e = tx.QueryContext(ctx, `SELECT id,kind,title,start_us,end_us,confidence,provenance,approved,edited FROM analysis_markers WHERE object_id=? AND source_revision=? AND source_binding=? AND deleted=0 AND end_us>? AND start_us<? ORDER BY start_us,id LIMIT 513`, v.ObjectID, v.Revision, v.SourceBinding, v.Start, v.End)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var m Marker
		var start, end int64
		if e = rows.Scan(&m.ID, &m.Kind, &m.Title, &start, &end, &m.Confidence, &m.Provenance, &m.Approved, &m.Edited); e != nil {
			break
		}
		m.StartUS = fmt.Sprint(max(start, v.Start) - v.Start)
		m.EndUS = fmt.Sprint(min(end, v.End) - v.Start)
		out.Markers = append(out.Markers, m)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(out.Markers) > 512 {
		return out, ErrBudget
	}
	rows, e = tx.QueryContext(ctx, `SELECT a.id,a.kind,a.start_us,a.end_us,a.width,a.height FROM analysis_artifacts a JOIN analysis_results r ON r.id=a.result_id JOIN analysis_heads h ON h.result_id=r.id WHERE h.object_id=? AND h.source_revision=? AND r.root_incarnation=? AND r.configuration_generation=? AND r.retired_ms=0 AND (?='' OR r.evidence=?) AND a.mime='image/jpeg' AND a.kind IN('chapter_images','trickplay') AND a.end_us>? AND a.start_us<? ORDER BY ABS(a.start_us-?),a.kind,a.id LIMIT 12`, v.ObjectID, v.Revision, v.Incarnation, v.Configuration, v.Evidence, v.Evidence, v.Start, v.End, v.Start+pos)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var p Preview
		var start, end int64
		if e = rows.Scan(&p.ID, &p.Kind, &start, &end, &p.Width, &p.Height); e != nil {
			break
		}
		p.StartUS = fmt.Sprint(max(start, v.Start) - v.Start)
		p.EndUS = fmt.Sprint(min(end, v.End) - v.Start)
		out.Previews = append(out.Previews, p)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return out, e
	}
	sort.SliceStable(out.Previews, func(i, j int) bool {
		x, _ := parseUS(out.Previews[i].StartUS)
		y, _ := parseUS(out.Previews[j].StartUS)
		return x < y
	})
	if out.CanManage {
		var job StageJob
		e = tx.QueryRowContext(ctx, `SELECT j.id,j.status,r.phase FROM jobs j JOIN inventory_runs r ON r.job_id=j.id WHERE r.source_id=? AND j.status IN('queued','running','paused') ORDER BY j.created_at DESC LIMIT 1`, v.RootID).Scan(&job.JobID, &job.JobStatus, &job.State)
		if e == nil {
			job.Stage = "scan"
			job.ProcessedBytes = "0"
			out.Jobs = append(out.Jobs, job)
		} else if e != sql.ErrNoRows {
			return out, e
		}
		rows, e = tx.QueryContext(ctx, `SELECT s.job_id,j.status,s.stage,s.state,s.attempt,s.processed_bytes,s.error_code,s.updated_ms FROM analysis_stage_runs s JOIN jobs j ON j.id=s.job_id WHERE s.object_id=? AND s.source_revision=? ORDER BY s.updated_ms DESC,s.stage LIMIT 32`, v.ObjectID, v.Revision)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var j StageJob
			var bytes int64
			if e = rows.Scan(&j.JobID, &j.JobStatus, &j.Stage, &j.State, &j.Attempt, &bytes, &j.ErrorCode, &j.UpdatedMS); e != nil {
				break
			}
			j.ProcessedBytes = fmt.Sprint(bytes)
			out.Jobs = append(out.Jobs, j)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	if v.NeedsProbe {
		out.Results = []Result{}
		out.Markers = []Marker{}
		out.Previews = []Preview{}
	}
	if e = gated.Commit(); e != nil {
		return out, e
	}
	return out, a.validateCapture(ctx)
}
func sessionEvidence(ctx context.Context, tx *sql.Tx, t Target, v Source, evidence string) error {
	if t.SessionID == "" || v.Container != "strm" {
		return nil
	}
	var pinned string
	if e := tx.QueryRowContext(ctx, `SELECT evidence FROM subtitle_remote_sessions WHERE session_id=?`, t.SessionID).Scan(&pinned); e != nil {
		return e
	}
	if pinned != evidence {
		return ErrConflict
	}
	return nil
}

type Artifact struct {
	Scope    Scope           `json:"scope"`
	Source   Source          `json:"source"`
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	MIME     string          `json:"mime"`
	Data     string          `json:"data,omitempty"`
	Document json.RawMessage `json:"document,omitempty"`
}

func (s *Service) Read(ctx context.Context, a Access, t Target, id string) (Artifact, error) {
	out := Artifact{Scope: a.scope(t), ID: id}
	if len(id) != 64 || t.SourceRevision == "" || t.MappingRevision == "" {
		return out, ErrInput
	}
	a, release, e := s.Capture(ctx, a, t)
	if e != nil {
		return out, e
	}
	defer release()
	s.publication.Lock()
	gated2, e := s.begin(ctx, a)
	if e != nil {
		s.publication.Unlock()
		return out, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	v, e := s.resolve(ctx, tx, a, t)
	if e != nil {
		s.publication.Unlock()
		return out, e
	}
	if v.NeedsProbe {
		s.publication.Unlock()
		return out, ErrConflict
	}
	out.Source = v
	var obj mediaartifact.Object
	var evidence string
	e = tx.QueryRowContext(ctx, `SELECT a.digest,a.size,a.kind,a.mime,r.evidence FROM analysis_artifacts a JOIN analysis_results r ON r.id=a.result_id JOIN analysis_heads h ON h.result_id=r.id WHERE a.id=? AND h.object_id=? AND h.source_revision=? AND r.root_incarnation=? AND r.configuration_generation=? AND r.retired_ms=0 AND (?='' OR r.evidence=?) AND a.end_us>? AND a.start_us<?`, id, v.ObjectID, v.Revision, v.Incarnation, v.Configuration, v.Evidence, v.Evidence, v.Start, v.End).Scan(&obj.Digest, &obj.Size, &out.Kind, &out.MIME, &evidence)
	if e == nil {
		e = sessionEvidence(ctx, tx, t, v, evidence)
	}
	if e != nil {
		s.publication.Unlock()
		return out, e
	}
	if obj.Size > 2<<20 {
		s.publication.Unlock()
		return out, ErrBudget
	}
	releaseObject, e := s.custody.read(obj.Digest)
	if e != nil {
		s.publication.Unlock()
		if errors.Is(e, livechannels.ErrPhysicalBusy) {
			return out, ErrConflict
		}
		return out, e
	}
	defer releaseObject()
	reader, e := s.artifacts.Open(ctx, obj)
	if e == nil {
		e = gated2.Commit()
	}
	s.publication.Unlock()
	if reader != nil {
		defer reader.Close()
	}
	if e != nil {
		return out, e
	}
	// Store.Open owns a real retained reader. Retirement/GC cannot reclaim its
	// inode until this read has physically finished, even on request cancellation.
	data, e := io.ReadAll(io.LimitReader(reader, (2<<20)+1))
	if e != nil {
		return out, e
	}
	if int64(len(data)) != obj.Size {
		return out, mediaartifact.ErrIdentity
	}
	if out.MIME == "image/jpeg" {
		out.Data = base64.StdEncoding.EncodeToString(data)
	} else {
		var doc map[string]any
		if e = json.Unmarshal(data, &doc); e != nil {
			return out, e
		}
		if out.Kind == "waveform" {
			if points, ok := doc["points"].([]any); ok {
				mapped := []any{}
				for _, raw := range points {
					p, ok := raw.(map[string]any)
					if !ok {
						return out, ErrInput
					}
					start, ok := p["startUS"].(string)
					if !ok {
						return out, ErrInput
					}
					end, ok := p["endUS"].(string)
					if !ok {
						return out, ErrInput
					}
					x, xe := parseUS(start)
					y, ye := parseUS(end)
					if xe != nil || ye != nil {
						return out, ErrInput
					}
					if y <= v.Start || x >= v.End {
						continue
					}
					p["startUS"] = fmt.Sprint(max(x, v.Start) - v.Start)
					p["endUS"] = fmt.Sprint(min(y, v.End) - v.Start)
					mapped = append(mapped, p)
				}
				doc["points"] = mapped
				doc["durationUS"] = v.DurationUS
			}
		}
		out.Document = json.RawMessage(jsonString(doc))
	}
	// Recheck permission and source after the potentially expensive hash/read.
	after, e := s.begin(ctx, a)
	// dbwork: the recheck is a fresh gated transaction; resolve reads through it.
	if e != nil {
		return Artifact{}, e
	}
	defer after.Rollback()
	current, e := s.resolve(ctx, after.Tx(), a, Target{SourceID: v.ID, SourceRevision: v.Revision, MappingRevision: v.MappingRevision, SessionID: t.SessionID, Generation: t.Generation})
	if e != nil || current.ID != v.ID {
		if e == nil {
			e = ErrConflict
		}
		return Artifact{}, e
	}
	if e = after.Commit(); e != nil {
		return Artifact{}, e
	}
	return out, a.validateCapture(ctx)
}
