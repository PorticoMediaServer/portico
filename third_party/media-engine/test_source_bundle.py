"""Corresponding-source requirements remain specific to the recipe being verified."""
import importlib.util
import io
import json
import pathlib
import tarfile
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("media_engine_verify", HERE / "verify.py")
verify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify)
LOCK = json.loads((HERE / "sources.lock.json").read_text())
SOURCES = {entry["file"] for entry in LOCK["components"].values() if "android" in entry["platforms"] and not entry.get("prebuilt")}
SOURCES.update(sub["file"] for entry in LOCK["components"].values() if "android" in entry["platforms"] for sub in entry.get("subsources", {}).values())
PATCHES = {path.name for path in (HERE / "patches").rglob("*.patch")}
RECIPE = {"libdovi-vendored-crates.tar", "sources.lock.json", "build-android.sh", "common.sh"}


class SourceBundleTest(unittest.TestCase):
    def check_bundle(self, members, notices=True):
        with tempfile.TemporaryDirectory(prefix="source-bundle-") as directory:
            out = pathlib.Path(directory)
            source = out / "android/source"
            source.mkdir(parents=True)
            with tarfile.open(source / f"portico-media-engine-android-source-r{LOCK['revision']}.tar", "w") as archive:
                for name in sorted(members):
                    body = b"test source member\n"
                    entry = tarfile.TarInfo(f"source/{name}")
                    entry.size = len(body)
                    archive.addfile(entry, io.BytesIO(body))
            if notices:
                folder = out / "android/notices"
                folder.mkdir()
                (folder / "THIRD-PARTY-NOTICES.txt").write_text("\n".join(f"{name} {entry['version']}" for name, entry in LOCK["components"].items() if "android" in entry["platforms"]))
            report = verify.Report()
            verify.check_notices(report, out, "android")
            return report

    def test_android_corresponding_source_uses_its_own_recipe_without_an_apple_build(self):
        members = SOURCES | PATCHES | RECIPE
        self.assertNotIn("build-apple.sh", members)
        self.assertEqual([], self.check_bundle(members).failures)

    def test_every_android_recipe_input_is_still_required_even_if_apple_script_is_present(self):
        for required in RECIPE:
            with self.subTest(required=required):
                report = self.check_bundle((SOURCES | PATCHES | RECIPE | {"build-apple.sh"}) - {required})
                self.assertIn(f"android: the source bundle lacks {required}", report.failures)

    def test_every_selected_component_and_subsource_archive_is_still_required(self):
        for required in SOURCES:
            with self.subTest(required=required):
                report = self.check_bundle((SOURCES | PATCHES | RECIPE) - {required})
                self.assertIn(f"android: the source bundle lacks {required}", report.failures)

    def test_every_checked_patch_is_still_required(self):
        for patch in PATCHES:
            with self.subTest(patch=patch):
                report = self.check_bundle((SOURCES | PATCHES | RECIPE) - {patch})
                self.assertIn(f"android: the source bundle lacks patch {patch}", report.failures)

    def test_missing_notices_still_fail(self):
        report = self.check_bundle(SOURCES | PATCHES | RECIPE, notices=False)
        self.assertTrue(any("is missing (run collect-notices.sh)" in failure for failure in report.failures))


if __name__ == "__main__":
    unittest.main()
