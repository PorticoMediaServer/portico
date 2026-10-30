#!/usr/bin/env python3
"""Synthetic SQLite checks for P04A DDL only; not a Go migration/runtime test."""
from pathlib import Path
import re
import sqlite3

root = Path(__file__).resolve().parents[1] / "internal" / "persistence"
def first_sql(name: str) -> str:
    source = (root / name).read_text()
    return re.search(r"(?:db|tx)\.Exec\(`(.*?)`\)", source, re.S).group(1)

base = first_sql("database.go")
admin = first_sql("admin.go")
revisions = first_sql("revisions.go")
inventory = first_sql("library_inventory.go")
db = sqlite3.connect(":memory:")
db.execute("PRAGMA foreign_keys=ON")
for sql in (base, revisions, admin):
    db.executescript(sql)
db.execute("INSERT INTO libraries VALUES('library','Movies','movie','/one')")
db.execute("INSERT INTO jobs(id,library_id,status,created_at) VALUES('legacy','library','running','2026-09-06')")
db.executescript(inventory)
assert db.execute("SELECT source_id FROM inventory_runs WHERE job_id='legacy'").fetchone() == ("library",)
db.execute("INSERT INTO library_sources(id,library_id,name,configured_root,root) VALUES('second','library','Second','/two','/two')")
db.execute("INSERT INTO jobs(id,library_id,status,created_at) VALUES('second-job','library','running','2026-09-06')")
db.execute("INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) SELECT 'second-job',id,generation,incarnation,root_identity,1 FROM library_sources WHERE id='second'")
db.execute("INSERT INTO inventory_source_active VALUES('second','second-job')")
# A restart with two active sources must not recreate the obsolete library lock.
for sql in (base, revisions, admin, inventory):
    db.executescript(sql)
assert db.execute("SELECT count(*) FROM inventory_source_active").fetchone() == (2,)
assert not db.execute("SELECT 1 FROM sqlite_master WHERE name='one_scan'").fetchone()
db.execute("INSERT INTO items(id,library_id,title,kind) VALUES('item','library','Movie','movie')")
for source, asset, path in (("library", "a", "/one/movie.mp4"), ("second", "b", "/two/movie.mp4")):
    db.execute("INSERT INTO assets VALUES(?,?,10,20,'mp4','','',0,0,0,1)", (asset, path))
    db.execute("INSERT INTO item_assets(item_id,asset_id) VALUES('item',?)", (asset,))
    db.execute("INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT ?,id,?,incarnation,'movie.mp4','revision','{}',10,20 FROM library_sources WHERE id=?", ("object-" + asset, asset, source))

def available() -> int:
    return db.execute("SELECT max(available) FROM inventory_item_availability WHERE item_id='item'").fetchone()[0]

before = db.execute("SELECT * FROM inventory_objects ORDER BY id").fetchall()
assert available() == 1
db.execute("UPDATE library_sources SET health='offline' WHERE id='library'")
assert available() == 1, "one offline source must not hide its surviving version"
db.execute("UPDATE library_sources SET health='offline' WHERE id='second'")
assert available() == 0
db.execute("UPDATE library_sources SET health='healthy' WHERE id='second'")
assert available() == 1
db.execute("UPDATE library_sources SET incarnation='replacement',generation=generation+1 WHERE id='second'")
assert available() == 0, "old locations must not become current on replacement"
assert db.execute("SELECT * FROM inventory_objects ORDER BY id").fetchall() == before
# Policy handoff and warning completion persist independently of process memory.
db.execute("UPDATE library_scan_policies SET tier='file_list_only',operations_json='[]',revision=revision+1 WHERE library_id='library'")
assert db.execute("SELECT revision FROM inventory_policy_pending WHERE library_id='library'").fetchone() == (2,)
db.execute("UPDATE jobs SET status='complete_with_warnings' WHERE id='second-job'")
assert db.execute("SELECT finished_at IS NOT NULL FROM job_observations WHERE job_id='second-job'").fetchone() == (1,)
assert not db.execute("PRAGMA foreign_key_check").fetchall()
print("PASS: DDL replay, legacy adoption, multi-source restart, source-only availability, replacement fence, durable policy handoff, warning completion, foreign keys")
