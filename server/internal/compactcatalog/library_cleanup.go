package compactcatalog

import (
	"context"
	"database/sql"
)

// ClearLibraryTx clears one bounded batch, child projections before their class.
// Source titles must already be removed by the library removal job.
//
// Every statement here reaches its rows through an index: this runs once per
// batch, so a statement that read a whole table would make removing a library
// cost the size of every other library, many times over. Three consequences:
//
//   - A visibility class belongs to one library and every row of a class
//     carries that library, so the class's projections are cleared through
//     the class alone.
//   - Rows kept per title (counted rows, facet members, eligibility,
//     recommendation postings, personal sort rows) and the totals built from
//     them are not swept here. The derivations own them: each one sees its
//     title gone and takes the row and its share of the total away together.
//     Sweeping only one side would leave the other to fail.
//   - Until that work has run, such rows still name the removed library's
//     handle. A handle is never given to a later library (AUTOINCREMENT on
//     catalog_libraries), so they can never be read as another library's.
func ClearLibraryTx(ctx context.Context, tx *sql.Tx, library string, limit int) (bool, error) {
	for _, query := range []string{
		`DELETE FROM compact_visibility_source_jobs WHERE (kind,library_id,subject_id,subject_text) IN(SELECT kind,library_id,subject_id,subject_text FROM compact_visibility_source_jobs WHERE kind=1 AND library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		`DELETE FROM catalog_book_group_visible_counts WHERE (class_id,generation,group_id) IN(SELECT class_id,generation,group_id FROM catalog_book_group_visible_counts WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_book_group_visible_edges WHERE (class_id,generation,group_id,book_id) IN(SELECT class_id,generation,group_id,book_id FROM catalog_book_group_visible_edges WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_book_visible_counts WHERE (class_id,generation,book_id) IN(SELECT class_id,generation,book_id FROM catalog_book_visible_counts WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_book_visible_file_edges WHERE (class_id,generation,file_id) IN(SELECT class_id,generation,file_id FROM catalog_book_visible_file_edges WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_collection_visible_counts WHERE (class_id,generation,collection_id) IN(SELECT class_id,generation,collection_id FROM catalog_collection_visible_counts WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_collection_visible_edges WHERE (class_id,generation,collection_id,item_id) IN(SELECT class_id,generation,collection_id,item_id FROM catalog_collection_visible_edges WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_collection_visible_item_jobs WHERE (class_id,generation,item_id) IN(SELECT class_id,generation,item_id FROM catalog_collection_visible_item_jobs WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_movie_category_visible_edges WHERE (class_id,generation,library_id,kind,value,item_id) IN(SELECT class_id,generation,library_id,kind,value,item_id FROM catalog_movie_category_visible_edges WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_movie_category_visible_item_jobs WHERE (class_id,generation,item_id) IN(SELECT class_id,generation,item_id FROM catalog_movie_category_visible_item_jobs WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_movie_category_visible_summaries WHERE (class_id,generation,library_id,kind,value) IN(SELECT class_id,generation,library_id,kind,value FROM catalog_movie_category_visible_summaries WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_personal_visibility_sort_buckets WHERE (profile_id,class_id,generation,library_id,kind,axis,value) IN(SELECT profile_id,class_id,generation,library_id,kind,axis,value FROM catalog_personal_visibility_sort_buckets INDEXED BY catalog_personal_visibility_sort_bucket_class WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_personal_visibility_sort_class_jobs WHERE (class_id,generation,entity_id) IN(SELECT class_id,generation,entity_id FROM catalog_personal_visibility_sort_class_jobs WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_personal_visibility_sort_edges WHERE (profile_id,class_id,generation,entity_id,axis) IN(SELECT profile_id,class_id,generation,entity_id,axis FROM catalog_personal_visibility_sort_edges WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_visibility_sort_buckets WHERE (class_id,generation,library_id,kind,axis,value) IN(SELECT class_id,generation,library_id,kind,axis,value FROM catalog_visibility_sort_buckets WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_visibility_sort_edges WHERE (class_id,generation,entity_id,axis) IN(SELECT class_id,generation,entity_id,axis FROM catalog_visibility_sort_edges WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_visibility_sort_pair_jobs WHERE (class_id,generation,entity_id) IN(SELECT class_id,generation,entity_id FROM catalog_visibility_sort_pair_jobs WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM compact_visibility_blocks WHERE (class_id,generation,library_id,kind,ordering,sort_value,entity_id) IN(SELECT class_id,generation,library_id,kind,ordering,sort_value,entity_id FROM compact_visibility_blocks WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM compact_visibility_counts WHERE (class_id,generation,library_id,kind,head,decade) IN(SELECT class_id,generation,library_id,kind,head,decade FROM compact_visibility_counts WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM compact_visibility_dirty WHERE (class_id,entity_id) IN(SELECT class_id,entity_id FROM compact_visibility_dirty WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM compact_visibility_rows WHERE (class_id,generation,entity_id) IN(SELECT class_id,generation,entity_id FROM compact_visibility_rows WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM compact_visibility_unavailable WHERE (class_id,generation,entity_id) IN(SELECT class_id,generation,entity_id FROM compact_visibility_unavailable WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM compact_visibility_unavailable_counts WHERE (class_id,generation,library_id,kind) IN(SELECT class_id,generation,library_id,kind FROM compact_visibility_unavailable_counts WHERE class_id IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1)) LIMIT ?2)`,
		`DELETE FROM catalog_asset_issues WHERE (library_id,asset_id) IN(SELECT library_id,asset_id FROM catalog_asset_issues WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		// A book's context goes with the book; what is left belongs to a book that could not be removed.
		`DELETE FROM catalog_book_context WHERE book_id IN(SELECT c.book_id FROM catalog_entities e INDEXED BY catalog_entities_library CROSS JOIN catalog_book_context c ON c.book_id=e.id WHERE e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		`DELETE FROM catalog_book_groups WHERE id IN(SELECT id FROM catalog_book_groups INDEXED BY catalog_book_groups_every WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		`DELETE FROM catalog_browse_blocks WHERE (library_id,kind,ordering,sort_value,entity_id) IN(SELECT library_id,kind,ordering,sort_value,entity_id FROM catalog_browse_blocks WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		`DELETE FROM catalog_browse_buckets WHERE (library_id,kind,container,axis,value,rating_key,label_key) IN(SELECT library_id,kind,container,axis,value,rating_key,label_key FROM catalog_browse_buckets WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		`DELETE FROM catalog_browse_counted_rows WHERE (entity_id) IN(SELECT entity_id FROM catalog_browse_counted_rows WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		`DELETE FROM catalog_browse_rows WHERE (entity_id) IN(SELECT entity_id FROM catalog_browse_rows WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		`DELETE FROM catalog_missing_titles WHERE (entity_id) IN(SELECT entity_id FROM catalog_missing_titles WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		`DELETE FROM catalog_movie_category_members WHERE (library_id,kind,value,item_id) IN(SELECT library_id,kind,value,item_id FROM catalog_movie_category_members WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		`DELETE FROM catalog_movie_category_summaries WHERE (library_id,kind,value) IN(SELECT library_id,kind,value FROM catalog_movie_category_summaries WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		`DELETE FROM catalog_movie_category_visible_pair_jobs WHERE (library_id,kind,value,item_id) IN(SELECT library_id,kind,value,item_id FROM catalog_movie_category_visible_pair_jobs WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		`DELETE FROM catalog_related_facets WHERE (entity_id,relation,provider,facet_id) IN(SELECT entity_id,relation,provider,facet_id FROM catalog_related_facets WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
		`DELETE FROM compact_visibility_classes WHERE (id) IN(SELECT id FROM compact_visibility_classes WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?1) LIMIT ?2)`,
	} {
		result, err := tx.ExecContext(ctx, query, library, limit)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if n > 0 {
			return false, nil
		}
	}
	return true, nil
}
