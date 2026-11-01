#!/usr/bin/env python3
"""Exercise the real Git object checkout guard without network or engine builds."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
TEXT = (ROOT / 'scripts/build-macos-ffmpeg-extras.sh').read_text()
FUNCTIONS = TEXT[:TEXT.index('export PKG_CONFIG_PATH=')]


def git(repository, *args, data=None):
    result = subprocess.run(['git', '-C', str(repository), *args], input=data,
                            capture_output=True, check=True)
    return result.stdout.decode().strip()


class SourceCheckout(unittest.TestCase):
    def fixture(self, root):
        repository = root / 'source'; repository.mkdir()
        git(repository, 'init', '--quiet')
        tree = git(repository, 'hash-object', '-w', '-t', 'tree', '--stdin', data=b'')
        def commit(message, parent=None):
            body = 'tree ' + tree + '\n' + ('parent ' + parent + '\n' if parent else '')
            body += ('author Justin Ehler <justin@justinehler.com> 1262304000 +0000\n'
                     'committer Justin Ehler <justin@justinehler.com> 1262304000 +0000\n\n' + message + '\n')
            return git(repository, 'hash-object', '-w', '-t', 'commit', '--stdin', data=body.encode())
        first = commit('Controlled source fixture')
        other = commit('Different controlled source fixture', first)
        tag_body = ('object ' + first + '\ntype commit\ntag fixture\n'
                    'tagger Justin Ehler <justin@justinehler.com> 1262304000 +0000\n\nPinned fixture\n')
        tag = git(repository, 'hash-object', '-w', '-t', 'tag', '--stdin', data=tag_body.encode())
        git(repository, 'update-ref', 'refs/tags/fixture', tag)
        git(repository, 'update-ref', 'refs/heads/main', first)
        git(repository, 'update-ref', 'refs/heads/other', other)
        recipe = root / 'recipe'; recipe.mkdir(); packages = recipe / 'packages'; packages.mkdir()
        return repository, recipe, packages, first, other, tag

    def clone(self, repository, recipe, packages, revision):
        (recipe / 'portico-macos-extras.lock.json').write_text(json.dumps({
            'fixture': {'repository': str(repository), 'commit': revision}}))
        return subprocess.run(['bash', '-eu', '-c', FUNCTIONS + '\nportico_clone fixture\n'],
                              env=dict(os.environ, CWD=str(recipe), PACKAGES=str(packages)),
                              capture_output=True, text=True)

    def test_exact_commit_and_annotated_tag_both_retain_the_original_object_and_peeled_commit(self):
        for kind in ('commit', 'tag'):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temporary:
                repo, recipe, packages, first, _, tag = self.fixture(Path(temporary))
                revision = tag if kind == 'tag' else first
                result = self.clone(repo, recipe, packages, revision)
                self.assertEqual(result.returncode, 0, result.stderr)
                record = json.loads((packages / 'portico-fixture-source.json').read_text())
                self.assertEqual(record, {'repository': str(repo), 'lockedObject': revision,
                                         'objectType': kind, 'checkedOutCommit': first})

    def test_an_existing_different_checkout_cannot_pass_the_object_guard(self):
        with tempfile.TemporaryDirectory() as temporary:
            repo, recipe, packages, first, other, tag = self.fixture(Path(temporary))
            self.assertEqual(self.clone(repo, recipe, packages, tag).returncode, 0)
            source = packages / 'portico-fixture'
            git(source, 'fetch', '--depth=1', 'origin', other)
            git(source, 'checkout', '--detach', 'FETCH_HEAD')
            self.assertEqual(git(source, 'rev-parse', 'HEAD'), other)
            self.assertNotEqual(self.clone(repo, recipe, packages, tag).returncode, 0)
            self.assertEqual(git(source, 'rev-parse', 'HEAD'), other)


if __name__ == '__main__':
    unittest.main()
