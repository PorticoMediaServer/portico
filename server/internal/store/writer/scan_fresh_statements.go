package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	scanFreshRun            stmt.ID = "scan.inventory_accept.fresh_run"
	scanFreshSource         stmt.ID = "scan.inventory_accept.fresh_source"
	scanFreshLibrary        stmt.ID = "scan.inventory_accept.fresh_library_inputs"
	scanFreshPolicy         stmt.ID = "scan.inventory_accept.fresh_policy"
	scanFreshJob            stmt.ID = "scan.inventory_accept.fresh_job"
	scanFreshDirectory      stmt.ID = "scan.inventory_accept.fresh_directory"
	scanFreshPath           stmt.ID = "scan.inventory_accept.fresh_path"
	scanFreshAssetRevision  stmt.ID = "scan.inventory_accept.asset_revision"
	scanFreshMembership     stmt.ID = "scan.inventory_accept.membership_clock"
	scanFreshDirectoryCAS   stmt.ID = "scan.inventory_accept.directory_cas"
	scanFreshRunCAS         stmt.ID = "scan.inventory_accept.run_cas"
	scanFreshJobCAS         stmt.ID = "scan.inventory_accept.job_cas"
	scanFreshProgressCAS    stmt.ID = "scan.inventory_accept.progress_cas"
	scanFreshLibraryCAS     stmt.ID = "scan.inventory_accept.library_cas"
	scanFreshMembershipCAS  stmt.ID = "scan.inventory_accept.membership_cas"
	scanFreshEntityFacts    stmt.ID = "scan.inventory_accept.entity_scalar_revision"
	scanFreshEntityInitial  stmt.ID = "scan.inventory_accept.entity_initial_keys"
	scanFreshScalarInsert   stmt.ID = "scan.inventory_accept.scalar_self_insert"
	scanFreshPhysicalInsert stmt.ID = "scan.inventory_accept.physical_self_insert"
	scanFreshAge            stmt.ID = "scan.inventory_accept.unrated_count"
	scanFreshAgeInsert      stmt.ID = "scan.inventory_accept.unrated_count_insert"
	scanFreshAgeCAS         stmt.ID = "scan.inventory_accept.unrated_count_cas"
	scanFreshFinalLinks     stmt.ID = "scan.inventory_accept.final_links"
	scanFreshFinalOwners    stmt.ID = "scan.inventory_accept.final_owners"
)

// Effects-only provider for W's central finite composition. History/current
// disclosure/ACK stay in W's existing provider; no duplicate IDs or registry.
func scanInventoryFreshDefinitions() []stmt.Definition {
	defs := append(scanInventoryDefinitions(), scanObjectDefinitions()...)
	defs = append(defs, scanFreshDefinitions()...)
	for _, d := range scanReceiptDefinitions() {
		if d.ID == scanReceiptInsert || d.ID == scanResultInsert {
			defs = append(defs, d)
		}
	}
	return append(defs, searchInputIntentDefinitions()...)
}

// No definition/cohort allocation is needed to inspect completeness inside a
// turn. Keep this finite inventory in lockstep with the effects-only provider.
func inventoryFreshIDs() [43]stmt.ID {
	return [43]stmt.ID{
		scanInventoryIdentity, scanInventoryAsset, scanInventorySubject, scanInventoryAlias, scanInventoryNewAsset, scanInventoryEntity, scanInventoryLink,
		scanInventoryObject, scanFreshRun, scanFreshSource, scanFreshLibrary, scanFreshPolicy, scanFreshJob, scanFreshDirectory, scanFreshPath,
		scanFreshAssetRevision, scanFreshMembership, scanFreshDirectoryCAS, scanFreshRunCAS, scanFreshJobCAS, scanFreshProgressCAS,
		scanFreshLibraryCAS, scanFreshMembershipCAS, scanFreshEntityFacts, scanFreshEntityInitial, scanFreshScalarInsert, scanFreshPhysicalInsert,
		scanFreshAge, scanFreshAgeInsert, scanFreshAgeCAS, scanFreshFinalLinks, scanFreshFinalOwners, scanReceiptInsert, scanResultInsert,
		searchInputHead, searchInputBank, searchInputEntity, searchInputSubject, searchInputExisting, searchInputInsert, searchInputPending, searchInputSequence, searchInputBuilding,
	}
}

// Exact per-command upper use counts, including both possible search banks.
// This is an effect inventory, NOT a numerical Work/grant/admission recipe.
// W owns all native row/VM/WAL/bytes/hold forecasts and terminal reserve.
func inventoryFreshMaxUses(id stmt.ID) int {
	switch id {
	case searchInputBank, searchInputInsert, searchInputPending:
		return 2
	default:
		for _, allowed := range inventoryFreshIDs() {
			if id == allowed {
				return 1
			}
		}
		return 0
	}
}

// These NEW-only points/CAS are fresh effects, never historical disclosure.
// W composes them with the existing history/catalogue/object/receipt/PB sets
// only under a real Capture receipt and its ONE complete command forecast.
func scanFreshDefinitions() []stmt.Definition {
	s := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	i := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	b := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Blob} }
	nullable := func(name string, kind stmt.Type) stmt.Param {
		return stmt.Param{Name: name, Type: kind, Nullable: true}
	}
	def := func(id stmt.ID, sql string, kind stmt.Kind, params []stmt.Param, args []any, table, seek string) stmt.Definition {
		p := stmt.Plan{Hot: true}
		if table != "" {
			p.PrimaryKeys, p.Seeks = []string{table}, []string{seek}
		}
		// Generated names are expected-plan assertions ONLY, never SQL hints.
		switch id {
		case scanFreshRun, scanFreshRunCAS:
			p.PrimaryKeys, p.Indexes, p.Seeks = nil, []string{"sqlite_autoindex_inventory_runs_2"}, []string{"job_id=? AND command_incarnation=?"}
		case scanFreshLibrary, scanFreshLibraryCAS:
			p.PrimaryKeys, p.Indexes = nil, []string{"sqlite_autoindex_libraries_1"}
		case scanFreshPolicy:
			p.PrimaryKeys, p.Indexes = nil, []string{"sqlite_autoindex_library_scan_policies_1"}
		case scanFreshJob, scanFreshJobCAS:
			p.PrimaryKeys, p.Indexes = nil, []string{"sqlite_autoindex_jobs_1"}
		case scanFreshSource, scanFreshProgressCAS:
			p.PrimaryKeys, p.Indexes = nil, []string{"sqlite_autoindex_library_sources_1"}
		case scanFreshDirectory, scanFreshDirectoryCAS:
			p.PrimaryKeys, p.Indexes = nil, []string{"sqlite_autoindex_inventory_directories_1"}
		case scanFreshPath:
			p.Indexes, p.Seeks = []string{"inventory_objects_path"}, []string{"source_id=? AND relative_path=?"}
		case scanFreshFinalOwners:
			p.Indexes, p.Seeks = []string{"catalog_asset_links_asset"}, []string{"asset_id=?"}
		}
		cohorts := []stmt.Cohort{{Name: "native_point_hit", Bindings: args}}
		if id == scanFreshScalarInsert || id == scanFreshPhysicalInsert || id == scanFreshAgeInsert {
			cohorts[0].Name = "new_native_row"
		} else {
			staleField := ""
			switch id {
			case scanFreshRun:
				staleField = "run_revision"
			case scanFreshRunCAS:
				staleField = "run_revision"
			case scanFreshSource, scanFreshProgressCAS:
				staleField = "source_generation"
			case scanFreshLibrary:
				staleField = "library_key"
			case scanFreshLibraryCAS:
				staleField = "expected_age_pending"
			case scanFreshPolicy:
				staleField = "policy_revision"
			case scanFreshJob:
				staleField = "run_id"
			case scanFreshJobCAS:
				staleField = "expected_processed"
			case scanFreshDirectory, scanFreshDirectoryCAS:
				staleField = "expected_cursor"
			case scanFreshPath:
				staleField = "relative_path"
			case scanFreshAssetRevision:
				staleField = "asset_id"
			case scanFreshMembershipCAS:
				staleField = "expected_sequence"
			case scanFreshEntityFacts, scanFreshEntityInitial:
				staleField = "entity_revision"
			case scanFreshAge:
				staleField = "library_id"
			case scanFreshAgeCAS:
				staleField = "expected_total"
			case scanFreshFinalLinks:
				staleField = "entity_id"
			case scanFreshFinalOwners:
				staleField = "asset_id"
			}
			if staleField != "" {
				stale := append([]any(nil), args...)
				for n, parameter := range params {
					if parameter.Name != staleField {
						continue
					}
					switch v := stale[n].(type) {
					case string:
						stale[n] = v + ":missing"
					case int64:
						stale[n] = v + 1
					}
				}
				name := "native_point_miss"
				if kind == stmt.Write {
					name = "native_cas_miss"
				}
				cohorts = append(cohorts, stmt.Cohort{Name: name, Bindings: stale})
			}
		}
		return stmt.Definition{ID: id, SQL: sql, Kind: kind, Params: params,
			Owner: "store/writer scan fresh owner", CallSites: []string{"store/writer/scan_fresh.go"}, Plan: p,
			Cohorts: cohorts}
	}
	runParams := []stmt.Param{s("run_id"), s("source_id"), i("source_generation"), s("root_incarnation"), s("root_identity"), i("policy_revision"),
		s("phase"), s("scan_mode"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms"), i("run_revision"), b("command_incarnation"), i("last_sequence"), i("ack_sequence")}
	runArgs := []any{"fresh-run", "fresh-source", int64(23), "fresh-incarnation", "dev:7:ino:1", int64(7), "inventory", "first-import",
		int64(13), int64(17), int64(4000000000000), int64(41), make([]byte, 16), int64(18), int64(18)}
	sourceParams := []stmt.Param{s("source_id"), s("library_key"), i("source_generation"), s("root_incarnation"), s("root_identity"), s("resolved_root"), i("follow_symlinks")}
	sourceArgs := []any{"fresh-source", "fresh-library", int64(23), "fresh-incarnation", "dev:7:ino:1", "/media", int64(0)}
	directoryParams := []stmt.Param{s("run_id"), s("directory_path"), s("directory_identity"), s("directory_revision"), s("expected_cursor"), i("expected_pages"), i("expected_skipped")}
	directoryArgs := []any{"fresh-run", ".", "directory:11", "directory:013", "", int64(5), int64(3)}
	return []stmt.Definition{
		def(scanFreshRun, `SELECT discovered FROM inventory_runs
WHERE job_id=? AND source_id=? AND source_generation=? AND root_incarnation=? AND root_identity=? AND policy_revision=?
AND phase=? AND scan_mode=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=?
AND job_revision=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?
AND authoritative=0 AND root_rebound=0 AND finished_at=''`, stmt.Read, runParams, runArgs, "inventory_runs", "job_id=?"),
		def(scanFreshSource, `SELECT id FROM library_sources
WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root_identity=? AND root=? AND follow_symlinks=?
AND kind='local' AND classification='local' AND enabled=1 AND identity_confirmed=1 AND health='ready' AND remote_source_id IS NULL`,
			stmt.Read, sourceParams, sourceArgs, "library_sources", "id=?"),
		def(scanFreshLibrary, `SELECT data_revision,data_order_revision,data_metadata_revision,age_input_revision,age_published_revision,age_pending_operations
FROM libraries WHERE id=? AND kind='movie' AND metadata_language=?`, stmt.Read,
			[]stmt.Param{s("library_key"), s("metadata_language")}, []any{"fresh-library", "en-US"}, "libraries", "id=?"),
		def(scanFreshPolicy, `SELECT revision FROM library_scan_policies WHERE library_id=? AND revision=?`, stmt.Read,
			[]stmt.Param{s("library_key"), i("policy_revision")}, []any{"fresh-library", int64(7)}, "library_scan_policies", "library_id=?"),
		def(scanFreshJob, `SELECT processed FROM jobs WHERE id=? AND library_id=? AND status='running'`, stmt.Read,
			[]stmt.Param{s("run_id"), s("library_key")}, []any{"fresh-run", "fresh-library"}, "jobs", "id=?"),
		def(scanFreshDirectory, `SELECT pages,skipped FROM inventory_directories
WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending'`,
			stmt.Read, directoryParams, directoryArgs, "inventory_directories", "job_id=? AND relative_path=?"),
		def(scanFreshPath, `SELECT id FROM inventory_objects WHERE source_id=? AND relative_path=? AND retired=0`, stmt.Read,
			[]stmt.Param{s("source_id"), s("relative_path")}, []any{"fresh-source", "Arrival.mkv"}, "", ""),
		def(scanFreshAssetRevision, `SELECT file_size_revision,info_epoch FROM catalog_assets
WHERE id=? AND library_id=? AND token=? AND path=? AND size=? AND modified_ns=?`, stmt.Read,
			[]stmt.Param{i("asset_id"), i("library_id"), s("asset_token"), s("asset_path"), i("size"), i("modified_ns")},
			[]any{int64(41), int64(7), "fresh-asset", "/media/Arrival.mkv", int64(42), int64(-11)}, "catalog_assets", "rowid=?"),
		def(scanFreshMembership, `SELECT sequence FROM catalog_membership_clock WHERE singleton=1`, stmt.Read, nil, nil, "catalog_membership_clock", "rowid=?"),
		def(scanFreshDirectoryCAS, `UPDATE inventory_directories SET cursor=?,pages=?
WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending'
RETURNING cursor,pages,skipped`, stmt.Write,
			append([]stmt.Param{s("actual_next_cursor"), i("next_pages")}, directoryParams...), append([]any{"42", int64(6)}, directoryArgs...),
			"inventory_directories", "job_id=? AND relative_path=?"),
		def(scanFreshRunCAS, `UPDATE inventory_runs SET discovered=?,job_revision=?,last_command_sequence=?
WHERE job_id=? AND source_id=? AND source_generation=? AND root_incarnation=? AND root_identity=? AND policy_revision=?
AND phase=? AND scan_mode=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=?
AND job_revision=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?
AND discovered=? AND authoritative=0 AND root_rebound=0 AND finished_at=''
RETURNING job_revision,discovered,last_command_sequence`, stmt.Write,
			append(append([]stmt.Param{i("next_discovered"), i("next_run_revision"), i("command_sequence")}, runParams...), i("expected_discovered")),
			append(append([]any{int64(60), int64(42), int64(19)}, runArgs...), int64(59)), "inventory_runs", "job_id=?"),
		def(scanFreshJobCAS, `UPDATE jobs SET processed=? WHERE id=? AND library_id=? AND processed=? AND status='running'`, stmt.Write,
			[]stmt.Param{i("next_processed"), s("run_id"), s("library_key"), i("expected_processed")}, []any{int64(68), "fresh-run", "fresh-library", int64(67)}, "jobs", "id=?"),
		def(scanFreshProgressCAS, `UPDATE library_sources SET progress_present=1,progress_last_progress_at=?
WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root_identity=? AND root=? AND follow_symlinks=?
AND kind='local' AND classification='local' AND enabled=1 AND identity_confirmed=1 AND health='ready' AND remote_source_id IS NULL`, stmt.Write,
			append([]stmt.Param{s("applied_at")}, sourceParams...), append([]any{"2026-11-06T00:00:00Z"}, sourceArgs...), "library_sources", "id=?"),
		def(scanFreshLibraryCAS, `UPDATE libraries SET data_revision=?,data_order_revision=?,data_metadata_revision=?,age_input_revision=?,age_published_revision=?
WHERE id=? AND data_revision=? AND data_order_revision=? AND data_metadata_revision=? AND age_input_revision=? AND age_published_revision=? AND age_pending_operations=?
AND kind='movie' AND metadata_language=?`, stmt.Write,
			[]stmt.Param{i("next_data_revision"), i("next_order_revision"), i("next_metadata_revision"), i("next_age_input_revision"), i("next_age_published_revision"), s("library_key"),
				i("expected_data_revision"), i("expected_order_revision"), i("expected_metadata_revision"), i("expected_age_input_revision"), i("expected_age_published_revision"), i("expected_age_pending"), s("metadata_language")},
			[]any{int64(72), int64(74), int64(80), int64(84), int64(84), "fresh-library", int64(71), int64(73), int64(79), int64(83), int64(83), int64(0), "en-US"}, "libraries", "id=?"),
		def(scanFreshMembershipCAS, `UPDATE catalog_membership_clock SET sequence=? WHERE singleton=1 AND sequence=? RETURNING sequence`, stmt.Write,
			[]stmt.Param{i("next_sequence"), i("expected_sequence")}, []any{int64(90), int64(89)}, "catalog_membership_clock", "rowid=?"),
		def(scanFreshEntityFacts, `SELECT scalar_source_revision,aggregate_revision,admission_revision FROM catalog_entities WHERE id=? AND library_id=? AND metadata_incarnation=? AND revision=? AND retired=0`, stmt.Read,
			[]stmt.Param{i("entity_id"), i("library_id"), b("metadata_incarnation"), i("entity_revision")}, []any{int64(31), int64(7), make([]byte, 16), int64(1)}, "catalog_entities", "rowid=?"),
		def(scanFreshEntityInitial, `UPDATE catalog_entities SET file_size_key=?,admission_allow_age=?,admission_known_age=?,admission_own_age=?,admission_member_count=?,admission_unrated_members=?,admission_min_known_age=?,
duration_key=0,community_rating_key=0,critic_rating_key=0,resolution_key=0,latest_aired_key='',episode_count=0,aggregate_publication_id=NULL
WHERE id=? AND library_id=? AND metadata_incarnation=? AND revision=? AND scalar_source_revision=? AND aggregate_revision=? AND admission_revision=? AND retired=0
RETURNING revision,scalar_source_revision,aggregate_revision,admission_revision,file_size_key,resolution_key,duration_key,community_rating_key,critic_rating_key,
latest_aired_key,episode_count,admission_allow_age,admission_known_age,admission_own_age,admission_member_count,admission_unrated_members,admission_min_known_age,aggregate_publication_id`, stmt.Write,
			[]stmt.Param{i("file_size"), nullable("allow_age", stmt.Integer), nullable("known_age", stmt.Integer), nullable("own_age", stmt.Integer), i("member_count"), i("unrated_members"), nullable("min_known_age", stmt.Integer),
				i("entity_id"), i("library_id"), b("metadata_incarnation"), i("entity_revision"), i("scalar_source_revision"), i("aggregate_revision"), i("admission_revision")},
			[]any{int64(42), int64(-1), nil, nil, int64(0), int64(0), nil, int64(31), int64(7), make([]byte, 16), int64(1), int64(1), int64(1), int64(1)}, "catalog_entities", "rowid=?"),
		def(scanFreshScalarInsert, `INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence,
duration,community_rating,critic_rating,resolution,latest_aired_key,admission_age) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, stmt.Write,
			[]stmt.Param{i("library_id"), i("parent_id"), i("contributor_id"), i("source"), i("contributor_revision"), i("membership_sequence"), nullable("duration", stmt.Real), nullable("community_rating", stmt.Real),
				nullable("critic_rating", stmt.Real), nullable("resolution", stmt.Integer), nullable("latest_aired_key", stmt.Text), nullable("admission_age", stmt.Integer)},
			[]any{int64(7), int64(31), int64(31), int64(0), int64(1), int64(90), nil, nil, nil, nil, nil, nil}, "", ""),
		def(scanFreshPhysicalInsert, `INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,authored_refs,file_size,file_size_revision) VALUES(?,?,?,?,?,?,?)`, stmt.Write,
			[]stmt.Param{i("library_id"), i("parent_id"), i("asset_id"), i("membership_refs"), i("authored_refs"), i("file_size"), i("file_size_revision")},
			[]any{int64(7), int64(31), int64(41), int64(1), int64(0), int64(42), int64(1)}, "", ""),
		def(scanFreshAge, `SELECT total FROM catalog_age_item_counts WHERE library_id=? AND kind=? AND block_unrated=? AND admission_age=?`, stmt.Read,
			[]stmt.Param{i("library_id"), i("kind"), i("block_unrated"), i("admission_age")}, []any{int64(7), int64(1), int64(0), int64(-1)}, "catalog_age_item_counts", "library_id=? AND kind=? AND block_unrated=? AND admission_age=?"),
		def(scanFreshAgeInsert, `INSERT INTO catalog_age_item_counts(library_id,kind,block_unrated,admission_age,total) VALUES(?,?,?,?,?)`, stmt.Write,
			[]stmt.Param{i("library_id"), i("kind"), i("block_unrated"), i("admission_age"), i("total")}, []any{int64(8), int64(1), int64(0), int64(-1), int64(1)}, "", ""),
		def(scanFreshAgeCAS, `UPDATE catalog_age_item_counts SET total=? WHERE library_id=? AND kind=? AND block_unrated=? AND admission_age=? AND total=?`, stmt.Write,
			[]stmt.Param{i("next_total"), i("library_id"), i("kind"), i("block_unrated"), i("admission_age"), i("expected_total")},
			[]any{int64(98), int64(7), int64(1), int64(0), int64(-1), int64(97)}, "catalog_age_item_counts", "library_id=? AND kind=? AND block_unrated=? AND admission_age=?"),
		def(scanFreshFinalLinks, `SELECT library_id,entity_id,asset_id,cue_ordinal,start_seconds,end_seconds,part_index,available,info_shape_id,info_pending_change,info_duration
FROM catalog_asset_links WHERE entity_id=? ORDER BY asset_id LIMIT ?`, stmt.Read,
			[]stmt.Param{i("entity_id"), i("limit")}, []any{int64(31), int64(2)}, "catalog_asset_links", "entity_id=?"),
		def(scanFreshFinalOwners, `SELECT entity_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=? ORDER BY entity_id LIMIT ?`, stmt.Read,
			[]stmt.Param{i("asset_id"), i("limit")}, []any{int64(41), int64(2)}, "", ""),
	}
}
