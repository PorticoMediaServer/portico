import hashlib
import importlib.util
import io
import json
import pathlib
import tempfile
import unittest
import urllib.error
import zipfile

spec = importlib.util.spec_from_file_location("fetch_locked", pathlib.Path(__file__).with_name("fetch_locked_rclone.py"))
fetch_locked = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fetch_locked)


class Acquisition(unittest.TestCase):
    def fixture(self, directory):
        program = b"controlled-tool-fixture"
        archive = io.BytesIO()
        with zipfile.ZipFile(archive, "w") as package:
            package.writestr("locked/rclone", program)
        data = archive.getvalue()
        lock = pathlib.Path(directory) / "lock.json"
        lock.write_text(json.dumps({"targets": {"linux-amd64": {"url": "https://fixture.invalid/locked.zip", "sha256": hashlib.sha256(data).hexdigest(), "archiveRoot": "locked", "binary": "rclone", "files": {"rclone": hashlib.sha256(program).hexdigest()}}}}))
        return lock, data, program

    def test_exact_artifact_and_only_403_official_fallback(self):
        with tempfile.TemporaryDirectory() as root:
            lock, data, program = self.fixture(root)
            calls = []

            def open_url(request, timeout):
                calls.append(request.full_url)
                self.assertLessEqual(timeout, 30)
                if len(calls) == 1:
                    raise urllib.error.HTTPError(request.full_url, 403, "fixture", {}, None)
                return io.BytesIO(data)

            target = fetch_locked.fetch(lock, root, open_url=open_url)
            self.assertEqual(target.read_bytes(), program)
            self.assertEqual(len(calls), 2)
            self.assertTrue(calls[1].startswith("https://github.com/rclone/rclone/releases/download/v1.75.2/"))
            self.assertFalse((pathlib.Path(root) / "rclone.zip").exists())

    def test_trickling_progress_cannot_reset_acquisition_deadline(self):
        with tempfile.TemporaryDirectory() as root:
            lock, _, _ = self.fixture(root)
            now = [0]

            class Trickle(io.BytesIO):
                def read1(self, size):
                    now[0] += 61
                    return b"x"

            with self.assertRaisesRegex(RuntimeError, "exceeded 120 seconds"):
                fetch_locked.fetch(lock, root, open_url=lambda *_args, **_kwargs: Trickle(), clock=lambda: now[0])
            self.assertFalse((pathlib.Path(root) / "rclone.zip").exists())
            self.assertFalse((pathlib.Path(root) / "rclone").exists())

    def test_non403_failure_and_wrong_bytes_never_install_or_reroute(self):
        with tempfile.TemporaryDirectory() as root:
            lock, _, _ = self.fixture(root)
            calls = []

            def denied(request, **_kwargs):
                calls.append(request.full_url)
                raise urllib.error.HTTPError(request.full_url, 500, "fixture", {}, None)

            with self.assertRaises(urllib.error.HTTPError):
                fetch_locked.fetch(lock, root, open_url=denied)
            self.assertEqual(len(calls), 1)
            with self.assertRaisesRegex(RuntimeError, "archive digest mismatch"):
                fetch_locked.fetch(lock, root, open_url=lambda *_args, **_kwargs: io.BytesIO(b"changed"))
            self.assertFalse((pathlib.Path(root) / "rclone").exists())

    def test_preexisting_archive_is_never_overwritten_or_cleaned(self):
        with tempfile.TemporaryDirectory() as root:
            lock, data, _ = self.fixture(root)
            original = pathlib.Path(root) / "rclone.zip"
            original.write_bytes(b"retained-original")
            with self.assertRaises(FileExistsError):
                fetch_locked.fetch(lock, root, open_url=lambda *_args, **_kwargs: io.BytesIO(data))
            self.assertEqual(original.read_bytes(), b"retained-original")


if __name__ == "__main__":
    unittest.main()
