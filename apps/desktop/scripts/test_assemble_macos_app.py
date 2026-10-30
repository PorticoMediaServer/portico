import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import plistlib
import shutil
import struct
import tempfile
import unittest
import zipfile


spec = importlib.util.spec_from_file_location(
    "assemble_macos_app", Path(__file__).with_name("assemble-macos-app.py"))
assembler = importlib.util.module_from_spec(spec)
spec.loader.exec_module(assembler)

ARM64 = 0x0100000C
X86_64 = 0x01000007
MH_EXECUTE = 2
MH_DYLIB = 6


def sha(data):
    return hashlib.sha256(data).hexdigest()


def macho(cputype=ARM64, filetype=MH_EXECUTE, payload=b""):
    return (b"\xcf\xfa\xed\xfe"
            + struct.pack("<IIIIIII", cputype, 0, filetype, 0, 0, 0x200085, 0)
            + payload)


class Fixture:
    def __init__(self, root):
        self.root = root
        self.binary = root / "portico-desktop-macos-arm64"
        self.web = root / "web"
        self.web_files = {
            "index.html": b"<!doctype html><link rel=stylesheet href=\"/assets/main.css\">",
            "assets/main.css": b"body{color:#fff}",
            "assets/main.js": b"console.log('portico')",
        }
        for name, data in self.web_files.items():
            path = self.web / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        self.binary.write_bytes(macho(payload=b"".join(
            b"/" + name.encode() + b"\0" for name in self.web_files)))
        self.stage = root / "native-stage"
        self.stage.mkdir()
        self.dylibs = {
            "libmpv.2.dylib": macho(filetype=MH_DYLIB, payload=b"mpv"),
            "libavcodec.62.dylib": macho(filetype=MH_DYLIB, payload=b"codec"),
        }
        records = []
        for name, data in self.dylibs.items():
            (self.stage / name).write_bytes(data)
            records.append({"name": name, "inputSha256": sha(data), "sha256": sha(data),
                            "bytes": len(data), "dependencies": []})
        self.patches = [{"path": "p%d.patch" % index, "sha256": str(index) * 64}
                        for index in range(3)]
        self.lock = {
            "schemaVersion": 2,
            "ffmpeg": {"sourceCommit": "c" * 40, "sourceSha256": "d" * 64,
                       "preparedReceiptSha256": "e" * 64, "requiredPatches": 3,
                       "sharedAbi": assembler.ABI,
                       "postSeriesPatches": [{"path": "post.patch", "sha256": "f" * 64}]},
            "packageLimits": {"installedBytes": 805306368},
        }
        self.manifest = {
            "status": "development-only; unqualified for release",
            "sourceReceiptSha256": "e" * 64,
            "sourceReceipt": {"sourceCommit": "c" * 40, "sourceSha256": "d" * 64,
                              "patches": self.patches,
                              "postSeriesPatches": self.lock["ffmpeg"]["postSeriesPatches"]},
            "ffmpegAbi": assembler.ABI,
            "localSignature": "ad-hoc; no Developer ID or notarization",
            "installedBytes": sum(len(data) for data in self.dylibs.values()),
            "files": records,
        }
        self.write_manifest()
        self.lock_path = root / "native-deps.lock.json"
        self.write_json(self.lock_path, self.lock)
        self.config = root / "tauri.conf.json"
        self.write_json(self.config, {
            "productName": "Portico", "version": "0.1.0",
            "identifier": "tv.getportico.desktop.spike",
            "bundle": {"category": "Entertainment", "copyright": "Portico contributors"}})
        self.output = root / "Portico.app"

    @staticmethod
    def write_json(path, value):
        path.write_text(json.dumps(value, indent=2) + "\n")

    def write_manifest(self):
        self.write_json(self.stage / assembler.MANIFEST, self.manifest)

    def replace_dylib(self, name, data):
        (self.stage / name).write_bytes(data)
        record = next(item for item in self.manifest["files"] if item["name"] == name)
        record.update(inputSha256=sha(data), sha256=sha(data), bytes=len(data))
        self.manifest["installedBytes"] = sum(item["bytes"] for item in self.manifest["files"])
        self.write_manifest()

    def run(self, **overrides):
        arguments = {"binary": self.binary, "web_root": self.web, "native_stage": self.stage,
                     "lock_path": self.lock_path, "tauri_config": self.config,
                     "output": self.output}
        arguments.update(overrides)
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            assembler.assemble(**arguments)
        return json.loads(output.getvalue())


class AssemblyTests(unittest.TestCase):
    def test_directory_web_root_assembles_the_shell_layout(self):
        with tempfile.TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            summary = fixture.run()
            contents = fixture.output / "Contents"
            executable = contents / "MacOS" / "Portico"
            self.assertEqual(executable.read_bytes(), fixture.binary.read_bytes())
            self.assertEqual(executable.stat().st_mode & 0o111, 0o111)
            self.assertEqual((contents / "PkgInfo").read_bytes(), b"APPL????")
            info = plistlib.loads((contents / "Info.plist").read_bytes())
            self.assertEqual(info["CFBundleExecutable"], "Portico")
            self.assertEqual(info["CFBundleName"], "Portico")
            self.assertEqual(info["CFBundleDisplayName"], "Portico")
            self.assertEqual(info["CFBundleIdentifier"], "tv.getportico.desktop.spike")
            self.assertEqual(info["CFBundleShortVersionString"], "0.1.0")
            self.assertEqual(info["CFBundleVersion"], "0.1.0")
            self.assertEqual(info["CFBundlePackageType"], "APPL")
            self.assertNotIn("LSMinimumSystemVersion", info)
            self.assertEqual(info["LSApplicationCategoryType"], "public.app-category.entertainment")
            self.assertEqual(info["NSHumanReadableCopyright"], "Portico contributors")
            self.assertIs(info["NSHighResolutionCapable"], True)
            native = contents / "Resources" / "native"
            for name, data in fixture.dylibs.items():
                self.assertEqual((native / name).read_bytes(), data)
                self.assertEqual((native / name).stat().st_mode & 0o111, 0o111)
            self.assertEqual((native / assembler.MANIFEST).read_bytes(),
                             (fixture.stage / assembler.MANIFEST).read_bytes())
            self.assertFalse((contents / "Resources" / "web").exists())
            self.assertEqual((contents / "Resources" / "assembly-receipt.json").is_file(), True)
            receipt = json.loads((contents / "Resources" / "assembly-receipt.json").read_text())
            self.assertEqual(receipt["status"],
                             "development-only; unsigned/ad-hoc; unqualified for release")
            self.assertEqual(receipt["inputs"]["binary"]["sha256"], sha(fixture.binary.read_bytes()))
            self.assertEqual(receipt["inputs"]["dependencyLock"]["sha256"],
                             sha(fixture.lock_path.read_bytes()))
            self.assertEqual(receipt["inputs"]["nativeManifest"]["sha256"],
                             sha((fixture.stage / assembler.MANIFEST).read_bytes()))
            self.assertEqual(receipt["inputs"]["nativeFiles"], 2)
            self.assertEqual(receipt["inputs"]["webRoot"]["kind"], "directory")
            self.assertEqual(receipt["inputs"]["webRoot"]["files"], 3)
            self.assertEqual(receipt["inputs"]["webRoot"]["correspondence"],
                             "asset-key presence only; embedded bytes are not compared")
            self.assertNotIn("minimumSystemVersion", receipt)
            self.assertEqual(summary["nativeFiles"], 2)
            self.assertEqual(summary["webFiles"], 3)

    def test_minimum_system_version_is_written_only_when_configured(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            configured = json.loads(fixture.config.read_text())
            configured["bundle"]["macOS"] = {"minimumSystemVersion": "14.4"}
            fixture.write_json(fixture.config, configured)
            fixture.run()
            contents = fixture.output / "Contents"
            info = plistlib.loads((contents / "Info.plist").read_bytes())
            receipt = json.loads((contents / "Resources/assembly-receipt.json").read_text())
            self.assertEqual(info["LSMinimumSystemVersion"], "14.4")
            self.assertEqual(receipt["minimumSystemVersion"], "14.4")
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            fixture.run()
            contents = fixture.output / "Contents"
            info = plistlib.loads((contents / "Info.plist").read_bytes())
            receipt = json.loads((contents / "Resources/assembly-receipt.json").read_text())
            self.assertNotIn("LSMinimumSystemVersion", info)
            self.assertNotIn("minimumSystemVersion", receipt)

    def test_zip_web_root_corresponds_to_the_same_entries_without_extraction(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            fixture.run()
            directory_receipt = json.loads(
                (fixture.output / "Contents/Resources/assembly-receipt.json").read_text())
            archive = root / "desktop.web.zip"
            with zipfile.ZipFile(archive, "w") as stream:
                for name, data in fixture.web_files.items():
                    stream.writestr(name, data)
            archive_digest = sha(archive.read_bytes())
            fixture.run(web_root=archive, output=root / "Portico-zip.app")
            zipped = root / "Portico-zip.app"
            receipt = json.loads(
                (zipped / "Contents/Resources/assembly-receipt.json").read_text())
            self.assertEqual(archive_digest, sha(archive.read_bytes()))
            self.assertEqual(receipt["inputs"]["webRoot"]["kind"], "zip")
            self.assertEqual(receipt["inputs"]["webRoot"]["files"], 3)
            self.assertEqual(receipt["inputs"]["webRoot"]["sha256"],
                             directory_receipt["inputs"]["webRoot"]["sha256"])
            self.assertFalse((zipped / "Contents/Resources/web").exists())

    def test_native_manifest_bytes_and_source_cohort_are_enforced(self):
        def corrupt_bytes(fixture):
            (fixture.stage / "libmpv.2.dylib").write_bytes(b"changed bytes")

        def corrupt_hash(fixture):
            fixture.manifest["files"][0]["sha256"] = "0" * 64
            fixture.write_manifest()

        def wrong_abi(fixture):
            fixture.manifest["ffmpegAbi"] = dict(assembler.ABI, avcodec=61)
            fixture.write_manifest()

        def wrong_source(fixture):
            fixture.manifest["sourceReceipt"]["sourceCommit"] = "9" * 40
            fixture.write_manifest()

        def wrong_receipt(fixture):
            fixture.manifest["sourceReceiptSha256"] = "9" * 64
            fixture.write_manifest()

        def missing_patch(fixture):
            fixture.manifest["sourceReceipt"]["patches"] = fixture.patches[:-1]
            fixture.write_manifest()

        def wrong_series(fixture):
            fixture.manifest["sourceReceipt"]["postSeriesPatches"] = []
            fixture.write_manifest()

        def oversized(fixture):
            fixture.manifest["installedBytes"] = (
                fixture.lock["packageLimits"]["installedBytes"] + 1)
            fixture.write_manifest()

        mutations = [corrupt_bytes, corrupt_hash, wrong_abi, wrong_source, wrong_receipt,
                     missing_patch, wrong_series, oversized]
        for mutation in mutations:
            with self.subTest(mutation=mutation.__name__), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                fixture = Fixture(root)
                mutation(fixture)
                with self.assertRaises(ValueError):
                    fixture.run()
                self.assertFalse(fixture.output.exists())
                self.assertEqual(list(root.glob("*.partial-*")), [])

    def test_missing_inputs_and_output_rules_are_refused(self):
        def missing_binary(fixture):
            fixture.binary.unlink()

        def missing_lock(fixture):
            fixture.lock_path.unlink()

        def missing_config(fixture):
            fixture.config.unlink()

        def missing_manifest(fixture):
            (fixture.stage / assembler.MANIFEST).unlink()

        def missing_dylib(fixture):
            (fixture.stage / "libavcodec.62.dylib").unlink()

        def extra_stage_file(fixture):
            (fixture.stage / "libextra.dylib").write_bytes(macho(filetype=MH_DYLIB))

        def missing_web(fixture):
            shutil.rmtree(fixture.web)

        def missing_index(fixture):
            (fixture.web / "index.html").unlink()

        mutations = [missing_binary, missing_lock, missing_config, missing_manifest,
                     missing_dylib, extra_stage_file, missing_web, missing_index]
        for mutation in mutations:
            with self.subTest(mutation=mutation.__name__), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                fixture = Fixture(root)
                mutation(fixture)
                with self.assertRaises(ValueError):
                    fixture.run()
                self.assertFalse(fixture.output.exists())
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            fixture.output.mkdir()
            (fixture.output / "keep").write_text("original")
            with self.assertRaises(ValueError):
                fixture.run()
            self.assertEqual((fixture.output / "keep").read_text(), "original")
            with self.assertRaises(ValueError):
                fixture.run(output=root / "Portico")
            with self.assertRaises(ValueError):
                fixture.run(output=root / "Portico.app.other")
            with self.assertRaises(ValueError):
                fixture.run(output=fixture.stage / "Portico.app")
            self.assertEqual(sorted(entry.name for entry in fixture.stage.iterdir()),
                             sorted([*fixture.dylibs, assembler.MANIFEST]))

    def test_non_arm64_and_non_macho_inputs_are_refused(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            fixture.binary.write_bytes(macho(X86_64, MH_EXECUTE,
                                             b"/index.html\0/assets/main.css\0/assets/main.js\0"))
            with self.assertRaises(ValueError):
                fixture.run()
            self.assertFalse(fixture.output.exists())
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            fixture.binary.write_bytes(b"\xca\xfe\xba\xbe" + b"\0" * 28 + b"".join(
                b"/" + name.encode() + b"\0" for name in fixture.web_files))
            with self.assertRaises(ValueError):
                fixture.run()
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            fixture.replace_dylib("libmpv.2.dylib", b"not a mach-o file" + b"\0" * 32)
            with self.assertRaises(ValueError):
                fixture.run()
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            fixture.replace_dylib("libmpv.2.dylib", macho(filetype=MH_EXECUTE, payload=b"wrong type"))
            with self.assertRaises(ValueError):
                fixture.run()

    def test_web_root_must_correspond_to_the_embedded_frontend(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            fixture.binary.write_bytes(macho(payload=b"/index.html\0/assets/main.css\0"))
            with self.assertRaises(ValueError):
                fixture.run()
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            os.symlink(fixture.web / "index.html", fixture.web / "alias.html")
            with self.assertRaises(ValueError):
                fixture.run()
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            archive = root / "desktop.web.zip"
            with zipfile.ZipFile(archive, "w") as stream:
                for name, data in fixture.web_files.items():
                    stream.writestr(name, data)
                stream.writestr("../escape.js", b"escape")
            with self.assertRaises(ValueError):
                fixture.run(web_root=archive)

    def test_successful_assembly_never_mutates_an_input(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            before = {str(path): (path.stat().st_mode, sha(path.read_bytes()))
                      for path in sorted(root.rglob("*"))
                      if path.is_file() and fixture.output not in path.parents}
            fixture.run()
            after = {str(path): (path.stat().st_mode, sha(path.read_bytes()))
                     for path in sorted(root.rglob("*"))
                     if path.is_file() and fixture.output not in path.parents}
            self.assertEqual(before, after)

    def test_bundle_metadata_is_deterministic(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            fixture.run()
            first = ((fixture.output / "Contents/Info.plist").read_bytes(),
                     (fixture.output / "Contents/Resources/assembly-receipt.json").read_bytes())
            fixture.run(output=root / "Second.app")
            second = ((root / "Second.app/Contents/Info.plist").read_bytes(),
                      (root / "Second.app/Contents/Resources/assembly-receipt.json").read_bytes())
            self.assertEqual(first, second)

    def test_ad_hoc_signing_seals_the_scratch_bundle_only(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            tool = root / "codesign"
            tool.write_text("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$0.args\"\n")
            tool.chmod(0o755)
            fixture.run(ad_hoc_sign_bundle=True, codesign=str(tool))
            calls = (root / "codesign.args").read_text().splitlines()
            partial = str(fixture.output) + ".partial-" + str(os.getpid())
            force = calls.index("--force")
            verify = calls.index("--verify")
            self.assertEqual(calls[force:force + 5],
                             ["--force", "--sign", "-", "--timestamp=none", partial])
            self.assertEqual(calls[verify:verify + 3], ["--verify", "--strict", partial])
            self.assertTrue(fixture.output.is_dir())
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = Fixture(root)
            tool = root / "codesign"
            tool.write_text("#!/bin/sh\necho 'no identity' >&2\nexit 1\n")
            tool.chmod(0o755)
            with self.assertRaises(ValueError):
                fixture.run(ad_hoc_sign_bundle=True, codesign=str(tool))
            self.assertFalse(fixture.output.exists())
            self.assertEqual(list(root.glob("*.partial-*")), [])


if __name__ == "__main__":
    unittest.main()
