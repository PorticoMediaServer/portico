package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// Construction is never an accepted pin. Every committed component is charged
// by the ordinary accounting triggers until bounded abort/retirement removes it.
// A track costs five changes with retention accounting and ownership release.
// 24 tracks plus the build cursor cost 121 measured rows, leaving headroom
// under the unchanged 160-row/256-page plan for recording-accounting indexes.
// The approved raw-source maximum remains 32.
// No construction plan exception here; parent payloads use their own row bound.
const mbBuildQuantum = 24

var errMBBuildBusy = errors.New("MusicBrainz evidence build is not ready")

type mbBuildProof struct {
	kind, revision, digest, root string
	generation                   int64 // zero denotes immutable, pre-build-protocol evidence
}

type mbBuildRow struct {
	values  []any
	encoded []byte
	charge  int64
}
type mbBuildTable struct {
	name    string
	columns []string
	keys    []int // canonical order after the revision key; all are INTEGER or TEXT
	rows    []mbBuildRow
}
type mbBuildPlan struct {
	kind, revision, provider, digest, root string
	tables                                 []mbBuildTable
	rows                                   int
}

func mbBuildSeed() [32]byte { return sha256.Sum256([]byte("portico-musicbrainz-components-v1\x00")) }
func mbBuildHash(previous [32]byte, encoded []byte) [32]byte {
	h := sha256.New()
	h.Write(previous[:])
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(encoded)))
	h.Write(length[:])
	h.Write(encoded)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func mbBuildHex(hash [32]byte) string { return hex.EncodeToString(hash[:]) }
func mbBuildLength(v *int) any {
	if v == nil {
		return nil
	}
	return int64(*v)
}
func mbBuildCredits(table, revision string, credits []metadataprovider.ArtistCredit) mbBuildTable {
	out := mbBuildTable{name: table, columns: []string{"revision_id", "ordinal", "artist_id", "name", "artist_name", "sort_name", "disambiguation", "join_phrase"}, keys: []int{1}}
	for n, v := range credits {
		out.rows = append(out.rows, mbBuildRow{values: []any{revision, int64(n), v.Artist.ID, v.Name, v.Artist.Name, v.Artist.SortName, v.Artist.Disambiguation, v.JoinPhrase}, charge: 512})
	}
	return out
}
func mbRecordingBuildPlan(stage mbRecordingEvidence, revision, observed string) mbBuildPlan {
	r := stage.value
	p := mbBuildPlan{kind: "recording", revision: revision, provider: r.ID, digest: stage.digest}
	p.tables = []mbBuildTable{
		{name: "mb_recording_evidence", columns: []string{"revision_id", "provider_id", "digest", "title", "artist", "length_millis", "disambiguation", "payload"}, rows: []mbBuildRow{{values: []any{revision, r.ID, stage.digest, r.Title, mbArtist(r.ArtistCredit), mbBuildLength(r.LengthMillis), r.Disambiguation, string(stage.payload)}, charge: int64(len(stage.payload))*8 + 512}}},
		mbBuildCredits("mb_recording_credits", revision, r.ArtistCredit),
	}
	p.finish()
	return p
}
func mbReleaseBuildPlan(stage mbReleaseEvidence, revision string, recordings map[string]mbBuildProof) (mbBuildPlan, error) {
	r := stage.value
	p := mbBuildPlan{kind: "release", revision: revision, provider: r.ID, digest: stage.digest}
	p.tables = []mbBuildTable{
		{name: "mb_release_evidence", columns: []string{"revision_id", "provider_id", "digest", "title", "artist", "release_group_id", "group_title", "primary_type", "date", "country", "barcode", "disambiguation", "payload"}, rows: []mbBuildRow{{values: []any{revision, r.ID, stage.digest, r.Title, mbArtist(r.ArtistCredit), r.ReleaseGroup.ID, r.ReleaseGroup.Title, r.ReleaseGroup.PrimaryType, r.Date, r.Country, r.Barcode, r.Disambiguation, string(stage.payload)}, charge: int64(len(stage.payload))*8 + 512}}},
		mbBuildCredits("mb_release_credits", revision, r.ArtistCredit),
		{name: "mb_release_group_types", columns: []string{"release_revision", "ordinal", "value"}, keys: []int{1}},
		{name: "mb_release_media", columns: []string{"release_revision", "position", "format", "title"}, keys: []int{1}},
		{name: "mb_release_tracks", columns: []string{"release_revision", "track_id", "medium_position", "position", "number", "title", "artist", "length_millis", "recording_revision"}, keys: []int{1}},
		{name: "mb_track_credits", columns: []string{"release_revision", "track_id", "ordinal", "artist_id", "name", "artist_name", "sort_name", "disambiguation", "join_phrase"}, keys: []int{1, 2}},
	}
	for n, v := range r.ReleaseGroup.SecondaryTypes {
		p.tables[2].rows = append(p.tables[2].rows, mbBuildRow{values: []any{revision, int64(n), v}, charge: 512})
	}
	for _, m := range r.Media {
		p.tables[3].rows = append(p.tables[3].rows, mbBuildRow{values: []any{revision, int64(m.Position), m.Format, m.Title}, charge: 512})
		for _, t := range m.Tracks {
			staged, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: t.Recording.ID, Recording: t.Recording})
			if e != nil {
				return p, e
			}
			proof, ok := recordings[staged.digest]
			if !ok {
				return p, errPublicationInput
			}
			p.tables[4].rows = append(p.tables[4].rows, mbBuildRow{values: []any{revision, t.ID, int64(m.Position), int64(t.Position), t.Number, t.Title, trackArtist(t), mbBuildLength(t.LengthMillis), proof.revision}, charge: 512})
			for n, c := range t.ArtistCredit {
				p.tables[5].rows = append(p.tables[5].rows, mbBuildRow{values: []any{revision, t.ID, int64(n), c.Artist.ID, c.Name, c.Artist.Name, c.Artist.SortName, c.Artist.Disambiguation, c.JoinPhrase}, charge: 512})
			}
		}
	}
	p.finish()
	return p, nil
}
func (p *mbBuildPlan) finish() {
	hash := mbBuildSeed()
	for n := range p.tables {
		table := &p.tables[n]
		sort.Slice(table.rows, func(i, j int) bool {
			for _, key := range table.keys {
				a, b := table.rows[i].values[key], table.rows[j].values[key]
				if reflect.DeepEqual(a, b) {
					continue
				}
				switch a := a.(type) {
				case int64:
					return a < b.(int64)
				case string:
					return a < b.(string)
				}
			}
			return false
		})
		for i := range table.rows {
			row := &table.rows[i]
			// JSON's array/string/integer/null grammar is unambiguous. Revision and
			// table identity bind normalized rows and references, including supplements
			// in the existing parent payload. Observation time is deliberately excluded.
			row.encoded, _ = json.Marshal([]any{table.name, row.values})
			hash = mbBuildHash(hash, row.encoded)
			p.rows++
		}
	}
	p.root = mbBuildHex(hash)
}

func (s *Service) mbBuildRevision(ctx context.Context, kind, provider, digest string) (string, error) {
	var revision string
	e := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.db, func(readCtx context.Context) error {
		return dbwork.QueryRow(readCtx, s.db, `SELECT revision_id FROM mb_`+kind+`_evidence WHERE provider_id=? AND digest=?`, provider, digest).Scan(&revision)
	})
	if errors.Is(e, sql.ErrNoRows) {
		return identity.Token(), nil
	}
	return revision, e
}

type mbBuildState struct {
	state, phase, owner, root      string
	generation                     int64
	cursor, materialized, expected int
}

func mbReadBuild(ctx context.Context, tx *sql.Tx, p mbBuildPlan) (mbBuildState, error) {
	var b mbBuildState
	e := tx.QueryRowContext(ctx, `SELECT state,phase,owner_operation,materialized_digest,generation,cursor,materialized_rows,expected_rows FROM mb_evidence_builds WHERE kind=? AND revision_id=? AND provider_id=? AND digest=? AND expected_shape_digest=?`, p.kind, p.revision, p.provider, p.digest, p.root).Scan(&b.state, &b.phase, &b.owner, &b.root, &b.generation, &b.cursor, &b.materialized, &b.expected)
	return b, e
}

// A canonical provider payload is one immutable base row, capped at 4 MiB.
// Its bytes cannot be split without introducing a second payload store. This
// separate parent-only plan covers at most 1024 payload pages plus index/header
// writes; it never admits additional normalized children or a pin transition.
var mbEvidenceParentPlan = dbwork.WritePlan{
	Name: "musicbrainz-evidence-parent", Rows: 8, Pages: 2048, VMOps: 250_000,
	Why: "one immutable canonical payload row capped at 4 MiB, its build identity and exact parent accounting; all normalized children are separate bounded transactions",
}

func (s *Service) mbEnsureBuild(ctx context.Context, j mbJob, p mbBuildPlan, observed, parentBuild string) error {
	row := p.tables[0].rows[0]
	root := mbBuildHex(mbBuildHash(mbBuildSeed(), row.encoded))
	columns := append(append([]string(nil), p.tables[0].columns...), "observed_at")
	values := append(append([]any(nil), row.values...), observed)
	marks := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
	w, e := dbwork.Begin(dbwork.WithPlan(ctx, mbEvidenceParentPlan), s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	defer w.Rollback()
	tx := w.Tx()
	if e = s.activeMB(ctx, tx, j); e != nil {
		return e
	}
	var existing string
	var sealed bool
	e = tx.QueryRowContext(ctx, `SELECT revision_id,sealed FROM mb_`+p.kind+`_evidence WHERE provider_id=? AND digest=?`, p.provider, p.digest).Scan(&existing, &sealed)
	if e == nil {
		if existing != p.revision {
			return errMBBuildBusy
		}
		b, err := mbReadBuild(ctx, tx, p)
		if errors.Is(err, sql.ErrNoRows) && sealed {
			return nil
		}
		if err != nil {
			return err
		}
		if b.state == "ready" && sealed {
			return nil
		}
		if b.state == "building" && b.owner == j.operation {
			return nil
		}
		return errMBBuildBusy
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	var occupied bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mb_evidence_builds WHERE owner_operation=? AND kind=? AND state='building' LIMIT 1)`, j.operation, p.kind).Scan(&occupied); e != nil {
		return e
	}
	if occupied {
		return errMBBuildBusy
	}
	// Build and parent are atomic even at expiry: no metadata-only build or
	// unaccounted parent can escape this first transaction.
	phase := "rows"
	if p.rows == 1 {
		phase = "proving"
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO mb_evidence_builds(build_id,kind,revision_id,provider_id,digest,state,phase,cursor,expected_shape_digest,expected_rows,materialized_rows,materialized_digest,materialized_charge,owner_operation,parent_build,expires_at) VALUES(?,?,?,?,?,'building','rows',1,?,?,1,?,?,?,?,?)`, p.revision, p.kind, p.revision, p.provider, p.digest, p.root, p.rows, root, row.charge, j.operation, parentBuild, j.until); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO `+p.tables[0].name+`(`+strings.Join(columns, ",")+`) VALUES(`+marks+`)`, values...); e != nil {
		return e
	}
	if phase == "proving" {
		if _, e = tx.ExecContext(ctx, `UPDATE mb_evidence_builds SET phase='proving' WHERE build_id=?`, p.revision); e != nil {
			return e
		}
	}
	if e = checkMBEvidenceBudget(ctx, tx); e != nil {
		return e
	}
	return s.commitMBCurrent(ctx, w, j)
}

// mbConstructEvidence resumes only the same live operation's exact canonical
// build. Other operations may reuse ready evidence, never an incomplete build.
// No takeover: expiry is handed to bounded cleanup, which increments generation.
func (s *Service) mbConstructEvidence(ctx context.Context, j mbJob, p mbBuildPlan, observed, parentBuild string) (proof mbBuildProof, result error) {
	proof = mbBuildProof{kind: p.kind, revision: p.revision, digest: p.digest, root: p.root}
	defer func() {
		if result != nil {
			s.mbAbandonBuild(ctx, j, p)
		}
	}()
	if e := s.mbEnsureBuild(ctx, j, p, observed, parentBuild); e != nil {
		return proof, e
	}
	hash := mbBuildSeed()
	offset := 0
	for {
		w, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
		if e != nil {
			return proof, e
		}
		var b mbBuildState
		var legacy bool
		e = func() error {
			defer w.Rollback()
			tx := w.Tx()
			if e := s.activeMB(ctx, tx, j); e != nil {
				return e
			}
			var existing, digest string
			var sealed bool
			e := tx.QueryRowContext(ctx, `SELECT revision_id,digest,sealed FROM mb_`+p.kind+`_evidence WHERE provider_id=? AND digest=?`, p.provider, p.digest).Scan(&existing, &digest, &sealed)
			if e == nil && existing != p.revision {
				return errMBBuildBusy
			}
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			b, e = mbReadBuild(ctx, tx, p)
			if errors.Is(e, sql.ErrNoRows) {
				if existing == "" || !sealed {
					return errMBBuildBusy
				}
				legacy = true
				return nil
			} else if e != nil {
				return e
			}

			proof.generation = b.generation
			if b.state == "ready" {
				if !sealed {
					return errPublicationInput
				}
				return nil
			}
			if b.state != "building" || b.owner != j.operation || b.expected != p.rows || b.cursor != b.materialized || b.cursor > p.rows {
				return errMBBuildBusy
			}
			// Hash computation/canonicalization is done before the writer. Resume
			// computes prefix below after closing this transaction, never under it.
			if b.cursor != offset {
				return nil
			}
			if b.root != mbBuildHex(hash) {
				return errPublicationInput
			}
			if b.phase == "proving" {
				return nil
			}
			if b.phase != "rows" {
				return errPublicationInput
			}
			count := 0
			charge := int64(0)
			nextHash := hash
			position := 0
			for _, table := range p.tables {
				for _, row := range table.rows {
					if position < offset {
						position++
						continue
					}
					if count == mbBuildQuantum {
						break
					}
					columns := append([]string(nil), table.columns...)
					values := append([]any(nil), row.values...)
					if table.name == "mb_"+p.kind+"_evidence" {
						columns = append(columns, "observed_at")
						values = append(values, observed)
					}
					marks := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
					if _, e = tx.ExecContext(ctx, `INSERT INTO `+table.name+`(`+strings.Join(columns, ",")+`) VALUES(`+marks+`)`, values...); e != nil {
						return e
					}
					// A release track's FK now protects its recording. Release the tiny
					// ready-build reservation in this same commit, not before the FK exists.
					if table.name == "mb_release_tracks" {
						if _, e = tx.ExecContext(ctx, `UPDATE mb_evidence_builds SET owner_operation='' WHERE kind='recording' AND revision_id=? AND state='ready' AND owner_operation<>''`, row.values[8]); e != nil {
							return e
						}
					}
					nextHash = mbBuildHash(nextHash, row.encoded)
					charge += row.charge
					count++
					position++
				}
				if count == mbBuildQuantum {
					break
				}
			}
			phase := "rows"
			if offset+count == p.rows {
				phase = "proving"
			}
			r, e := tx.ExecContext(ctx, `UPDATE mb_evidence_builds SET cursor=?,materialized_rows=?,materialized_digest=?,materialized_charge=materialized_charge+?,phase=? WHERE build_id=? AND state='building' AND phase='rows' AND generation=? AND cursor=? AND owner_operation=?`, offset+count, offset+count, mbBuildHex(nextHash), charge, phase, p.revision, b.generation, offset, j.operation)
			if e != nil {
				return e
			}
			if e = mbBuildOne(r); e != nil {
				return e
			}
			if e = checkMBEvidenceBudget(ctx, tx); e != nil {
				return e
			}
			if e = s.commitMBCurrent(ctx, w, j); e != nil {
				return e
			}
			hash = nextHash
			offset += count
			b.cursor = offset
			b.root = mbBuildHex(hash)
			b.phase = phase
			return nil
		}()
		if e != nil {
			return proof, e
		}
		if legacy || b.state == "ready" {
			if legacy {
				proof.generation = 0
			}
			// A reused ready root proves the same typed canonical rows already. A
			// legacy seal needs one bounded independent proof before final authority.
			if !legacy {
				return proof, nil
			}
			if e = s.mbProveBuild(ctx, p, proof.generation); e != nil {
				return proof, e
			}
			return proof, nil
		}
		if offset != b.cursor {
			// Restart/resume: input is detached and canonical; prefix hashing holds
			// neither writer nor snapshot. The stored root must match its exact prefix.
			hash = mbBuildSeed()
			offset = 0
			for _, table := range p.tables {
				for _, row := range table.rows {
					if offset == b.cursor {
						break
					}
					hash = mbBuildHash(hash, row.encoded)
					offset++
				}
				if offset == b.cursor {
					break
				}
			}
			if mbBuildHex(hash) != b.root {
				return proof, errPublicationInput
			}
		}
		if b.phase == "proving" {
			break
		}
	}
	if e := s.mbProveBuild(ctx, p, proof.generation); e != nil {
		return proof, e
	}
	w, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return proof, e
	}
	defer w.Rollback()
	if e = s.activeMB(ctx, w.Tx(), j); e != nil {
		return proof, e
	}
	b, e := mbReadBuild(ctx, w.Tx(), p)
	if e != nil {
		return proof, e
	}
	if b.state != "building" || b.phase != "proving" || b.owner != j.operation || b.generation != proof.generation || b.cursor != p.rows || b.materialized != p.rows || b.root != p.root {
		return proof, errMBBuildBusy
	}
	if _, e = w.Tx().ExecContext(ctx, `UPDATE mb_`+p.kind+`_evidence SET sealed=1 WHERE revision_id=? AND sealed=0`, p.revision); e != nil {
		return proof, e
	}
	if _, e = w.Tx().ExecContext(ctx, `UPDATE mb_evidence_builds SET state='ready',phase='ready' WHERE build_id=? AND generation=?`, p.revision, proof.generation); e != nil {
		return proof, e
	}
	if e = checkMBEvidenceBudget(ctx, w.Tx()); e != nil {
		return proof, e
	}
	return proof, s.commitMBCurrent(ctx, w, j)
}
func mbBuildOne(result sql.Result) error {
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return errMBBuildBusy
	}
	return nil
}

// This is a bounded best effort state change, not destruction. If cancellation
// or process death prevents it, expiry/provenance lets the existing worker claim
// the same charged rows later. Never cascade a parent from this error path.
func (s *Service) mbAbandonBuild(ctx context.Context, j mbJob, p mbBuildPlan) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_, _ = dbwork.ExecWrite(c, s.db, dbwork.ClassBackgroundMedia, `UPDATE mb_evidence_builds SET state='aborting',phase='cleanup',generation=generation+1 WHERE kind=? AND revision_id=? AND owner_operation=? AND state='building'`, p.kind, p.revision, j.operation)
}

// mbProveBuild compares independent reads of all authoritative normalized rows
// against canonical typed input. Every snapshot reads <=33 rows, and closes
// before CPU hashing or the next batch. Frozen children (or a legacy terminal
// seal) and a generation check permit multiple snapshots without mixed facts.
func (s *Service) mbProveBuild(ctx context.Context, p mbBuildPlan, generation int64) error {
	hash := mbBuildSeed()
	for _, table := range p.tables {
		offset := 0
		for {
			var got [][]any
			e := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.db, func(readCtx context.Context) error {
				tx := dbwork.Snapshot(readCtx)
				if e := mbBuildFrozenTx(readCtx, tx, p, generation); e != nil {
					return e
				}
				q := `SELECT ` + strings.Join(table.columns, ",") + ` FROM ` + table.name + ` WHERE ` + table.columns[0] + `=?`
				args := []any{p.revision}
				if offset > 0 && len(table.keys) > 0 {
					keys := []string{}
					marks := []string{}
					for _, k := range table.keys {
						keys = append(keys, table.columns[k])
						marks = append(marks, "?")
						args = append(args, table.rows[offset-1].values[k])
					}
					q += ` AND (` + strings.Join(keys, ",") + `) > (` + strings.Join(marks, ",") + `)`
				}
				order := []string{}
				for _, k := range table.keys {
					order = append(order, table.columns[k])
				}
				if len(order) > 0 {
					q += ` ORDER BY ` + strings.Join(order, ",")
				}
				q += ` LIMIT ?`
				args = append(args, mbBuildQuantum+1)
				rows, e := tx.QueryContext(readCtx, q, args...)
				if e != nil {
					return e
				}
				defer rows.Close()
				for rows.Next() {
					v := make([]any, len(table.columns))
					ptr := make([]any, len(v))
					for n := range v {
						ptr[n] = &v[n]
					}
					if e = rows.Scan(ptr...); e != nil {
						return e
					}
					for n := range v {
						if b, ok := v[n].([]byte); ok {
							v[n] = string(b)
						}
					}
					got = append(got, v)
				}
				return rows.Err()
			})
			if e != nil {
				return e
			}
			count := len(got)
			if count > mbBuildQuantum {
				count = mbBuildQuantum
			}
			if offset+count > len(table.rows) {
				return errPublicationInput
			}
			for n := 0; n < count; n++ {
				if !reflect.DeepEqual(got[n], table.rows[offset+n].values) {
					return fmt.Errorf("%w: normalized %s component %d differs", errPublicationInput, table.name, offset+n)
				}
				encoded, _ := json.Marshal([]any{table.name, got[n]})
				hash = mbBuildHash(hash, encoded)
			}
			offset += count
			if len(got) <= mbBuildQuantum {
				if offset != len(table.rows) {
					return errPublicationInput
				}
				break
			}
			if len(table.keys) == 0 {
				return errPublicationInput
			}
		}
	}
	if mbBuildHex(hash) != p.root {
		return errPublicationInput
	}
	return nil
}
func mbBuildFrozenTx(ctx context.Context, tx *sql.Tx, p mbBuildPlan, generation int64) error {
	if generation == 0 {
		var sealed bool
		e := tx.QueryRowContext(ctx, `SELECT sealed FROM mb_`+p.kind+`_evidence WHERE revision_id=? AND provider_id=? AND digest=?`, p.revision, p.provider, p.digest).Scan(&sealed)
		if e != nil {
			return e
		}
		if !sealed {
			return errPublicationInput
		}
		return nil
	}
	b, e := mbReadBuild(ctx, tx, p)
	if e != nil {
		return e
	}
	if b.generation != generation || (b.state != "ready" && (b.state != "building" || b.phase != "proving")) || b.root != p.root || b.cursor != p.rows || b.materialized != p.rows || b.expected != p.rows {
		return errMBBuildBusy
	}
	return nil
}

func mbEvidenceReadyTx(ctx context.Context, tx *sql.Tx, proof mbBuildProof) error {
	var sealed bool
	var digest string
	if e := tx.QueryRowContext(ctx, `SELECT digest,sealed FROM mb_`+proof.kind+`_evidence WHERE revision_id=?`, proof.revision).Scan(&digest, &sealed); e != nil {
		return e
	}
	if !sealed || digest != proof.digest {
		return errPublicationInput
	}
	var state, root string
	var generation int64
	e := tx.QueryRowContext(ctx, `SELECT state,generation,expected_shape_digest FROM mb_evidence_builds WHERE kind=? AND revision_id=?`, proof.kind, proof.revision).Scan(&state, &generation, &root)
	if errors.Is(e, sql.ErrNoRows) && proof.generation == 0 {
		return nil
	}
	if e != nil {
		return e
	}
	if state != "ready" || generation != proof.generation || root != proof.root {
		return errMBBuildBusy
	}
	return nil
}

func (s *Service) mbStageRecording(ctx context.Context, j mbJob, stage mbRecordingEvidence, observed string) (mbBuildProof, error) {
	revision, e := s.mbBuildRevision(ctx, "recording", stage.value.ID, stage.digest)
	if e != nil {
		return mbBuildProof{}, e
	}
	return s.mbConstructEvidence(ctx, j, mbRecordingBuildPlan(stage, revision, observed), observed, "")
}
func (s *Service) mbStageRelease(ctx context.Context, j mbJob, stage mbReleaseEvidence, observed string) (mbBuildProof, error) {
	revision, e := s.mbBuildRevision(ctx, "release", stage.value.ID, stage.digest)
	if e != nil {
		return mbBuildProof{}, e
	}
	records := map[string]mbRecordingEvidence{}
	order := []string{}
	for _, m := range stage.value.Media {
		for _, t := range m.Tracks {
			record, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: t.Recording.ID, Recording: t.Recording})
			if e != nil {
				return mbBuildProof{}, errPublicationInput
			}
			if _, ok := records[record.digest]; ok {
				continue
			}
			records[record.digest] = record
			order = append(order, record.digest)
		}
	}
	recordings := map[string]mbBuildProof{}
	// Only identities are collected here, never authority or query results. The
	// references are part of this actual evidence mutation's canonical manifest.
	for offset := 0; offset < len(order); offset += mbBuildQuantum {
		end := min(offset+mbBuildQuantum, len(order))
		e = dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.db, func(readCtx context.Context) error {
			for _, digest := range order[offset:end] {
				record := records[digest]
				var id string
				e := dbwork.QueryRow(readCtx, s.db, `SELECT revision_id FROM mb_recording_evidence WHERE provider_id=? AND digest=?`, record.value.ID, digest).Scan(&id)
				if errors.Is(e, sql.ErrNoRows) {
					id = identity.Token()
				} else if e != nil {
					return e
				}
				recordings[digest] = mbBuildProof{kind: "recording", revision: id, digest: digest}
			}
			return nil
		})
		if e != nil {
			return mbBuildProof{}, e
		}
	}
	p, e := mbReleaseBuildPlan(stage, revision, recordings)
	if e != nil {
		return mbBuildProof{}, e
	}
	// A release parent and build anchor precede every origin recording build.
	// Failed dependency construction can therefore be collected immediately by
	// indexed parent_build provenance, rather than waiting ordinary cache age.
	if e = s.mbEnsureBuild(ctx, j, p, observed, ""); e != nil {
		s.mbAbandonBuild(ctx, j, p)
		return mbBuildProof{}, e
	}
	for _, digest := range order {
		record := records[digest]
		rp := mbRecordingBuildPlan(record, recordings[digest].revision, observed)
		proof, e := s.mbConstructEvidence(ctx, j, rp, observed, p.revision)
		if e != nil {
			s.mbAbandonBuild(ctx, j, p)
			return mbBuildProof{}, e
		}
		recordings[digest] = proof
	}
	return s.mbConstructEvidence(ctx, j, p, observed, "")
}

// Bind ready evidence to its operation before releasing build ownership. Both
// the result FK and reservation transition are protected by the live fence.
func (s *Service) mbAttachStaged(ctx context.Context, j mbJob, proof mbBuildProof) error {
	w, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	defer w.Rollback()
	if e = s.activeMB(ctx, w.Tx(), j); e != nil {
		return e
	}
	if e = mbEvidenceReadyTx(ctx, w.Tx(), proof); e != nil {
		return e
	}
	if _, e = w.Tx().ExecContext(ctx, `UPDATE mb_publication_operations SET result_`+proof.kind+`_revision=?,result_digest=?,status='staged' WHERE id=?`, proof.revision, proof.digest, j.operation); e != nil {
		return e
	}
	if _, e = w.Tx().ExecContext(ctx, `UPDATE mb_evidence_builds SET owner_operation='' WHERE kind=? AND revision_id=? AND state='ready' AND owner_operation<>''`, proof.kind, proof.revision); e != nil {
		return e
	}
	// Reuse has no construction writes. Preserve the original staging admission
	// check here too, before an operation acquires its ready result FK.
	if e = checkMBEvidenceBudget(ctx, w.Tx()); e != nil {
		return e
	}
	return s.commitMBCurrent(ctx, w, j)
}
