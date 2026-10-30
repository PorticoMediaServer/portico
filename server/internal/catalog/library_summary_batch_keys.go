package catalog

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

// librarySummaryBatchFact is one library's identity row plus its raw Reading
// state. The Reading part is nullable because a movie library has no
// attribution row and a Reading library can be unready.
type librarySummaryBatchFact struct {
	handle, revision, orderRevision, generation, classification int64
	retaining                                                   bool
	state                                                       bool
	namespace, input, ready, rule                               int64
}

// librarySummaryBatchKeys is the batched form of librarySummaryKey. Callers
// pass the viewer's already-allowed libraries; the key carries no viewer
// identity, only that effective policy and each library's own facts.
//
// It performs a fixed number of statements for any number of libraries: the
// per-library identity/Reading row, the shared restricted-policy rows, and the
// shared domain rows, all through the caller's carried snapshot. It opens no
// snapshot, writer or cache entry, and returns the same conservative reason as
// librarySummaryKey for every library.
//
// A key whose reason is empty is exactly the key librarySummaryKey would build.
// A Reading key keeps the raw namespace/input/ready/handle even when its
// reason is non-empty, so a caller can construct its bounded partial window
// without another read. Other bypassed keys also carry the identity facts a
// caller needs; only empty-reason keys are value-identical to the legacy key.
// reasons is sparse: it has an entry only for a library that must bypass reuse.
func (s *Service) librarySummaryBatchKeys(ctx context.Context, viewer Viewer, libraries []Library) (keys map[string]summaryKey, reasons map[string]telemetry.SummaryCacheEvent, err error) {
	keys = make(map[string]summaryKey, len(libraries))
	reasons = make(map[string]telemetry.SummaryCacheEvent)
	if len(libraries) == 0 {
		return keys, reasons, nil
	}
	policy := summaryPolicyKey(viewer.Restrictions)
	for _, library := range libraries {
		key := summaryKey{DB: s.db, Namespace: s.state, Library: library.ID, Kind: library.Kind, Algorithm: summaryAlgorithm, Cap: librarySummaryCap, Policy: policy}
		copy(key.Kinds[:], summaryKinds(library.Kind))
		keys[library.ID] = key
	}
	if dbwork.Snapshot(ctx) == nil {
		for _, library := range libraries {
			reasons[library.ID] = telemetry.SummaryBypassSnapshot
		}
		return keys, reasons, nil
	}

	// Author memberships resolve through the creation-root identity ledger and
	// can consume another library's item attributes after root move/reuse, so
	// the existing conservative audiobook decision wins over every later fact.
	// Its row is still read: callers need the library handle even when they must
	// bypass the reusable value.
	active := viewer.Restrictions.Active()

	ids := make([]any, len(libraries))
	for i, library := range libraries {
		ids[i] = library.ID
	}
	rows, err := s.read().QueryContext(ctx, `SELECT c.library_id,c.id,COALESCE(r.revision,0),COALESCE(r.order_revision,0),COALESCE(g.generation,0),COALESCE(g.retaining,0),COALESCE(v.revision,0),a.namespace,a.input_generation,a.ready_generation,a.publication_rule_version
 FROM catalog_libraries c
 LEFT JOIN library_revisions r ON r.library_id=c.library_id
 LEFT JOIN reading_attribution_state a ON a.library_id=c.id
 LEFT JOIN catalog_generations g ON g.singleton=1
 LEFT JOIN catalog_summary_classification_revision v ON v.singleton=1
 WHERE c.library_id IN(`+placeholders(len(ids))+`) AND c.retired=0`, ids...)
	if err != nil {
		for _, library := range libraries {
			if library.Kind == "audiobook" && active {
				reasons[library.ID] = telemetry.SummaryBypassRestrictedMembership
				continue
			}
			reasons[library.ID] = telemetry.SummaryBypassIdentity
		}
		return nil, nil, err
	}
	facts := make(map[string]librarySummaryBatchFact, len(libraries))
	for rows.Next() {
		var id string
		var fact librarySummaryBatchFact
		var namespace, input, ready, rule sql.NullInt64
		if err = rows.Scan(&id, &fact.handle, &fact.revision, &fact.orderRevision, &fact.generation, &fact.retaining, &fact.classification, &namespace, &input, &ready, &rule); err != nil {
			break
		}
		if namespace.Valid && input.Valid && ready.Valid && rule.Valid {
			fact.state = true
			fact.namespace, fact.input, fact.ready, fact.rule = namespace.Int64, input.Int64, ready.Int64, rule.Int64
		}
		facts[id] = fact
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		for _, library := range libraries {
			if library.Kind == "audiobook" && active {
				reasons[library.ID] = telemetry.SummaryBypassRestrictedMembership
				continue
			}
			reasons[library.ID] = telemetry.SummaryBypassIdentity
		}
		return nil, nil, err
	}

	candidates := make([]Library, 0, len(libraries))
	for _, library := range libraries {
		fact, ok := facts[library.ID]
		if !ok {
			if library.Kind == "audiobook" && active {
				reasons[library.ID] = telemetry.SummaryBypassRestrictedMembership
				continue
			}
			reasons[library.ID] = telemetry.SummaryBypassIdentity
			continue
		}
		key := keys[library.ID]
		key.Handle, key.Revision, key.OrderRevision = fact.handle, fact.revision, fact.orderRevision
		key.Generation, key.Classification = fact.generation, fact.classification
		if isReadingLibrary(library.Kind) {
			key.Reading = summaryReading{Library: fact.handle, Kind: library.Kind, Mobi: library.Kind == "book" && s.readingMobiConfigured(), CB7: readingcap.CB7(), CBR: readingcap.CBR()}
			if fact.state {
				key.Reading.Namespace, key.Reading.Input, key.Reading.Ready = fact.namespace, fact.input, fact.ready
				key.Reading.Rule = int(fact.rule)
			}
		}
		keys[library.ID] = key
		if library.Kind == "audiobook" && active {
			reasons[library.ID] = telemetry.SummaryBypassRestrictedMembership
			continue
		}
		if fact.revision < 1 || fact.orderRevision < 1 || fact.classification < 1 {
			reasons[library.ID] = telemetry.SummaryBypassIdentity
			continue
		}
		if fact.retaining {
			reasons[library.ID] = telemetry.SummaryBypassRetaining
			continue
		}
		candidates = append(candidates, library)
	}

	// Restricted visibility reads cross-library members and the derived browse
	// rows, so these global facts are shared by every candidate in the snapshot.
	if active && len(candidates) > 0 {
		var epoch int64
		var dirty, failed bool
		err = s.read().QueryRowContext(ctx, `SELECT COALESCE((SELECT policy_epoch FROM catalog_facet_source_state WHERE singleton=1),0),EXISTS(SELECT 1 FROM catalog_dirty),EXISTS(SELECT 1 FROM catalog_derivation_failures)`).Scan(&epoch, &dirty, &failed)
		if err != nil {
			return nil, nil, err
		}
		if epoch < 1 {
			for _, library := range candidates {
				reasons[library.ID] = telemetry.SummaryBypassIdentity
			}
			candidates = nil
		} else {
			for _, library := range candidates {
				key := keys[library.ID]
				key.PolicyEpoch = epoch
				keys[library.ID] = key
			}
			switch {
			case dirty:
				for _, library := range candidates {
					reasons[library.ID] = telemetry.SummaryBypassDirty
				}
				candidates = nil
			case failed:
				for _, library := range candidates {
					reasons[library.ID] = telemetry.SummaryBypassFailure
				}
				candidates = nil
			}
		}
	}

	// Only browse-row existence (domain 18) and membership edges (domain 19)
	// belong in this key. Domain rows arrive in order, so the last flag wins
	// exactly as it does when librarySummaryKey scans them one at a time.
	if active && len(candidates) > 0 {
		var domainRows *sql.Rows
		domainRows, err = s.read().QueryContext(ctx, `SELECT domain,version,active_slot,rebuilding,cleaning,first_build FROM catalog_derivations WHERE domain IN(?,?) ORDER BY domain`, compactcatalog.DomainBrowseRows, compactcatalog.DomainBrowseEdges)
		domainReason := telemetry.SummaryCacheEvent("")
		if err != nil {
			domainReason = telemetry.SummaryBypassIdentity
		} else {
			var keyShape summaryKey
			for domainRows.Next() {
				var domain, slot int
				var version int64
				var rebuild, clean, first bool
				if err = domainRows.Scan(&domain, &version, &slot, &rebuild, &clean, &first); err != nil {
					break
				}
				if domain < 1 || domain >= len(keyShape.Domains) || version < 1 {
					domainReason = telemetry.SummaryBypassIdentity
					break
				}
				for _, library := range candidates {
					key := keys[library.ID]
					key.Domains[domain] = summaryDomain{Version: version, Slot: slot, Present: true}
					keys[library.ID] = key
				}
				if rebuild {
					domainReason = telemetry.SummaryBypassRebuild
				}
				if clean {
					domainReason = telemetry.SummaryBypassCleanup
				}
				if first {
					domainReason = telemetry.SummaryBypassFirstBuild
				}
			}
			if err == nil {
				err = domainRows.Err()
			}
			domainRows.Close()
			if err != nil {
				domainReason = telemetry.SummaryBypassIdentity
			}
		}
		if err != nil {
			return nil, nil, err
		}
		if domainReason != "" {
			for _, library := range candidates {
				reasons[library.ID] = domainReason
			}
			candidates = nil
		}
	}

	for _, library := range candidates {
		if !isReadingLibrary(library.Kind) {
			continue
		}
		fact := facts[library.ID]
		if !fact.state || fact.ready == 0 || fact.input != fact.ready || fact.rule != compactcatalog.ReadingProjectionRuleVersion {
			reasons[library.ID] = telemetry.SummaryBypassReading
			continue
		}
		key := keys[library.ID]
		key.Reading.Rule = compactcatalog.ReadingProjectionRuleVersion
		keys[library.ID] = key
	}
	return keys, reasons, nil
}
