package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/diagnostics"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strconv"
	"strings"
	"time"
)

type Record struct {
	Sequence  int64            `json:"sequence"`
	Lane      string           `json:"lane"`
	At        apiwire.Instant  `json:"at"`
	Severity  string           `json:"severity"`
	Component string           `json:"component"`
	Code      string           `json:"code"`
	Fields    map[string]int64 `json:"fields"`
}
type AuditRecord struct {
	Sequence     int64           `json:"sequence"`
	ID           string          `json:"id"`
	At           apiwire.Instant `json:"at"`
	Actor        string          `json:"actor"`
	Action       string          `json:"action"`
	Target       string          `json:"target"`
	Revision     int64           `json:"revision"`
	PreviousHash string          `json:"previousHash"`
	Hash         string          `json:"hash"`
}

func validComponent(v string) bool {
	switch v {
	case "scheduler", "storage", "playback", "client", "support":
		return true
	}
	return false
}

// Both client and server records reject arbitrary strings before persistence.
// Client uploads have a separate lane and cannot rotate server or audit rows.
func validRecord(lane, severity, component, code string, fields map[string]int64) bool {
	if lane != "runtime" && lane != "client" || !validComponent(component) || !validID(code) || len(code) > 64 || len(fields) > 12 {
		return false
	}
	switch severity {
	case "info", "warning", "error", "debug":
	default:
		return false
	}
	for key, value := range fields {
		switch key {
		case "processed", "attempt", "durationMs", "count", "bytes":
		default:
			return false
		}
		if value < 0 || value > 9007199254740991 {
			return false
		}
	}
	return true
}
func (s *Store) recordTx(tx *sql.Tx, lane, severity, component, code string, fields map[string]int64) error {
	if !validRecord(lane, severity, component, code, fields) {
		return ErrInvalid
	}
	if severity == "debug" {
		var body string
		if tx.QueryRow(`SELECT body FROM console_documents WHERE scope='capture'`).Scan(&body) != nil {
			return nil
		}
		var c Capture
		if decodeDocument(body, &c) != nil || int64(c.ExpiresAt) <= s.now() || c.Component != component {
			return nil
		}
	}
	b, _ := json.Marshal(fields)
	if _, e := tx.Exec(`INSERT INTO console_records(lane,time_ms,severity,component,code,fields) VALUES(?,?,?,?,?,?)`, lane, s.now(), severity, component, code, string(b)); e != nil {
		return e
	}
	limit := 20000
	if lane == "client" {
		limit = 5000
	}
	_, e := tx.Exec(`DELETE FROM console_records WHERE lane=? AND sequence NOT IN(SELECT sequence FROM console_records WHERE lane=? ORDER BY sequence DESC LIMIT ?)`, lane, lane, limit)
	return e
}
func (s *Store) Record(ctx context.Context, lane, severity, component, code string, fields map[string]int64) error {
	gated, e := dbwork.Begin(ctx, s.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if e = s.recordTx(tx, lane, severity, component, code, fields); e != nil {
		return e
	}
	return gated.Commit()
}
func readRecords(tx *sql.Tx, lane string, before, from, to int64, limit int) (Page[Record], error) {
	out := Page[Record]{Items: []Record{}}
	rows, e := tx.Query(`SELECT sequence,lane,time_ms,severity,component,code,fields FROM console_records WHERE lane=? AND sequence<? AND time_ms>=? AND time_ms<=? ORDER BY sequence DESC LIMIT ?`, lane, before, from, to, limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var r Record
		var fields string
		if e = rows.Scan(&r.Sequence, &r.Lane, &r.At, &r.Severity, &r.Component, &r.Code, &fields); e != nil {
			return out, e
		}
		if e = decodeDocument(fields, &r.Fields); e != nil {
			return out, e
		}
		// Validate again on projection; corrupt/migrated rows never become raw UI text.
		if !validRecord(r.Lane, r.Severity, r.Component, r.Code, r.Fields) {
			return out, ErrInvalid
		}
		out.Items = append(out.Items, r)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = strconv.FormatInt(out.Items[len(out.Items)-1].Sequence, 10)
	}
	return out, nil
}
func readAudit(tx *sql.Tx, before, from, to int64, limit int) (Page[AuditRecord], error) {
	out := Page[AuditRecord]{Items: []AuditRecord{}}
	rows, e := tx.Query(`SELECT sequence,id,time_ms,actor,action,target,revision,previous_hash,hash FROM console_audit WHERE sequence<? AND time_ms>=? AND time_ms<=? ORDER BY sequence DESC LIMIT ?`, before, from, to, limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var r AuditRecord
		if e = rows.Scan(&r.Sequence, &r.ID, &r.At, &r.Actor, &r.Action, &r.Target, &r.Revision, &r.PreviousHash, &r.Hash); e != nil {
			return out, e
		}
		out.Items = append(out.Items, r)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = strconv.FormatInt(out.Items[len(out.Items)-1].Sequence, 10)
	}
	return out, nil
}
func (s *Store) Records(ctx context.Context, p identity.Principal, auth Authorize, lane, cursor string) (out any, e error) {
	before, e := Cursor(cursor)
	if e != nil {
		return nil, e
	}
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		var err error
		if lane == "audit" {
			out, err = readAudit(tx, before, 0, s.now(), 40)
		} else if lane == "runtime" || lane == "client" {
			out, err = readRecords(tx, lane, before, 0, s.now(), 40)
		} else {
			return ErrInvalid
		}
		return err
	})
	if e == nil && lane == "audit" {
		// C74: the audit entry for this read is a write, so it cannot share the
		// read snapshot (it failed there, and every audit-log read failed with it).
		// It is committed in its own short gated write before the page is returned;
		// if it cannot be recorded, the page is not returned.
		if e = s.auditAfterRead(ctx, auth, p, "audit.read", "audit"); e != nil {
			out = nil
		}
	}
	return
}

// auditAfterRead records an access audit entry in its own gated write, after a
// read that ran on a snapshot (C74). The caller withholds the read's result when
// this fails, so nothing is disclosed without its audit record.
func (s *Store) auditAfterRead(ctx context.Context, auth Authorize, p identity.Principal, action, target string) error {
	return s.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		return Audit(tx, s.now(), AccountKey(p), action, target, 0)
	})
}

type Capture struct {
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	Component      string          `json:"component"`
	ExpiresAt      apiwire.Instant `json:"expiresAt"`
}

func (s *Store) Capture(ctx context.Context, p identity.Principal, auth Authorize, c Capture) error {
	if c.Component != "scheduler" || c.ExpiresAt < 0 || int64(c.ExpiresAt) > s.now()+int64(time.Hour/time.Millisecond) {
		return ErrInvalid
	}
	return s.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		scope := "capture:" + AccountKey(p)
		raw, digest, e := Receipt(tx, scope, c.IdempotencyKey, c, s.now())
		if e != nil {
			return e
		}
		if raw != "" {
			return nil
		}
		// A replay can confirm an already-expired capture, but cannot reactivate it.
		saved := c
		saved.IdempotencyKey = ""
		b, _ := json.Marshal(saved)
		if _, e := tx.Exec(`INSERT INTO console_documents VALUES('capture',1,?,?) ON CONFLICT(scope) DO UPDATE SET revision=revision+1,body=excluded.body,updated_ms=excluded.updated_ms`, string(b), s.now()); e != nil {
			return e
		}
		if e := Audit(tx, s.now(), AccountKey(p), "diagnostics.capture", c.Component, 0); e != nil {
			return e
		}
		return SaveReceipt(tx, scope, c.IdempotencyKey, digest, map[string]bool{"accepted": true}, s.now())
	})
}

type ExportRequest struct {
	IncludePaths   ExportIncludePaths `json:"includePaths,omitempty"`
	IdempotencyKey string             `json:"idempotencyKey"`
	From           apiwire.Instant    `json:"from"`
	To             apiwire.Instant    `json:"to"`
	Components     []string           `json:"components"`
}
type Export struct {
	SnapshotPaths *ExportPathFacts    `json:"snapshotPaths,omitempty"`
	ID            string              `json:"id"`
	ExpiresAt     apiwire.Instant     `json:"expiresAt"`
	Manifest      ExportManifest      `json:"manifest"`
	Snapshot      *diagnostics.Report `json:"snapshot,omitempty"`
	Runtime       *Page[Record]       `json:"runtime,omitempty"`
	Client        *Page[Record]       `json:"client,omitempty"`
	Audit         *Page[AuditRecord]  `json:"audit,omitempty"`
	AuditAnchor   *AuditAnchor        `json:"auditAnchor,omitempty"`
}
type ExportManifest struct {
	IncludePaths              bool                 `json:"includePaths,omitempty"`
	PathCategories            []ExportPathCategory `json:"pathCategories,omitempty"`
	PathsTruncated            bool                 `json:"pathsTruncated,omitempty"`
	PathsInvalidAmongExamined int                  `json:"pathsInvalidAmongExamined,omitempty"`
	PathsScanTruncated        bool                 `json:"pathsScanTruncated,omitempty"`
	Version                   string               `json:"version"`
	From                      apiwire.Instant      `json:"from"`
	To                        apiwire.Instant      `json:"to"`
	Included                  []string             `json:"included"`
	Excluded                  []string             `json:"excluded"`
	Redaction                 string               `json:"redaction"`
	MaxBytes                  int                  `json:"maxBytes"`
}

func (s *Store) CreateExport(ctx context.Context, p identity.Principal, auth Authorize, hosted bool, c ExportRequest) (out Export, e error) {
	if c.From < 0 || c.To < c.From || int64(c.To) > s.now()+60000 || int64(c.To-c.From) > days(7) || len(c.Components) == 0 || len(c.Components) > 4 {
		return out, ErrInvalid
	}
	seen := map[string]bool{}
	for _, v := range c.Components {
		if seen[v] {
			return out, ErrInvalid
		}
		seen[v] = true
		switch v {
		case "snapshot", "runtime", "client", "audit":
		default:
			return out, ErrInvalid
		}
	}
	// Preserve the existing support-report builder and its own bounded snapshot.
	var snapshot *diagnostics.Report
	if seen["snapshot"] {
		v, err := diagnostics.Read(ctx, s.DB, hosted)
		if err != nil {
			return out, err
		}
		snapshot = &v
	}
	e = s.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		raw, digest, err := Receipt(tx, "export:"+p.Hash, c.IdempotencyKey, c, s.now())
		if err != nil {
			return err
		}
		if raw != "" {
			if err = decodeDocument(raw, &out); err != nil {
				return err
			}
			if int64(out.ExpiresAt) <= s.now() {
				return ErrExpired
			}
			return nil
		}
		var n int
		if err = tx.QueryRow(`SELECT count(*) FROM console_exports WHERE actor=? AND expires_ms>?`, p.Hash, s.now()).Scan(&n); err != nil {
			return err
		}
		if n >= 4 {
			return ErrCapacity
		}
		out = Export{ID: identity.Token(), ExpiresAt: apiwire.Instant(s.now() + int64(time.Hour/time.Millisecond)), Manifest: ExportManifest{Version: RegistryRevision, From: c.From, To: c.To, Included: c.Components, Excluded: []string{"secrets", "raw logs", "media", "databases", "feedback text", "routes and paths"}, Redaction: "Typed allowlist before persistence and projection; no automatic upload", MaxBytes: 1 << 20, IncludePaths: bool(c.IncludePaths)}, Snapshot: snapshot}
		if bool(c.IncludePaths) && seen["snapshot"] {
			facts, err := readExportPathFacts(ctx, tx)
			if err != nil {
				return err
			}
			out.SnapshotPaths = facts
			out.Manifest.PathsTruncated = facts.HasMore
			out.Manifest.PathsInvalidAmongExamined = facts.InvalidAmongExamined
			out.Manifest.PathsScanTruncated = facts.ScanTruncated
			categories := map[ExportPathCategory]bool{}
			for _, fact := range facts.Items {
				if !categories[fact.Category] {
					categories[fact.Category] = true
					out.Manifest.PathCategories = append(out.Manifest.PathCategories, fact.Category)
				}
			}
			out.Manifest.Excluded = []string{"secrets", "raw logs", "media", "databases", "feedback text", "routes", "all paths outside the three-field filesystem allowlist"}
			out.Manifest.Redaction = "Typed operational allowlist plus bounded recorded filesystem roots and mount points; no automatic upload"
		}

		if seen["runtime"] {
			v, err := readRecords(tx, "runtime", 9223372036854775807, int64(c.From), int64(c.To), 1000)
			if err != nil {
				return err
			}
			out.Runtime = &v
		}
		if seen["client"] {
			v, err := readRecords(tx, "client", 9223372036854775807, int64(c.From), int64(c.To), 1000)
			if err != nil {
				return err
			}
			out.Client = &v
		}
		if seen["audit"] {
			v, err := readAudit(tx, 9223372036854775807, int64(c.From), int64(c.To), 1000)
			if err != nil {
				return err
			}
			out.Audit = &v
			anchor, err := auditAnchor(tx)
			if err != nil {
				return err
			}
			out.AuditAnchor = &anchor
		}
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if len(b) > 960<<10 {
			return ErrCapacity
		}
		if _, err = tx.Exec(`INSERT INTO console_exports VALUES(?,?,?,?)`, out.ID, p.Hash, out.ExpiresAt, string(b)); err != nil {
			return err
		}
		if err = Audit(tx, s.now(), AccountKey(p), "support.export", out.ID, 0); err != nil {
			return err
		}
		if err = SaveReceipt(tx, "export:"+p.Hash, c.IdempotencyKey, digest, out, s.now()); err != nil {
			return err
		}
		// The receipt contains the export; its private bytes must expire too.
		_, err = tx.Exec(`UPDATE console_receipts SET expires_ms=? WHERE scope=? AND key=?`, out.ExpiresAt, "export:"+p.Hash, c.IdempotencyKey)
		return err
	})
	return
}
func (s *Store) Export(ctx context.Context, p identity.Principal, auth Authorize, id string) (out Export, e error) {
	if !validID(id) {
		return out, ErrInvalid
	}
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		var body string
		var expiry int64
		e := tx.QueryRow(`SELECT body,expires_ms FROM console_exports WHERE id=? AND actor=?`, id, p.Hash).Scan(&body, &expiry)
		if e != nil {
			return e
		}
		if expiry <= s.now() {
			return ErrExpired
		}
		return decodeDocument(body, &out)
	})
	if e == nil {
		// C74: audited in its own gated write; no audit, no download.
		if e = s.auditAfterRead(ctx, auth, p, "support.download", id); e != nil {
			out = Export{}
		}
	}
	return
}

type Alert struct {
	ID          string            `json:"id"`
	Code        string            `json:"code"`
	Severity    string            `json:"severity"`
	Status      string            `json:"status"`
	FirstAt     apiwire.Instant   `json:"firstAt"`
	LastAt      apiwire.Instant   `json:"lastAt"`
	Occurrences int64             `json:"occurrences"`
	Revision    int64             `json:"revision"`
	Params      map[string]string `json:"params"`
}

func (s *Store) Alert(ctx context.Context, code, severity string, active bool, details ...map[string]string) error {
	params := map[string]string{}
	if len(details) > 1 {
		return ErrInvalid
	}
	if len(details) == 1 {
		for key, value := range details[0] {
			if key != "title" && key != "channel" && key != "reason" && key != "time" || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n\t") {
				return ErrInvalid
			}
			if key == "reason" && value != "" && !validID(value) {
				return ErrInvalid
			}
			if key == "time" {
				if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
					return ErrInvalid
				}
			}
			params[key] = value
		}
	}
	if !validID(code) || len(code) > 64 || severity != "warning" && severity != "critical" && severity != "info" {
		return ErrInvalid
	}
	if !active {
		// "This is still fine" was a gated write transaction and a commit, from
		// several loops, every second, forever. Resolving an alert that is not
		// raised is a read that finds nothing.
		var open int
		if e := dbwork.QueryRow(ctx, s.DB, `SELECT count(*) FROM console_alerts WHERE code=? AND status!='resolved'`, code).Scan(&open); e == nil && open == 0 {
			return nil
		}
	}
	gated2, e := dbwork.Begin(ctx, s.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	var id, status string
	var revision int64
	e = tx.QueryRow(`SELECT id,status,revision FROM console_alerts WHERE code=?`, code).Scan(&id, &status, &revision)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if !active {
		if _, e = tx.Exec(`UPDATE console_alerts SET status='resolved',revision=revision+1,last_ms=? WHERE code=? AND status!='resolved'`, s.now(), code); e != nil {
			return e
		}
		if e = AppendActivityProgressTx(tx, "server.updated", "alert", id, strconv.FormatInt(revision+1, 10), true); e != nil {
			return e
		}
		return gated2.Commit()
	}
	opened := e == sql.ErrNoRows || status == "resolved"
	if id == "" {
		id = identity.Token()
	}
	if _, e = tx.Exec(`INSERT INTO console_alerts VALUES(?,?,?,'open',?,?,1,1) ON CONFLICT(code) DO UPDATE SET status=CASE WHEN status='resolved' THEN 'open' ELSE status END,severity=excluded.severity,last_ms=excluded.last_ms,occurrences=occurrences+1,revision=revision+CASE WHEN status='resolved' OR severity!=excluded.severity THEN 1 ELSE 0 END`, id, code, severity, s.now(), s.now()); e != nil {
		return e
	}
	if _, e = tx.Exec(`DELETE FROM console_alert_params WHERE alert_id=?`, id); e != nil {
		return e
	}
	for key, value := range params {
		if _, e = tx.Exec(`INSERT INTO console_alert_params(alert_id,key,value) VALUES(?,?,?)`, id, key, value); e != nil {
			return e
		}
	}
	if opened {
		owners, e := ownerScopes(tx)
		if e != nil {
			return e
		}
		for _, scope := range owners {
			if e = notifyTx(tx, s.now(), scope, "owner-alert", id, id+":"+strconv.FormatInt(revision+1, 10)); e != nil {
				return e
			}
		}
	}
	if e = AppendActivityProgressTx(tx, "server.updated", "alert", id, strconv.FormatInt(revision+1, 10), opened); e != nil {
		return e
	}
	return gated2.Commit()
}
func (s *Store) Alerts(ctx context.Context, auth Authorize) (out []Alert, e error) {
	out = []Alert{}
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id,code,severity,status,first_ms,last_ms,occurrences,revision FROM console_alerts ORDER BY CASE WHEN status='resolved' THEN 1 ELSE 0 END,last_ms DESC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a := Alert{Params: map[string]string{}}
			if err = rows.Scan(&a.ID, &a.Code, &a.Severity, &a.Status, &a.FirstAt, &a.LastAt, &a.Occurrences, &a.Revision); err != nil {
				return err
			}
			out = append(out, a)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		params, e := tx.Query(`SELECT alert_id,key,value FROM console_alert_params ORDER BY alert_id,key`)
		if e != nil {
			return e
		}
		defer params.Close()
		indexes := map[string]int{}
		for n := range out {
			indexes[out[n].ID] = n
		}
		for params.Next() {
			var id, key, value string
			if e = params.Scan(&id, &key, &value); e != nil {
				return e
			}
			if n, ok := indexes[id]; ok {
				out[n].Params[key] = value
			}
		}
		return params.Err()
	})
	return
}
func (s *Store) Acknowledge(ctx context.Context, p identity.Principal, auth Authorize, id string, c JobCommand) error {
	if !validID(id) {
		return ErrInvalid
	}
	return s.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		scope := "alert-ack:" + AccountKey(p)
		raw, digest, e := Receipt(tx, scope, c.IdempotencyKey, []any{id, c}, s.now())
		if e != nil {
			return e
		}
		if raw != "" {
			return nil
		}
		var revision int64
		var state string
		if e = tx.QueryRow(`SELECT revision,status FROM console_alerts WHERE id=?`, id).Scan(&revision, &state); e != nil {
			return e
		}
		if revision != c.ExpectedRevision || state != "open" {
			return &ConflictError{revision}
		}
		if _, e = tx.Exec(`UPDATE console_alerts SET status='acknowledged',revision=revision+1 WHERE id=?`, id); e != nil {
			return e
		}
		if e = Audit(tx, s.now(), AccountKey(p), "alert.acknowledge", id, revision+1); e != nil {
			return e
		}
		return SaveReceipt(tx, scope, c.IdempotencyKey, digest, map[string]bool{"acknowledged": true}, s.now())
	})
}

// retentionBatch bounds one retention transaction to 16 parent rows, so a
// parent's cascading children (report events, alert parameters, played
// intervals) still fit a maintenance write plan. Each batch commits on its own:
// a large backlog clears over as many batches as it needs, keeping every one it
// finished even when the caller's deadline ends the run, instead of being one
// transaction that never fits the deadline and so never deletes anything.
const retentionBatch = 16

func (s *Store) Prune(ctx context.Context) error {
	class := dbwork.ClassFrom(ctx, dbwork.ClassMaintenance)
	now := s.now()
	// Receipt reclamation shares this existing maintenance wake. Each turn
	// advances its durable cutoff and reclaims at most 32 rows before yielding.
	for {
		var removed int64
		if err := dbwork.WithWriteTx(ctx, s.DB, class, func(tx *sql.Tx) error {
			var err error
			removed, err = usermutation.Prune(ctx, tx, now)
			return err
		}); err != nil {
			return err
		}
		if removed < usermutation.PruneLimit {
			break
		}
		if !dbwork.Yield(ctx) {
			return ctx.Err()
		}
	}
	var v SettingsDocument
	auditThrough := int64(-1)
	// One short transaction reads the retention settings and keeps a
	// trusted-host checkpoint before old links are dropped from the audit chain.
	if e := dbwork.WithWriteTx(ctx, s.DB, class, func(tx *sql.Tx) error {
		var e error
		if v, e = readSettings(tx); e != nil {
			return e
		}
		var sequence int64
		var hash string
		e = tx.QueryRow(`SELECT sequence,hash FROM console_audit WHERE time_ms<? AND sequence<COALESCE((SELECT min(sequence) FROM console_audit WHERE time_ms>=?),9223372036854775807) ORDER BY sequence DESC LIMIT 1`, now-days(90), now-days(90)).Scan(&sequence, &hash)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		b, _ := json.Marshal(map[string]any{"sequence": sequence, "hash": hash})
		if _, e = tx.Exec(`INSERT INTO console_documents VALUES('audit-anchor',1,?,?) ON CONFLICT(scope) DO UPDATE SET revision=revision+1,body=excluded.body,updated_ms=excluded.updated_ms`, string(b), now); e != nil {
			return e
		}
		auditThrough = sequence
		return nil
	}); e != nil {
		return e
	}
	// Each condition selects at most one batch of rowids; the delete repeats
	// until a batch comes back short.
	type sweep struct {
		table, where string
		args         []any
	}
	sweeps := []sweep{
		{`console_receipts`, `expires_ms<=?`, []any{now}},
		{`console_exports`, `expires_ms<=?`, []any{now}},
		{`console_alerts`, `status='resolved' AND last_ms<?`, []any{now - days(v.Effective.JobDays)}},
		{`console_records`, `time_ms<?`, []any{now - days(v.Effective.DiagnosticDays)}},
		{`console_reports`, `expires_ms<=?`, []any{now}},
		{`app_reports`, `expires_ms<=?`, []any{now}},
		{`console_notifications`, `expires_ms<=? OR created_ms<?`, []any{now, now - days(v.Effective.NotificationDays)}},
		{`console_operations`, `state IN ('succeeded','failed','cancelled') AND updated_ms<?`, []any{now - days(v.Effective.JobDays)}},
		{`console_documents`, `scope LIKE 'session:%' AND NOT EXISTS(SELECT 1 FROM authorization_access WHERE hash=substr(console_documents.scope,9) AND revoked=0 AND expires_at>?)`, []any{s.Now().UTC().Format(time.RFC3339)}},
	}
	if auditThrough >= 0 {
		sweeps = append(sweeps, sweep{`console_audit`, `sequence<=?`, []any{auditThrough}})
	}
	for _, q := range sweeps {
		statement := `DELETE FROM ` + q.table + ` WHERE rowid IN(SELECT rowid FROM ` + q.table + ` WHERE ` + q.where + ` LIMIT ` + strconv.Itoa(retentionBatch) + `)`
		if e := s.sweepRetention(ctx, class, statement, q.args...); e != nil {
			return e
		}
	}
	if keep := v.Effective.PlayHistoryDays; keep > 0 {
		// The server's play history is kept in full unless the owner asks for
		// less; shortening the setting on a long history goes in batches too.
		if e := s.sweepRetention(ctx, class, `DELETE FROM play_history WHERE id IN(SELECT id FROM play_history WHERE started_ms<? ORDER BY started_ms LIMIT `+strconv.Itoa(retentionBatch)+`)`, now-days(keep)); e != nil {
			return e
		}
	}
	// The notification inbox prunes with its own revision bookkeeping, so a
	// client parked on a revision learns that its counts moved.
	for {
		var removed int
		if e := dbwork.WithWriteTx(ctx, s.DB, class, func(tx *sql.Tx) error {
			var e error
			removed, e = s.pruneNotifications(tx, now, v.Effective.NotificationDays)
			return e
		}); e != nil {
			return e
		}
		if removed < retentionBatch {
			return nil
		}
		if !dbwork.Yield(ctx) {
			return ctx.Err()
		}
	}
}

// sweepRetention repeats one bounded delete, each batch its own transaction,
// until a batch removes fewer than retentionBatch rows.
func (s *Store) sweepRetention(ctx context.Context, class dbwork.Class, statement string, args ...any) error {
	for {
		result, e := dbwork.ExecWrite(ctx, s.DB, class, statement, args...)
		if e != nil {
			return e
		}
		if n, _ := result.RowsAffected(); n < retentionBatch {
			return nil
		}
		if !dbwork.Yield(ctx) {
			return ctx.Err()
		}
	}
}
