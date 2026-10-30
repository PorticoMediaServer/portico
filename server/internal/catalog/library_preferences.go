package catalog

import (
	"context"
	"database/sql"
	"errors"
)

// LibraryPreferences holds library choices that have no independent domain
// policy. Audio local evidence remains in audio_metadata_policies.
type LibraryPreferences struct {
	GroupCollections   bool
	LocalTags          string
	LyricsSource       string
	TitleSource        string
	EpisodeNumbering   string
	FindExtras         bool
	SortIgnoreArticles bool
	Revision           int64
}

func DefaultLibraryPreferences() LibraryPreferences {
	return LibraryPreferences{GroupCollections: true, LocalTags: "prefer", LyricsSource: "files", TitleSource: "tags", EpisodeNumbering: "season", FindExtras: true, SortIgnoreArticles: true, Revision: 1}
}
func LibraryPreferencesTx(ctx context.Context, tx *sql.Tx, library string) (LibraryPreferences, error) {
	v := DefaultLibraryPreferences()
	err := tx.QueryRowContext(ctx, `SELECT revision,group_collections,local_tags,lyrics_source,title_source,episode_numbering,find_extras,sort_ignore_articles FROM library_preferences WHERE library_id=?`, library).Scan(&v.Revision, &v.GroupCollections, &v.LocalTags, &v.LyricsSource, &v.TitleSource, &v.EpisodeNumbering, &v.FindExtras, &v.SortIgnoreArticles)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return v, err
}
func ApplyLibraryPreferencesTx(ctx context.Context, tx *sql.Tx, library string, v LibraryPreferences) error {
	old, err := LibraryPreferencesTx(ctx, tx, library)
	if err != nil {
		return err
	}
	v.Revision = old.Revision
	if v == old {
		return nil
	}
	local := v.LocalTags
	// Audio has one existing local-mode authority, including supplement.
	if local == "supplement" {
		local = "prefer"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO library_preferences(library_id,group_collections,local_tags,lyrics_source,title_source,episode_numbering,find_extras,sort_ignore_articles) VALUES(?,?,?,?,?,?,?,?)
 ON CONFLICT(library_id) DO UPDATE SET revision=revision+1,group_collections=excluded.group_collections,local_tags=excluded.local_tags,lyrics_source=excluded.lyrics_source,title_source=excluded.title_source,episode_numbering=excluded.episode_numbering,find_extras=excluded.find_extras,sort_ignore_articles=excluded.sort_ignore_articles`, library, v.GroupCollections, local, v.LyricsSource, v.TitleSource, v.EpisodeNumbering, v.FindExtras, v.SortIgnoreArticles)
	if err != nil {
		return err
	}
	if !v.GroupCollections {
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_entities SET retired=1 WHERE id IN(SELECT a.entity_id FROM catalog_automatic_collections a WHERE a.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND a.owner_edited=0)`, library); err != nil {
			return err
		}
	}
	if old.SortIgnoreArticles != v.SortIgnoreArticles {
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_entities SET sort_key=portico_sort_title(title,CASE WHEN ?=0 AND sort_title='' THEN title ELSE sort_title END,metadata_language),sort_head=substr(portico_sort_title(title,CASE WHEN ?=0 AND sort_title='' THEN title ELSE sort_title END,metadata_language),1,1) WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND sort_key<>portico_sort_title(title,CASE WHEN ?=0 AND sort_title='' THEN title ELSE sort_title END,metadata_language)`, v.SortIgnoreArticles, v.SortIgnoreArticles, library, v.SortIgnoreArticles); err != nil {
			return err
		}
	}
	// Wake only known provider sets. Hand-made and owner-edited collections retain
	// their identities and membership. SyncAutomaticCollections applies visibility.
	_, err = tx.ExecContext(ctx, `UPDATE catalog_automatic_collections SET dirty=1 WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?)`, library)
	return err
}

// LibraryAudienceTx projects current account-level access without a second writer.
func LibraryAudienceTx(ctx context.Context, tx *sql.Tx, library string) (LibraryAudience, error) {
	return libraryAudience(ctx, tx, library)
}
