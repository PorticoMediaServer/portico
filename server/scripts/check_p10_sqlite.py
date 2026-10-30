#!/usr/bin/env python3
"""Supplemental cross-driver checks against the current append-only schema.

The Go persistence tests remain authoritative. This uses Python's SQLite engine
to catch migration SQL or trigger assumptions that accidentally depend on the
Go driver. It creates only disposable in-memory state.
"""
from pathlib import Path
import sqlite3
import unittest

MIGRATIONS = Path(__file__).resolve().parents[1] / 'internal' / 'persistence' / 'migrations'


class Boundaries(unittest.TestCase):
    def setUp(self):
        self.db = sqlite3.connect(':memory:')
        self.db.execute('PRAGMA foreign_keys=ON')
        # The sort-key function is registered by the Go driver in production.
        # These checks do not assert sort collation, only migration boundaries.
        self.db.create_function(
            'portico_sort_title', 3,
            lambda title, sort, language: (sort or title or '').casefold(),
            deterministic=True,
        )
        for path in sorted(MIGRATIONS.glob('*.sql')):
            self.db.executescript(path.read_text())
        self.db.execute(
            "INSERT INTO accounts(id,username,password_hash,profile_id,epoch) "
            "VALUES('account','owner',X'00','primary',1)"
        )
        self.db.commit()

    def tearDown(self):
        self.assertEqual(self.db.execute('PRAGMA integrity_check').fetchone()[0], 'ok')
        self.assertEqual(self.db.execute('PRAGMA foreign_key_check').fetchall(), [])
        self.db.close()

    def test_retired_credentials_are_absent_from_the_current_schema(self):
        names = {row[0] for row in self.db.execute(
            "SELECT name FROM sqlite_master WHERE type='table'"
        )}
        self.assertNotIn('sessions', names)
        self.assertNotIn('direct_account_sessions', names)
        self.assertNotIn('identity_step_up_proofs', names)
        self.assertIn('authorization_family_tokens', names)
        self.assertIn('identity_refresh_credentials', names)
        self.assertIn('identity_account_attempt_budget', names)
        self.assertIn('direct_profile_pin_attempts', names)

    def test_epoch_change_revokes_a_family_and_clears_profile_trust(self):
        db = self.db
        db.execute(
            "INSERT INTO direct_profile_trust VALUES"
            "('proof','account','primary','installation',1,1,1,'2099-01-01')"
        )
        db.execute(
            "INSERT INTO authorization_session_families "
            "VALUES('family','server','account','primary','local','owner',1,'2099-01-01',1,0)"
        )
        db.execute(
            "INSERT INTO authorization_family_tokens "
            "VALUES('viewer','family',1,'2099-01-01',0)"
        )
        db.execute("UPDATE accounts SET epoch=2 WHERE id='account'")
        self.assertEqual(db.execute(
            "SELECT revoked FROM authorization_session_families WHERE id='family'"
        ).fetchone()[0], 1)
        self.assertEqual(db.execute(
            "SELECT count(*) FROM direct_profile_trust WHERE account_id='account'"
        ).fetchone()[0], 0)

    def test_profile_capacity_remains_a_database_fence(self):
        for number in range(7):
            self.db.execute(
                "INSERT INTO direct_profiles(id,account_id,name) VALUES(?,'account','Child')",
                (f'child-{number}',),
            )
        with self.assertRaisesRegex(sqlite3.IntegrityError, 'profile_capacity'):
            self.db.execute(
                "INSERT INTO direct_profiles(id,account_id,name) "
                "VALUES('ninth','account','Too many')"
            )


if __name__ == '__main__':
    unittest.main()
