package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP-23's finite artwork GC family (229 mark, 230 finalize). Group E is
// embedded, so these definitions are qualified against the canonical schema;
// they make no Capture, adoption or schema-SHA claim.
//
// Every read is an exact primary/unique-key point or a reverse-index seek; the
// writes are two exact retirement CASes, two exact retiring-row deletes, one
// owner CAS and one receipt insert. There is no OFFSET, temp sort, filtered scan
// or recursive child walk. The receipt statements are each command's OWN read:
// only the frozen E labels 'gc_mark' and 'gc_finish' map to 229 and 230, while
// any other label at the same key maps to 0 and is refused.
//
// The source reverse fences use the embedded artwork_selections_digest,
// metadata_owner_history_artwork_digest and artwork_receipt_selected_digest/
// artwork_receipt_previous_digest indexes. The blob reverse fences use the
// embedded artwork_sources_small/large/ambient indexes, the material-slot digest
// reverse index and the existing embedded backup_blob_holds singleton.
const (
	ipAGCOwner          stmt.ID = "ip.art.gc.owner"
	ipAGCOwnerLive      stmt.ID = "ip.art.gc.owner_live"
	ipAGCOwnerCAS       stmt.ID = "ip.art.gc.owner_cas"
	ipAGCReceiptOctets  stmt.ID = "ip.art.gc.receipt_octets"
	ipAGCMarkReceipt    stmt.ID = "ip.art.gc.mark.receipt"
	ipAGCFinishReceipt  stmt.ID = "ip.art.gc.finish.receipt"
	ipAGCSourceFence    stmt.ID = "ip.art.gc.source_fence"
	ipAGCSourceSelected stmt.ID = "ip.art.gc.source_selected_receipt"
	ipAGCSourcePrevious stmt.ID = "ip.art.gc.source_previous_receipt"
	ipAGCSourceHistory  stmt.ID = "ip.art.gc.source_history"
	ipAGCSourceSelect   stmt.ID = "ip.art.gc.source_selection"
	ipAGCSourceIntent   stmt.ID = "ip.art.gc.source_intent"
	ipAGCBlobFence      stmt.ID = "ip.art.gc.blob_fence"
	ipAGCBlobSmall      stmt.ID = "ip.art.gc.blob_small"
	ipAGCBlobLarge      stmt.ID = "ip.art.gc.blob_large"
	ipAGCBlobAmbient    stmt.ID = "ip.art.gc.blob_ambient"
	ipAGCBlobSlot       stmt.ID = "ip.art.gc.blob_slot"
	ipAGCBackup         stmt.ID = "ip.art.gc.backup"
	ipAGCSourceMark     stmt.ID = "ip.art.gc.source_mark"
	ipAGCSourceFinish   stmt.ID = "ip.art.gc.source_finish"
	ipAGCBlobMark       stmt.ID = "ip.art.gc.blob_mark"
	ipAGCBlobFinish     stmt.ID = "ip.art.gc.blob_finish"
	ipAGCSaveMark       stmt.ID = "ip.art.gc.mark.save"
	ipAGCSaveFinish     stmt.ID = "ip.art.gc.finish.save"
)

func ipArtworkGCDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const other = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	incarnation := make([]byte, 16)
	sourceIncarnation := make([]byte, 16)
	sourceIncarnation[0] = 4
	defs := []stmt.Definition{
		{
			// The retained subject job is the command owner; its exact
			// incarnation, counters and lease are the receipt-owner witness.
			ID: ipAGCOwner, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM artwork_subject_jobs WHERE id=?`,
			Params: []stmt.Param{i("owner")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_subject_jobs"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901)}},
				{Name: "missing", Bindings: []any{int64(999)}},
			},
		},
		{
			// A fresh GC command re-proves the exact retained owner under a live
			// lease in its publish or retire phase. Phase is a witness, not
			// permission to select arbitrary rows.
			ID: ipAGCOwnerLive, SQL: `SELECT 1 FROM artwork_subject_jobs WHERE id=? AND command_incarnation=? AND coalesce(entity_id,0)=? AND coalesce(person_id,0)=? AND source_incarnation=? AND source_revision=? AND job_revision=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_lease_expires_ms>? AND phase IN('publish','retire')`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("entity"), i("person"), b("source_incarnation"), i("source_revision"), i("revision"), i("lease_owner"), i("lease_generation"), i("lease_until"), i("now")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_subject_jobs"}},
			Cohorts: []stmt.Cohort{
				{Name: "live", Bindings: []any{int64(901), incarnation, int64(778), int64(0), sourceIncarnation, int64(1), int64(1), int64(7), int64(1), int64(1 << 50), int64(2)}},
				{Name: "expired", Bindings: []any{int64(901), incarnation, int64(778), int64(0), sourceIncarnation, int64(1), int64(1), int64(7), int64(1), int64(1 << 50), int64(1 << 51)}},
			},
		},
		{
			// One exact owner CAS advances the command sequence and job revision;
			// it acknowledges the predecessor and never changes the lease.
			ID: ipAGCOwnerCAS, SQL: `UPDATE artwork_subject_jobs SET last_command_sequence=?,job_revision=job_revision+1 WHERE id=? AND command_incarnation=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND coalesce(entity_id,0)=? AND coalesce(person_id,0)=? AND source_incarnation=? AND source_revision=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_lease_expires_ms>? AND phase IN('publish','retire')`,
			Params:  []stmt.Param{i("sequence"), i("owner"), b("incarnation"), i("revision"), i("last"), i("acknowledged"), i("entity"), i("person"), b("source_incarnation"), i("source_revision"), i("lease_owner"), i("lease_generation"), i("lease_until"), i("now")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_subject_jobs"}},
			Cohorts: []stmt.Cohort{{Name: "cas", Bindings: []any{int64(1), int64(901), incarnation, int64(1), int64(0), int64(0), int64(778), int64(0), sourceIncarnation, int64(1), int64(7), int64(1), int64(1 << 50), int64(2)}}},
		},
		{
			// Point octet preflight before copying the scalar GC digest/token.
			// A GC receipt never pins an intent, selection digest, previous
			// digest, role, stage or history: those are all FKs to objects it
			// retires.
			ID: ipAGCReceiptOctets, SQL: `SELECT coalesce(octet_length(blob_digest),0),octet_length(operation_id),octet_length(request_digest),octet_length(result_digest),coalesce(octet_length(retirement_token),0),coalesce(octet_length(material_intent_id),0),coalesce(octet_length(selected_digest),0),coalesce(octet_length(previous_digest),0),coalesce(octet_length(role),0),coalesce(stage_id,0),coalesce(history_id,0) FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), incarnation, int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), incarnation, int64(2)}},
			},
		},
		{
			// Only a gc_mark row maps to 229. The scalar digest and retirement
			// token are the saved physical witness; target is after_ordinal.
			ID: ipAGCMarkReceipt, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'gc_mark' THEN 229 ELSE 0 END,accepted_revision,applied_ms,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,coalesce(blob_digest,''),after_ordinal,coalesce(storage_revision,0),retirement_token FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), incarnation, int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), incarnation, int64(2)}},
			},
		},
		{
			// Only a gc_finish row maps to 230; a mark row at the same key is
			// refused instead of replayed as completion.
			ID: ipAGCFinishReceipt, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'gc_finish' THEN 230 ELSE 0 END,accepted_revision,applied_ms,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,coalesce(blob_digest,''),after_ordinal,coalesce(storage_revision,0),retirement_token FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), incarnation, int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), incarnation, int64(2)}},
			},
		},
		{
			// The source-level fence: the exact accepted source is returned only
			// while all three encoded blobs are still ready custody. A retiring or
			// replacing source, or a source whose blob is already unlinking, cannot
			// be marked or finalized because the join yields no row.
			ID: ipAGCSourceFence, SQL: `SELECT s.revision,s.retiring,coalesce(octet_length(s.retirement_token),0),s.retirement_token,s.original_bytes FROM artwork_sources s JOIN artwork_blobs sm ON sm.digest=s.small_blob AND sm.status='ready' AND sm.deletion_token IS NULL JOIN artwork_blobs lg ON lg.digest=s.large_blob AND lg.status='ready' AND lg.deletion_token IS NULL JOIN artwork_blobs am ON am.digest=s.ambient_blob AND am.status='ready' AND am.deletion_token IS NULL WHERE s.digest=?`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_sources", "artwork_blobs"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			// Exact selection/history/receipt/intent reverse probes. Each must be
			// absent before a source is marked, and again before its row is
			// acknowledged deleted. INDEXED BY pins the reviewed reverse index.
			ID: ipAGCSourceSelected, SQL: `SELECT 1 FROM artwork_command_receipts INDEXED BY artwork_receipt_selected_digest WHERE selected_digest=? LIMIT 1`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"artwork_receipt_selected_digest"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			ID: ipAGCSourcePrevious, SQL: `SELECT 1 FROM artwork_command_receipts INDEXED BY artwork_receipt_previous_digest WHERE previous_digest=? LIMIT 1`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"artwork_receipt_previous_digest"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			ID: ipAGCSourceHistory, SQL: `SELECT 1 FROM metadata_owner_history_artwork INDEXED BY metadata_owner_history_artwork_digest WHERE digest=? LIMIT 1`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_owner_history_artwork_digest"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			ID: ipAGCSourceSelect, SQL: `SELECT 1 FROM artwork_selections INDEXED BY artwork_selections_digest WHERE digest=? LIMIT 1`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"artwork_selections_digest"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			// Any retained material intent in a live state still charges the
			// source: an abandoned, installed or published intent must complete
			// its own reclamation before the source row can be acknowledged gone.
			ID: ipAGCSourceIntent, SQL: `SELECT 1 FROM artwork_material_intents INDEXED BY artwork_material_intent_source WHERE source_digest=? AND state IN('prepared','installed','published','retiring') LIMIT 1`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"artwork_material_intent_source"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{other}},
			},
		},
		{
			ID: ipAGCBlobFence, SQL: `SELECT storage_revision,bytes,status,coalesce(octet_length(deletion_token),0),deletion_token FROM artwork_blobs WHERE digest=?`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_blobs"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			// Any source that still names this blob on any encoded slot keeps it
			// charged, including a retiring source that has not been acknowledged
			// deleted yet.
			ID: ipAGCBlobSmall, SQL: `SELECT 1 FROM artwork_sources INDEXED BY artwork_sources_small WHERE small_blob=? LIMIT 1`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"artwork_sources_small"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			ID: ipAGCBlobLarge, SQL: `SELECT 1 FROM artwork_sources INDEXED BY artwork_sources_large WHERE large_blob=? LIMIT 1`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"artwork_sources_large"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			ID: ipAGCBlobAmbient, SQL: `SELECT 1 FROM artwork_sources INDEXED BY artwork_sources_ambient WHERE ambient_blob=? LIMIT 1`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"artwork_sources_ambient"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			// An installed or abandoned manifest slot still charges its exact
			// bytes until that material generation retires its own slot.
			ID: ipAGCBlobSlot, SQL: `SELECT 1 FROM artwork_material_slots INDEXED BY artwork_material_slot_digest WHERE digest=? LIMIT 1`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"artwork_material_slot_digest"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			// The existing embedded backup hold proof. A backup copies artwork
			// files: no mark may start beside it and no finalize may race it.
			ID: ipAGCBackup, SQL: `SELECT 1 FROM backup_blob_holds WHERE singleton=1`,
			Params:  nil,
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"backup_blob_holds"}},
			Cohorts: []stmt.Cohort{{Name: "present", Bindings: nil}, {Name: "missing", Bindings: nil}},
		},
		{
			// Marking a source is permission to attempt its exact externally-held
			// original under this revision; it is not physical completion.
			ID: ipAGCSourceMark, SQL: `UPDATE artwork_sources SET retiring=1,revision=revision+1,retirement_token=? WHERE digest=? AND revision=? AND retiring=0 AND retirement_token IS NULL AND original_bytes=?`,
			Params:  []stmt.Param{b("token"), t("digest"), i("revision"), i("bytes")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_sources"}},
			Cohorts: []stmt.Cohort{{Name: "marked", Bindings: []any{incarnation, digest, int64(1), int64(2 << 20)}}},
		},
		{
			// The source row is acknowledged only after the actual original file
			// exit: the exact retiring revision, token and byte count must still
			// match. Encoded blobs keep their own separate custody.
			ID: ipAGCSourceFinish, SQL: `DELETE FROM artwork_sources WHERE digest=? AND revision=? AND retiring=1 AND retirement_token=? AND original_bytes=?`,
			Params:  []stmt.Param{t("digest"), i("revision"), b("token"), i("bytes")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_sources"}},
			Cohorts: []stmt.Cohort{{Name: "deleted", Bindings: []any{digest, int64(2), incarnation, int64(2 << 20)}}},
		},
		{
			// A blob mark pairs its deletion token with the existing status; a
			// ready/replacement storage revision is never overwritten.
			ID: ipAGCBlobMark, SQL: `UPDATE artwork_blobs SET status='deleting',storage_revision=storage_revision+1,deletion_token=? WHERE digest=? AND storage_revision=? AND bytes=? AND status='ready' AND deletion_token IS NULL`,
			Params:  []stmt.Param{b("token"), t("digest"), i("revision"), i("bytes")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_blobs"}},
			Cohorts: []stmt.Cohort{{Name: "marked", Bindings: []any{incarnation, digest, int64(1), int64(40 << 10)}}},
		},
		{
			// The blob row is acknowledged only after the exact file exit; the
			// token and marked storage revision make a stale retry a no-op.
			ID: ipAGCBlobFinish, SQL: `DELETE FROM artwork_blobs WHERE digest=? AND storage_revision=? AND bytes=? AND status='deleting' AND deletion_token=?`,
			Params:  []stmt.Param{t("digest"), i("revision"), i("bytes"), b("token")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_blobs"}},
			Cohorts: []stmt.Cohort{{Name: "deleted", Bindings: []any{digest, int64(2), int64(40 << 10), incarnation}}},
		},
		{
			// A mark receipt carries the minted token and the new retiring
			// revision; it never populates selected/previous (they are source
			// FKs) or a material intent (a material FK).
			ID: ipAGCSaveMark, SQL: `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,blob_digest,after_ordinal,retirement_token,storage_revision) VALUES(?,?,?,?,?,?,?,'gc_mark',?,1,?,0,?,?,?,?,?,?)`,
			Params:  []stmt.Param{i("owner"), b("incarnation"), i("sequence"), b("operation_id"), b("request_digest"), i("applied_ms"), b("result_digest"), i("revision"), i("bytes"), i("lease_generation"), i("lease_until"), t("digest"), i("target"), b("token"), i("storage_revision")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{int64(901), incarnation, int64(1), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), int64(1), int64(2 << 20), int64(1), int64(1 << 50), digest, int64(1), make([]byte, 16), int64(2)}}},
		},
		{
			// The finalize receipt retains the same scalar token and revision so
			// replay is exact after the row itself is physically gone.
			ID: ipAGCSaveFinish, SQL: `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,blob_digest,after_ordinal,retirement_token,storage_revision) VALUES(?,?,?,?,?,?,?,'gc_finish',?,1,?,1,?,?,?,?,?,?)`,
			Params:  []stmt.Param{i("owner"), b("incarnation"), i("sequence"), b("operation_id"), b("request_digest"), i("applied_ms"), b("result_digest"), i("revision"), i("bytes"), i("lease_generation"), i("lease_until"), t("digest"), i("target"), b("token"), i("storage_revision")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{int64(901), incarnation, int64(2), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), int64(2), int64(40 << 10), int64(1), int64(1 << 50), digest, int64(2), make([]byte, 16), int64(2)}}},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipAGCOwnerCAS, ipAGCSourceMark, ipAGCSourceFinish, ipAGCBlobMark, ipAGCBlobFinish,
			ipAGCSaveMark, ipAGCSaveFinish:
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP artwork GC"
		defs[n].CallSites = []string{"store/writer/ip_artwork_gc_native.go"}
	}
	return defs
}
