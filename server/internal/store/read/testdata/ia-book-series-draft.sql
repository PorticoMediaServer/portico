-- Exact group C C2 accepted local book-series subset for the read plan fixture.
-- Group C is not embedded, so this is proposed fixture DDL only: no
-- canonical/embed/Capture claim and no recorded golden.
CREATE TABLE catalog_book_series_heads(
 book_id INTEGER PRIMARY KEY CHECK(book_id>0),
 library_id INTEGER NOT NULL CHECK(library_id>0),
 book_kind INTEGER NOT NULL DEFAULT 8 CHECK(book_kind=8),
 source_asset_id INTEGER NOT NULL CHECK(source_asset_id>0),
 source_info_epoch INTEGER NOT NULL CHECK(source_info_epoch>0),
 source_revision TEXT NOT NULL CHECK(length(source_revision)>0),
 local_policy_revision INTEGER NOT NULL CHECK(local_policy_revision>0),
 revision INTEGER NOT NULL CHECK(revision>0),
 member_count INTEGER NOT NULL CHECK(member_count>=0),
 member_bytes INTEGER NOT NULL CHECK(member_bytes>=0),
 member_digest BLOB NOT NULL CHECK(length(member_digest)=32),
 FOREIGN KEY(book_id,library_id,book_kind) REFERENCES catalog_subjects(id,library_id,kind) ON DELETE NO ACTION,
 FOREIGN KEY(library_id,source_asset_id) REFERENCES catalog_assets(library_id,id) ON DELETE NO ACTION
) STRICT;
CREATE INDEX catalog_book_series_source ON catalog_book_series_heads(library_id,source_asset_id,book_id);
CREATE TABLE catalog_book_series_members(
 book_id INTEGER NOT NULL REFERENCES catalog_book_series_heads(book_id) ON DELETE NO ACTION,
 ordinal INTEGER NOT NULL CHECK(ordinal>=0),
 series_name TEXT NOT NULL CHECK(length(series_name)>0),
 series_position TEXT NOT NULL,
 PRIMARY KEY(book_id,ordinal)
) WITHOUT ROWID, STRICT;
CREATE INDEX catalog_book_series_name ON catalog_book_series_members(series_name COLLATE BINARY,book_id,ordinal);
