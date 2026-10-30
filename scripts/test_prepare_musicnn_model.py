import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest
import zipfile


spec = importlib.util.spec_from_file_location("prepare_musicnn", Path(__file__).with_name("prepare-musicnn-model.py"))
model = importlib.util.module_from_spec(spec)
spec.loader.exec_module(model)


class OriginalModelPreparationTests(unittest.TestCase):
    def fixture(self, root, licence="ISC", wrong_model=False):
        wheel = root / "fixture.whl"
        legal = (model.ROOT / "third_party/musicnn/LICENSE.md").read_bytes()
        # Deliberately synthetic bytes test the staging boundary, not inference.
        weights = b"synthetic boundary fixture, not a neural model"
        path = "musicnn/MTT_musicnn/.data-00000-of-00001"
        with zipfile.ZipFile(wheel, "w") as archive:
            archive.writestr("metadata", "Name: musicnn\nVersion: 0.1.0\nLicense: " + licence + "\n")
            archive.writestr("licence", legal)
            archive.writestr(path, weights)
        lock = {"modelId": "fixture", "modelVersion": "fixture", "sourceModel": "MTT_musicnn", "license": "ISC",
                "licenseSHA256": hashlib.sha256(legal).hexdigest(),
                "distribution": {"bytes": wheel.stat().st_size, "sha256": model.digest(wheel),
                                 "metadata": "metadata", "licenseFile": "licence"},
                "files": [{"path": path, "bytes": len(weights),
                           "sha256": "0" * 64 if wrong_model else hashlib.sha256(weights).hexdigest()}]}
        return wheel, lock

    def test_every_original_file_and_licence_verified_before_publish(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            wheel, lock = self.fixture(root)
            output = root / "model"
            model.prepare(wheel, output, lock)
            self.assertEqual((output / ".data-00000-of-00001").read_bytes(), b"synthetic boundary fixture, not a neural model")
            self.assertTrue((output / "LICENSE.md").is_file())

    def test_unlicensed_or_changed_model_never_publishes(self):
        for licence, wrong in [("CC-BY-NC", False), ("ISC", True)]:
            with self.subTest(licence=licence, wrong=wrong), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                wheel, lock = self.fixture(root, licence, wrong)
                with self.assertRaises(ValueError):
                    model.prepare(wheel, root / "model", lock)
                self.assertFalse((root / "model").exists())

    def test_changed_publisher_archive_and_existing_output_are_preserved(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            wheel, lock = self.fixture(root)
            lock["distribution"]["sha256"] = "0" * 64
            with self.assertRaises(ValueError):
                model.prepare(wheel, root / "model", lock)
            wheel, lock = self.fixture(root)
            output = root / "model"
            output.mkdir()
            (output / "keep").write_text("existing model")
            with self.assertRaises(ValueError):
                model.prepare(wheel, output, lock)
            self.assertEqual((output / "keep").read_text(), "existing model")


if __name__ == "__main__":
    unittest.main()
