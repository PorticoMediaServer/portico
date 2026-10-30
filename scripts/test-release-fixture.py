#!/usr/bin/env python3
"""Exercise the packaging fixtures through the unchanged release verifier."""
import pathlib
import subprocess
import sys
import tempfile
import unittest

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from release_test_fixture import ReleaseFixture


class OfflineReleaseFixtureTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = pathlib.Path(temporary.name)
        self.fixture = ReleaseFixture(self.root)

    def payload(self, target):
        directory = self.root / target
        self.fixture.rclone(directory / "third_party/rclone", target)
        ffmpeg = directory / "third_party/ffmpeg"
        ffmpeg.mkdir()
        (ffmpeg / "toolchain-manifest.json").write_text("fixture media manifest\n")
        binary = "portico-server.exe" if target.startswith("windows-") else "portico-server"
        (directory / binary).write_bytes(b"disposable server payload")
        self.fixture.sign(directory, target)
        return directory

    def verify(self, directory, target):
        return subprocess.run([
            sys.executable, str(self.fixture.repo / "scripts/rclone-bundle.py"),
            "verify-release", str(directory), "--target", target,
            "--public-key", str(self.fixture.publicfile),
        ], capture_output=True, text=True, timeout=30)

    def test_signed_fixtures_satisfy_the_real_verifier_for_all_packaging_targets(self):
        for target in self.fixture.lock["targets"]:
            with self.subTest(target=target):
                result = self.verify(self.payload(target), target)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_wrong_public_key_is_rejected(self):
        directory = self.payload("linux-amd64")
        self.fixture.publicfile.write_bytes(Ed25519PrivateKey.generate().public_key().public_bytes_raw())
        result = self.verify(directory, "linux-amd64")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("signature does not match the pinned key", result.stderr)

    def test_modified_server_is_rejected(self):
        directory = self.payload("linux-amd64")
        (directory / "portico-server").write_bytes(b"modified")
        result = self.verify(directory, "linux-amd64")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Release server artifact changed", result.stderr)

    def test_modified_rclone_payload_is_rejected(self):
        directory = self.payload("linux-amd64")
        (directory / "third_party/rclone/rclone").write_bytes(b"modified")
        result = self.verify(directory, "linux-amd64")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Missing or modified rclone payload", result.stderr)

    def test_modified_contract_is_rejected(self):
        directory = self.payload("linux-amd64")
        (self.fixture.contract / "DEPENDENCY-NOTICES.txt").write_text("modified pinned inventory")
        result = self.verify(directory, "linux-amd64")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("does not match the pinned release", result.stderr)


if __name__ == "__main__":
    unittest.main()
