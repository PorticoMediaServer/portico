import importlib.util
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("native_pair", ROOT / "native_plan_pair.py")
pair = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pair)


class NativePlanPairTest(unittest.TestCase):
    def test_overlay_preserves_source_and_every_gate(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source, output = root / "source", root / "output"
            originals = {}
            for package, (name, files, _) in pair.TARGETS.items():
                for filename in files:
                    path = source / pair.STORE / package / filename
                    path.parent.mkdir(parents=True, exist_ok=True)
                    data = "package " + name + "\n// fixture bytes remain unchanged\nplan.Capture(ctx, conn, registry, fixture, policy)\n"
                    if filename == "canonical_test.go":
                        data += "plan.ForeignKeys(ctx, conn, policy)\n\tcheckFKGolden(t, fk)\n"
                    path.write_text(data)
                    originals[path] = path.read_bytes()
            path, changes = pair.overlay(source, output, (ROOT / "native_plan_pair_test.go.tmpl").read_text())
            self.assertTrue(path.exists())
            self.assertEqual(len(changes), len(originals))
            for source_file, data in originals.items():
                self.assertEqual(source_file.read_bytes(), data)
            canonical = (output / "overlay/stmt/plan/canonical_test.go").read_text()
            self.assertIn("nativePairCapture(t, ctx", canonical)
            self.assertIn("nativePairForeignKeys(t, ctx", canonical)
            self.assertIn("t.Cleanup(func() { checkFKGolden(t, fk) })", canonical)

    def test_exact_sha_and_external_output_are_required(self):
        with self.assertRaisesRegex(ValueError, "40-character"):
            pair.check_locations(Path("/source"), Path("/output"), "main")
        with self.assertRaisesRegex(ValueError, "outside"):
            pair.check_locations(Path("/source"), Path("/source/output"), "a" * 40)

    def test_missing_capture_anchor_fails_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = root / "source" / pair.STORE / "stmt/plan/canonical_test.go"
            path.parent.mkdir(parents=True)
            path.write_text("package plan_test\n")
            with self.assertRaisesRegex(ValueError, "canonical FK check changed"):
                pair.overlay(root / "source", root / "output", "package PACKAGE\n")

    def test_failed_test_process_keeps_nonzero_status(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source, output = root / "source", root / "output"
            sha = "a" * 40
            def fake_git(_, *args):
                if args == ("rev-parse", "HEAD"):
                    return sha
                if args == ("rev-parse", "HEAD^{tree}"):
                    return "tree"
                return ""
            with patch.object(pair, "git", side_effect=fake_git), \
                 patch.object(pair, "overlay", return_value=(output / "overlay.json", [])), \
                 patch.object(pair, "TARGETS", {"read": ("read", [], ["TestBrowsePlans"])}), \
                 patch.object(pair.subprocess, "check_output", return_value="fixture\n"), \
                 patch.object(pair.subprocess, "run", return_value=SimpleNamespace(returncode=1)):
                self.assertEqual(pair.collect(source, output, sha), 1)


if __name__ == "__main__":
    unittest.main()
