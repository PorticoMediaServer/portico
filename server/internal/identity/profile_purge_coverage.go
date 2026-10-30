package identity

// Profile purge coverage is the compiled ledger behind the "no private child left"
// claim. Every table that carries a profile dimension is either:
//
//   - purged: named by a unit in profilePurgeRegistry (purgeCovered derives that
//     set from the registry, so it cannot drift from the plan), or
//   - classified here with the exact reason it is not deleted.
//
// A table that is neither in the registry nor in this ledger is a source gap, not
// a silent omission. The reconcile test walks the installed schema and fails when a
// profile-bearing table is missing from both lists.
type purgeClassification uint8

const (
	// purgeRetainedPolicy: a product ruling keeps the rows.
	purgeRetainedPolicy purgeClassification = iota + 1
	// purgeRetainedMedia: library media, recordings and their file bookkeeping.
	purgeRetainedMedia
	// purgeRetainedLibrary: library-wide scheduling/walk state whose cursor is not
	// profile ownership; scrubbing it blindly would corrupt the walk.
	purgeRetainedLibrary
	// purgeRetainedAccount: the account root, receipt capacity and installation/root
	// lifetime owners, which other phases/owners own.
	purgeRetainedAccount
	// purgeRetainedRuntime: short-lived runtime rows with no durable identifying
	// payload beyond a revision or policy snapshot.
	purgeRetainedRuntime
	// purgeRetainedOwnerCommand: an owner/primary-only command journal that is not
	// profile-private purge state (A1 selection actions).
	purgeRetainedOwnerCommand
)

type purgeCoverageEntry struct {
	table  string
	reason string
	class  purgeClassification
}

// purgeRetainedTables is compiled from the current source. None of these rows is
// counted as erased.
var purgeRetainedTables = []purgeCoverageEntry{
	// Product ruling.
	{"play_history", "retained by policy: owner history/activity only, never recommendations; no purge unit names it", purgeRetainedPolicy},
	{"play_history_time", "child of play_history; retained with the play rows", purgeRetainedPolicy},
	{"hosted_profile_erasure_receipts", "Hosted owner's erasure receipts; local purge never certifies Hosted deletion", purgeRetainedPolicy},
	{"hosted_profile_revisions", "Hosted owner's profile revisions; local purge never certifies Hosted deletion", purgeRetainedPolicy},

	// Library media and recording bookkeeping (not private; never deleted).

	{"live_allocations", "kind=recording media leases are retained unchanged; only kind=live viewer allocations are erased", purgeRetainedMedia},
	{"dvr_recordings", "retained by policy unchanged, including creator/profile_id, owner_key, paths, content and state", purgeRetainedMedia},
	{"dvr_owner_revisions", "retained-media list revision while any recording references owner_key; erased otherwise", purgeRetainedMedia},
	{"dvr_recording_layouts", "recorded-file layout receipts; owner_key is part of the real media path, so it is media bookkeeping, not private purge state", purgeRetainedMedia},
	{"live_reservations", "tuner reservations of retained recordings; disabled, never deleted", purgeRetainedMedia},

	// Library-wide scheduling/walk state. The four catalog walk tables are named by
	// registry scrub units that blank only an exact retiring personal key, so their
	// profile dimension is purged while the walk stays valid.
	{"notice_broadcast_work", "operations broadcast keyset position over accounts/profiles; it is not profile content, and blanking it would re-deliver notices to the rest of the account's profiles", purgeRetainedLibrary},
	{"social_rate", "non-identifying aggregate counters keyed by bucket/window", purgeRetainedLibrary},
	{"maintenance_table_bounds", "table row bounds and counters", purgeRetainedLibrary},
	{"prepared_media_jobs", "optimized library media keyed by an encoding preset, not a profile; only principal_json is scrubbed by the actor unit", purgeRetainedMedia},
	{"prepared_media_versions", "optimized library media keyed by an encoding preset, not a profile; media is never deleted", purgeRetainedMedia},
	{"live_source_dependencies", "polymorphic (kind,id) dependency rows with no owner column and no index on (kind,id); rule ids are not profile attribution", purgeRetainedLibrary},

	// Account, receipt capacity and lifetime owners.
	{"accounts", "account root; the member erasure phase owns it", purgeRetainedAccount},
	{"user_mutation_accounts", "per-account receipt capacity row; released by receipt deletion", purgeRetainedAccount},
	{"user_mutation_global", "global receipt capacity singleton", purgeRetainedAccount},
	{"security_purge_jobs", "the job itself; deleted by the final commit when the proof passes", purgeRetainedAccount},
	{"identity_installations", "installation incarnation/CAS owner; not profile-private", purgeRetainedAccount},
	{"identity_move_waiting", "account-move waiting row, owned by the move protocol", purgeRetainedAccount},
	{"identity_moves", "account-move journal, owned by the move protocol", purgeRetainedAccount},

	// Owner/primary-only command journal (A1). Confirmed executable commands only:
	// no query, traversal cursor or preview storage, and not deletable by a profile
	// erase.
	{"selection_action_groups", "A1 owner/primary command journal; confirmed executable commands, not profile-private purge rows", purgeRetainedOwnerCommand},
	{"selection_action_targets", "A1 command targets; erased only with the owner command lifecycle", purgeRetainedOwnerCommand},
	{"selection_action_files", "A1 command file list; not query membership or profile-private state", purgeRetainedOwnerCommand},
	{"selection_action_history", "A1 completed command history; owner-only, not deletable by profile erase", purgeRetainedOwnerCommand},

	// Runtime rows with no identifying payload left.
	{"remote_source_captures", "capture byte accounting; no profile id", purgeRetainedRuntime},
	{"remote_source_capture_policy", "capture policy snapshot; profile_* columns are policy values, not identifiers", purgeRetainedRuntime},
}

// purgeCovered reports whether the compiled registry names the table.
func purgeCovered(table string) bool {
	if table == "playback_start_preparations" || table == "playback_disc_lifecycle" {
		return true
	} // atomically removed with its actor mapper

	for i := range profilePurgeRegistry {
		if profilePurgeRegistry[i].table == table {
			return true
		}
	}
	return false
}

// profilePurgeCoverage returns the compiled retained/blocked ledger for the
// reconcile test and diagnostics. It never reads the database.
func profilePurgeCoverage() []purgeCoverageEntry {
	out := make([]purgeCoverageEntry, 0, len(purgeRetainedTables))
	out = append(out, purgeRetainedTables...)
	return out
}
