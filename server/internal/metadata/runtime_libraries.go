package metadata

import (
	"context"
	"database/sql"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

type MetadataLibrarySettings struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Kind                 string   `json:"kind"`
	Revision             string   `json:"revision"`
	Agent                string   `json:"agent"`
	AgentOptions         []string `json:"agentOptions"`
	LyricsSource         string   `json:"lyricsSource"`
	EpisodeThumbnailMode string   `json:"episodeThumbnailMode"`
	ScrubPreviews        bool     `json:"scrubPreviews"`
	CanScrub             bool     `json:"canScrub"`
	Managed              bool     `json:"managed"`
}
type MetadataLibraryChange struct {
	ID                   string  `json:"id"`
	ExpectedRevision     string  `json:"expectedRevision"`
	Agent                *string `json:"agent,omitempty"`
	LyricsSource         *string `json:"lyricsSource,omitempty"`
	EpisodeThumbnailMode *string `json:"episodeThumbnailMode,omitempty"`
	ScrubPreviews        *bool   `json:"scrubPreviews,omitempty"`
}

func readMetadataLibrary(ctx context.Context, tx *sql.Tx, id string) (MetadataLibrarySettings, error) {
	var out MetadataLibrarySettings
	out.ID = id
	if err := tx.QueryRowContext(ctx, `SELECT name,kind,EXISTS(SELECT 1 FROM dvr_private_libraries WHERE library_id=l.id) FROM libraries l WHERE id=?`, id).Scan(&out.Name, &out.Kind, &out.Managed); err != nil {
		return out, err
	}
	agent, err := readLibraryAgent(ctx, tx, id)
	if err != nil {
		return out, err
	}
	prefs, err := catalog.LibraryPreferencesTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	scan, err := catalog.ScanPolicyTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	episode, err := catalog.EpisodeThumbnailPolicyTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	out.Agent = agent.Agent
	out.AgentOptions = []string{}
	for _, option := range agent.Agents {
		out.AgentOptions = append(out.AgentOptions, option.ID)
	}
	out.LyricsSource = prefs.LyricsSource
	if agent.Agent == AgentLocal {
		out.LyricsSource = "files"
	}
	out.EpisodeThumbnailMode = episode.Mode
	out.ScrubPreviews = scan.Allows("trickplay")
	out.CanScrub = scan.Allows("probe") && (out.Kind == "movie" || out.Kind == "tv" || out.Kind == "anime") && !out.Managed
	out.Revision = publicationDigest([]any{id, out.Kind, out.Managed, agent.Revision, agent.Agent, prefs, scan, episode.Mode})
	return out, nil
}
func readMetadataLibraries(ctx context.Context, tx *sql.Tx) ([]MetadataLibrarySettings, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM libraries ORDER BY position,id LIMIT 513`)
	if err != nil {
		return nil, false, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, false, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	if len(ids) > 512 {
		return nil, false, nil
	}
	out := []MetadataLibrarySettings{}
	for _, id := range ids {
		row, err := readMetadataLibrary(ctx, tx, id)
		if err != nil {
			return nil, false, err
		}
		out = append(out, row)
	}
	return out, true, nil
}
func applyMetadataLibraries(ctx context.Context, tx *sql.Tx, changes []MetadataLibraryChange) error {
	if len(changes) > 512 {
		return ErrRepairInput
	}
	seen := map[string]bool{}
	for _, change := range changes {
		if change.ID == "" || seen[change.ID] || !artDigest(change.ExpectedRevision) {
			return ErrRepairInput
		}
		seen[change.ID] = true
		current, err := readMetadataLibrary(ctx, tx, change.ID)
		if err != nil {
			return err
		}
		if current.Managed {
			return ErrRepairInput
		}
		if current.Revision != change.ExpectedRevision {
			return ErrScreenConflict
		}
		agent := current.Agent
		if change.Agent != nil {
			agent = *change.Agent
			if !slices.Contains(current.AgentOptions, agent) {
				return ErrRepairInput
			}
		}
		if change.LyricsSource != nil && (current.Kind != "music" || (*change.LyricsSource != "files" && *change.LyricsSource != "lrclib") || agent == AgentLocal && *change.LyricsSource != "files") {
			return ErrRepairInput
		}
		if change.EpisodeThumbnailMode != nil && ((current.Kind != "tv" && current.Kind != "anime") || !catalog.ValidEpisodeThumbnailMode(*change.EpisodeThumbnailMode)) {
			return ErrRepairInput
		}
		if change.ScrubPreviews != nil && ((current.Kind != "movie" && current.Kind != "tv" && current.Kind != "anime") || *change.ScrubPreviews && !current.CanScrub) {
			return ErrRepairInput
		}
		if change.Agent != nil {
			if err = ApplyLibraryAgentTx(ctx, tx, change.ID, agent); err != nil {
				return err
			}
		}
		if change.LyricsSource != nil {
			prefs, err := catalog.LibraryPreferencesTx(ctx, tx, change.ID)
			if err != nil {
				return err
			}
			prefs.LyricsSource = *change.LyricsSource
			if err = catalog.ApplyLibraryPreferencesTx(ctx, tx, change.ID, prefs); err != nil {
				return err
			}
		}
		if change.EpisodeThumbnailMode != nil {
			policy, err := catalog.EpisodeThumbnailPolicyTx(ctx, tx, change.ID)
			if err != nil {
				return err
			}
			if err = catalog.ApplyEpisodeThumbnailModeTx(ctx, tx, change.ID, *change.EpisodeThumbnailMode, policy.Revision); err != nil {
				return err
			}
		}
		if change.ScrubPreviews != nil {
			policy, err := catalog.ScanPolicyTx(ctx, tx, change.ID)
			if err != nil {
				return err
			}
			ops := slices.DeleteFunc(slices.Clone(policy.Operations), func(op string) bool { return op == "trickplay" })
			if *change.ScrubPreviews {
				ops = append(ops, "trickplay")
			}
			if policy.Tier != "file_list_only" {
				policy.Tier = "custom"
			}
			policy.Operations = ops
			if err = catalog.ApplyScanPolicyTx(ctx, tx, change.ID, policy); err != nil {
				return err
			}
		}
	}
	return nil
}
