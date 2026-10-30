"""Source-cohort admission only; no Mach-O, shared ABI or target qualification."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("native_stage", ROOT / "scripts/stage-native-macos.py")
stage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(stage)


class SourceContract(unittest.TestCase):
    def setUp(self):
        self.lock = json.loads((ROOT / "native-deps.lock.json").read_text())
        source = self.lock["ffmpeg"]
        self.receipt = {"sourceCommit": source["sourceCommit"], "sourceSha256": source["sourceSha256"],
                        "patches": [{"sha256": "1" * 64} for _ in range(100)],
                        "postSeriesPatches": copy.deepcopy(source["postSeriesPatches"])}

    def test_exact_main_102_cohort_and_shared_abi_stay_distinct_from_process_artifacts(self):
        source = self.lock["ffmpeg"]
        self.assertEqual(source["contractCommit"], "f3ef43fafe8e929f9b5c0822b87c09e9debe4f65")
        self.assertEqual(source["requiredPatches"], 100)
        self.assertEqual(len(source["postSeriesPatches"]), 2)
        self.assertEqual(source["preparedReceiptSha256"], "a47f4c8e3dadc47e0fd564d47f7b4c3f2a9b1f7680c4765f0750e562efb6a71f")
        self.assertEqual(source["sharedAbi"], stage.ABI)
        self.assertFalse(self.lock["releaseReady"])
        stage.verify_source_receipt(self.receipt, self.lock, source["preparedReceiptSha256"])

    def test_old_100_and_newer_103_receipts_cannot_silently_replace_the_102_input(self):
        for post in [[], self.receipt["postSeriesPatches"] + [{"path": "third.patch", "sha256": "2" * 64}],
                     list(reversed(self.receipt["postSeriesPatches"]))]:
            with self.subTest(post=post), self.assertRaises(ValueError):
                stage.verify_source_receipt({**self.receipt, "postSeriesPatches": post}, self.lock, self.lock["ffmpeg"]["preparedReceiptSha256"])

    def test_wrong_source_receipt_hash_and_malformed_patch_fail_closed(self):
        for value in [{**self.receipt, "sourceCommit": "wrong"}, {**self.receipt, "patches": [{}] * 100}]:
            with self.assertRaises(ValueError):
                stage.verify_source_receipt(value, self.lock, self.lock["ffmpeg"]["preparedReceiptSha256"])
        with self.assertRaises(ValueError):
            stage.verify_source_receipt(self.receipt, self.lock, "0" * 64)

    def test_process_executables_and_static_objects_are_not_shared_native_libraries(self):
        self.assertTrue(stage.dylib_headers("Mach header\nmagic cputype filetype\nMH_MAGIC_64 ARM64 DYLIB"))
        for kind in ["EXECUTE", "OBJECT", "BUNDLE"]:
            self.assertFalse(stage.dylib_headers("MH_MAGIC_64 ARM64 " + kind))
        self.assertFalse(stage.dylib_headers("not a Mach-O object"))
        self.assertFalse(stage.dylib_headers("MH_MAGIC_64 ARM64 DYLIB\nMH_MAGIC_64 X86_64 EXECUTE"))


if __name__ == "__main__":
    unittest.main()
