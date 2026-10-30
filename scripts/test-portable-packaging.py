#!/usr/bin/env python3
import os
import pathlib
import shutil
import subprocess
import tarfile
import tempfile
import unittest
import zipfile

from release_test_fixture import PORTABLE_INPUTS, ReleaseFixture


class PortablePackagingTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.fixture = ReleaseFixture(self.root)
        self.fixture.copy_inputs(PORTABLE_INPUTS)
        self.stage = self.root / "stage"
        self.web = self.root / "web"
        self.out = self.root / "out"
        tools = self.root / "tools"
        (self.stage / "third_party/ffmpeg").mkdir(parents=True)
        self.web.mkdir()
        tools.mkdir()
        (self.stage / "third_party/ffmpeg/toolchain-manifest.json").write_text('{"fixture":"portable packaging input"}\n')
        (self.web / "index.html").write_text("fixture web")
        binary = self.stage / "portico-server"
        binary.write_text('#!/bin/sh\nprintf "%s\\n" "$PORTICO_WEB_DIR" "$PORTICO_DATA_DIR" "$1"\n')
        binary.chmod(0o700)
        (self.stage / "portico-server.exe").write_text("fixture executable")
        for name in ["nfpm", "makensis"]:
            tool = tools / name
            if name == "makensis":
                tool.write_text("#!/usr/bin/env python3\nimport pathlib,sys\nstage=pathlib.Path(next(a.split('=',1)[1] for a in sys.argv if a.startswith('-DSTAGE_DIR=')))\nassert not (stage/'README.txt').exists(), 'portable README reached installer'\nassert not (stage/'start-portico.cmd').exists(), 'portable launcher reached installer'\n")
            else:
                tool.write_text("#!/bin/sh\nexit 0\n")
            tool.chmod(0o700)
        self.env = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ["PATH"],
                        PORTICO_RELEASE_PUBLIC_KEY_FILE=str(self.fixture.publicfile))
        self.env.pop("PORTICO_WEB_DIR", None)
        self.env.pop("PORTICO_DATA_DIR", None)

    def seal(self, platform):
        target = platform + "-amd64"
        self.fixture.rclone(self.stage / "third_party/rclone", target)
        self.fixture.sign(self.stage, target)

    def package(self, platform):
        return subprocess.run(["bash", str(self.fixture.repo / "scripts/package-release.sh"), platform, "amd64",
                               "0.1.0", str(self.stage), str(self.web), str(self.out), "1"],
                              env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

    def test_linux_archive_launches_from_unrelated_working_directory(self):
        self.seal("linux")
        result = self.package("linux")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        unpacked = self.root / "unpacked media server"
        unpacked.mkdir()
        with tarfile.open(self.out / "portico-media-server-linux-x64.tar.gz") as archive:
            archive.extractall(unpacked, filter="data")
        launcher = unpacked / "start-portico.sh"
        self.assertTrue(launcher.is_file(), "archive must supply a configured launcher")
        output = subprocess.check_output([str(launcher), "argument"], cwd=self.root,
                                         env=self.env, text=True).splitlines()
        self.assertEqual(output, [str(unpacked / "web"), "", "argument"])
        override = dict(self.env, PORTICO_DATA_DIR=str(self.root / "owner-state"),
                        PORTICO_WEB_DIR=str(self.root / "owner-web"))
        output = subprocess.check_output([str(launcher), "argument"], cwd="/tmp",
                                         env=override, text=True).splitlines()
        self.assertEqual(output, [override["PORTICO_WEB_DIR"], override["PORTICO_DATA_DIR"], "argument"])
        self.assertTrue((unpacked / "README.txt").is_file())

    @unittest.skipUnless(shutil.which("zip"), "zip is unavailable")
    def test_windows_archive_supplies_launcher_and_instructions(self):
        self.seal("windows")
        result = self.package("windows")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        with zipfile.ZipFile(self.out / "Portico-Media-Server-Windows-x64-Portable.zip") as archive:
            self.assertIn("start-portico.cmd", archive.namelist())
            self.assertIn("README.txt", archive.namelist())

    def test_modified_server_is_rejected_before_archive_creation(self):
        self.seal("linux")
        with (self.stage / "portico-server").open("ab") as stream:
            stream.write(b"tampered")
        result = self.package("linux")
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Release server artifact changed", result.stderr)
        self.assertFalse((self.out / "portico-media-server-linux-x64.tar.gz").exists())

    def test_modified_rclone_is_rejected_before_archive_creation(self):
        self.seal("linux")
        with (self.stage / "third_party/rclone/rclone").open("ab") as stream:
            stream.write(b"tampered")
        result = self.package("linux")
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Missing or modified rclone payload", result.stderr)
        self.assertFalse((self.out / "portico-media-server-linux-x64.tar.gz").exists())


if __name__ == "__main__":
    unittest.main()
