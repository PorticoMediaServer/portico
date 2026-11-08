import io
import json
import shutil
import subprocess
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest.mock import patch

import plan_pins

SHA = 'a' * 64

PLAN = '''package writer

import "crypto/sha256"

func TestFrozenPlans(t *testing.T) {
\tparts := []struct{ name, sha string }{
\t\t{"basic_begin.go", "%(sha)s"},
\t\t{"basic_begin_test.go", "%(sha)s"},
\t}
\tsum := sha256.Sum256(nil)
\t_ = sum
}
'''

FUNCTIONS = '''package writer

import "crypto/sha256"

func TestFixtureFunctions(t *testing.T) {
\tfor _, entry := range []struct{ path, name string }{{"fixture_a_test.go", "fixtureA"}, {"fixture_b_test.go", "fixtureB"}} {
\t\t_ = entry
\t}
\t_ = sha256.Sum256(nil)
}
'''


def writer_root(tmp):
    root = Path(tmp)
    writer = root / plan_pins.WRITER
    writer.mkdir(parents=True)
    return root, writer


def changes_file(root, text, name='changes.txt'):
    path = root / name
    path.write_text(text)
    return str(path)


def run(argv, root):
    out, err = io.StringIO(), io.StringIO()
    with redirect_stdout(out), redirect_stderr(err):
        code = plan_pins.main(argv + ['--root', str(root)])
    return code, out.getvalue(), err.getvalue()


def line_of(text, needle):
    return text[:text.index(needle)].count('\n') + 1


class PlanPins(unittest.TestCase):
    def test_pinned_file_is_named_with_its_plan_test_and_line(self):
        plan = PLAN % {'sha': SHA}
        with tempfile.TemporaryDirectory() as tmp:
            root, writer = writer_root(tmp)
            (writer / 'basic_begin_plan_test.go').write_text(plan)
            changes = changes_file(root, 'M server/internal/store/writer/basic_begin.go\n')
            code, out, _ = run(['--changed-files', changes], root)
            self.assertEqual(code, 0)
            line = line_of(plan, '"basic_begin.go"')
            self.assertIn('PINNED: server/internal/store/writer/basic_begin.go is hash-pinned by '
                          'server/internal/store/writer/basic_begin_plan_test.go:%d; ' % line
                          + 'add a new file/wrapper or re-record the pin with a "no plan change" statement', out)
            self.assertIn('1 changed file(s) touch 1 pin(s) in 1 plan test(s)', out)

    def test_unpinned_file_is_not_named(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, writer = writer_root(tmp)
            (writer / 'basic_begin_plan_test.go').write_text(PLAN % {'sha': SHA})
            changes = changes_file(root, 'M server/internal/store/writer/elsewhere.go\n')
            code, out, _ = run(['--changed-files', changes], root)
            self.assertEqual(code, 0)
            self.assertNotIn('PINNED:', out)
            self.assertIn('none of the 1 changed file(s) is hash-pinned', out)

    def test_renamed_file_names_the_old_pin(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, writer = writer_root(tmp)
            plan = PLAN % {'sha': SHA}
            (writer / 'basic_begin_plan_test.go').write_text(plan)
            changes = changes_file(root, 'R server/internal/store/writer/basic_begin.go '
                                         'server/internal/store/writer/basic_begin_v2.go\n')
            code, out, _ = run(['--changed-files', changes], root)
            self.assertEqual(code, 0)
            line = line_of(plan, '"basic_begin.go"')
            self.assertIn('PINNED: server/internal/store/writer/basic_begin_v2.go was renamed from '
                          'server/internal/store/writer/basic_begin.go, which is hash-pinned by '
                          'server/internal/store/writer/basic_begin_plan_test.go:%d; ' % line
                          + 'add a new file/wrapper or re-record the pin with a "no plan change" statement', out)

    def test_deleted_pinned_file_is_named(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, writer = writer_root(tmp)
            (writer / 'basic_begin_plan_test.go').write_text(PLAN % {'sha': SHA})
            changes = changes_file(root, 'D server/internal/store/writer/basic_begin.go\n')
            code, out, _ = run(['--changed-files', changes], root)
            self.assertEqual(code, 0)
            self.assertIn('PINNED: server/internal/store/writer/basic_begin.go is deleted but is hash-pinned by', out)

    def test_function_pin_names_the_fixture_function(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, writer = writer_root(tmp)
            (writer / 'stage_plan_test.go').write_text(FUNCTIONS)
            changes = changes_file(root, 'M server/internal/store/writer/fixture_b_test.go\n')
            code, out, _ = run(['--changed-files', changes], root)
            self.assertEqual(code, 0)
            self.assertIn('stage_plan_test.go:6 (function fixtureB)', out)
            self.assertNotIn('fixtureA', out)

    def test_callsites_metadata_is_not_a_pin(self):
        text = '''package writer

import "crypto/sha256"

func TestReads(t *testing.T) {
\t_ = []stmt.Definition{{ID: "x", CallSites: []string{"caller_plan_test.go"}}}
\t_ = sha256.Sum256(nil)
}
'''
        with tempfile.TemporaryDirectory() as tmp:
            root, writer = writer_root(tmp)
            (writer / 'reads_plan_test.go').write_text(text)
            changes = changes_file(root, 'M server/internal/store/writer/caller_plan_test.go\n')
            code, out, _ = run(['--changed-files', changes], root)
            self.assertEqual(code, 0)
            self.assertNotIn('PINNED:', out)

    def test_source_lists_and_helper_arguments_are_pins(self):
        text = '''package writer

import "crypto/sha256"

func TestLists(t *testing.T) {
\tfor _, name := range []string{"listed_a.go", "listed_b.go"} {
\t\t_ = name
\t}
\tcaptureEffectiveFixture(t, conn, config, []stmt.Definition{}, "helper_a_test.go", "helper_b_test.go")
\t_ = sha256.Sum256(nil)
}
'''
        with tempfile.TemporaryDirectory() as tmp:
            root, writer = writer_root(tmp)
            (writer / 'lists_plan_test.go').write_text(text)
            changes = changes_file(root, 'M server/internal/store/writer/listed_b.go\n'
                                         'M server/internal/store/writer/helper_a_test.go\n')
            code, out, _ = run(['--changed-files', changes], root)
            self.assertEqual(code, 0)
            self.assertIn('listed_b.go is hash-pinned', out)
            self.assertIn('helper_a_test.go is hash-pinned', out)

    def test_changed_plan_test_pinned_by_another_plan_test(self):
        pinned = '''package writer

import "crypto/sha256"

func TestPinned(t *testing.T) { _ = sha256.Sum256(nil) }
'''
        pinning = '''package writer

import "crypto/sha256"

func TestPinning(t *testing.T) {
\tfor _, name := range []string{"pinned_plan_test.go"} {
\t\t_ = name
\t}
\t_ = sha256.Sum256(nil)
}
'''
        with tempfile.TemporaryDirectory() as tmp:
            root, writer = writer_root(tmp)
            (writer / 'pinned_plan_test.go').write_text(pinned)
            (writer / 'pinning_plan_test.go').write_text(pinning)
            changes = changes_file(root, 'M server/internal/store/writer/pinned_plan_test.go\n')
            code, out, _ = run(['--changed-files', changes], root)
            self.assertEqual(code, 0)
            self.assertIn('pinned_plan_test.go is hash-pinned by '
                          'server/internal/store/writer/pinning_plan_test.go:6', out)

    def test_strict_exits_nonzero_only_with_a_pin(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, writer = writer_root(tmp)
            (writer / 'basic_begin_plan_test.go').write_text(PLAN % {'sha': SHA})
            pinned = changes_file(root, 'M server/internal/store/writer/basic_begin.go\n')
            self.assertEqual(run(['--changed-files', pinned, '--strict'], root)[0], 1)
            self.assertEqual(run(['--changed-files', pinned], root)[0], 0)
            unpinned = changes_file(root, 'M server/internal/store/writer/other.go\n', 'unpinned.txt')
            self.assertEqual(run(['--changed-files', unpinned, '--strict'], root)[0], 0)
            self.assertIn('--strict', run(['--changed-files', pinned, '--strict'], root)[2])

    def test_json_and_summary(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, writer = writer_root(tmp)
            (writer / 'basic_begin_plan_test.go').write_text(PLAN % {'sha': SHA})
            changes = changes_file(root, 'M server/internal/store/writer/basic_begin.go\n')
            code, out, _ = run(['--changed-files', changes, '--json'], root)
            self.assertEqual(code, 0)
            report = json.loads(out)
            self.assertEqual(report['pins'][0]['plan_test'], 'server/internal/store/writer/basic_begin_plan_test.go')
            self.assertEqual(report['pins'][0]['changed'], 'server/internal/store/writer/basic_begin.go')
            summary = root / 'summary.md'
            run(['--changed-files', changes, '--summary', str(summary)], root)
            text = summary.read_text()
            self.assertIn('## Plan hash pins', text)
            self.assertIn('1 changed file(s) touch 1 hash pin(s)', text)
            self.assertIn('PINNED: server/internal/store/writer/basic_begin.go', text)

    def test_summary_defaults_to_the_job_summary_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, writer = writer_root(tmp)
            (writer / 'basic_begin_plan_test.go').write_text(PLAN % {'sha': SHA})
            changes = changes_file(root, 'M server/internal/store/writer/basic_begin.go\n')
            summary = root / 'step-summary.md'
            with patch.dict('os.environ', {'GITHUB_STEP_SUMMARY': str(summary)}):
                run(['--changed-files', changes], root)
            self.assertIn('## Plan hash pins', summary.read_text())
            self.assertIn('PINNED: server/internal/store/writer/basic_begin.go', summary.read_text())

    @unittest.skipUnless(shutil.which('git'), 'git is required')
    def test_git_diff_uses_the_merge_base_and_reports_a_rename(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, writer = writer_root(tmp)
            (writer / 'basic_begin_plan_test.go').write_text(PLAN % {'sha': SHA})
            (writer / 'basic_begin.go').write_text('package writer\n')
            run_git = lambda *args: subprocess.run(['git', *args], cwd=root, check=True,
                                                   capture_output=True, text=True)
            run_git('init', '-b', 'main')
            run_git('config', 'user.email', 'test@example.invalid')
            run_git('config', 'user.name', 'Test')
            run_git('add', '-A')
            run_git('commit', '-m', 'base')
            base = run_git('rev-parse', 'HEAD').stdout.strip()
            code, out, _ = run(['--base', base, '--head', 'HEAD'], root)
            self.assertEqual(code, 0)
            self.assertNotIn('PINNED:', out)
            run_git('mv', 'server/internal/store/writer/basic_begin.go',
                    'server/internal/store/writer/basic_begin_v2.go')
            run_git('commit', '-m', 'rename')
            code, out, _ = run(['--base', base, '--head', 'HEAD'], root)
            self.assertEqual(code, 0)
            self.assertIn('basic_begin_v2.go was renamed from server/internal/store/writer/basic_begin.go', out)


if __name__ == '__main__':
    unittest.main()
