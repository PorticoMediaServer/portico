import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import plan

class QueueRepository(unittest.TestCase):
    """A queue of two groups, run against a real repository: main, then group 1 (Go), then group 2 (web only) on top of it."""

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(); self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.git('init', '-q', '-b', 'main'); self.git('config', 'user.name', 'T'); self.git('config', 'user.email', 't@example.invalid')
        self.main = self.commit('server/go.mod', 'module m\n')
        self.git('update-ref', 'refs/remotes/origin/main', self.main)
        self.first = self.commit('server/internal/a/a.go', 'package a\n')
        self.second = self.commit('web/src/b.ts', 'export {}\n')

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, text=True).strip()

    def commit(self, name, text):
        (self.root / name).parent.mkdir(parents=True, exist_ok=True); (self.root / name).write_text(text)
        self.git('add', '-A'); self.git('commit', '-q', '-m', name)
        return self.git('rev-parse', 'HEAD')

    def plan(self, group, head=None):
        event = {'merge_group': {'base_ref': 'refs/heads/main', **group}}
        with patch('plan.command', side_effect=lambda args, cwd=None: subprocess.check_output(args, cwd=self.root, text=True)):
            return plan.base_head(event, {'GITHUB_EVENT_NAME': 'merge_group'})

    def changed(self, base, head):
        return [p for p in self.git('diff', '--name-only', base, head).splitlines()]

    def test_a_group_behind_another_plans_everything_it_carries_beyond_main(self):
        # Its own base_sha is group 1's head, so the old plan saw only the web file and ran no Go.
        base, head = self.plan({'base_sha': self.first, 'head_sha': self.second})
        self.assertEqual((base, head), (self.main, self.second))
        self.assertEqual(self.changed(self.first, self.second), ['web/src/b.ts'])
        self.assertEqual(self.changed(base, head), ['server/internal/a/a.go', 'web/src/b.ts'])

    def test_the_first_group_and_a_group_after_main_advanced_plan_against_main(self):
        self.git('checkout', '-q', self.first)
        self.assertEqual(self.plan({'base_sha': self.main, 'head_sha': self.first})[0], self.main)
        self.git('update-ref', 'refs/remotes/origin/main', self.first)  # group 1 merged
        self.git('checkout', '-q', self.second)
        self.assertEqual(self.plan({'base_sha': self.first, 'head_sha': self.second})[0], self.first)

    def test_a_checkout_that_is_not_the_group_head_is_refused(self):
        with self.assertRaisesRegex(ValueError, 'checkout does not match'):
            self.plan({'base_sha': self.first, 'head_sha': self.first})

    def test_a_group_without_a_branch_or_with_an_unknown_one_fails_closed(self):
        with self.assertRaisesRegex(ValueError, 'target branch'):
            with patch('plan.command', side_effect=lambda args, cwd=None: subprocess.check_output(args, cwd=self.root, text=True)):
                plan.base_head({'merge_group': {'base_sha': self.first, 'head_sha': self.second, 'base_ref': 'main'}}, {'GITHUB_EVENT_NAME': 'merge_group'})
        self.git('update-ref', '-d', 'refs/remotes/origin/main')
        with self.assertRaises(subprocess.CalledProcessError):  # no origin to fetch the branch from either
            self.plan({'base_sha': self.first, 'head_sha': self.second})

if __name__ == '__main__': unittest.main()
