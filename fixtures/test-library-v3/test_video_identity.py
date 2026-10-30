"""Offline generation smoke: real names/NFOs, no encoders or artwork needed."""
import collections
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import common
import video


class VideoIdentityTests(unittest.TestCase):
    def test_template_copies_are_independent_assets(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            template = root / "template.mp4"
            template.write_bytes(b"procedural media template")
            first, second = root / "one.mp4", root / "two.mp4"
            common.copy_media(template, first)
            common.copy_media(template, second)
            self.assertEqual(first.read_bytes(), second.read_bytes())
            self.assertFalse(first.samefile(second))
            self.assertFalse(first.samefile(template))
            # A repeated generation must not overwrite an existing fixture.
            first.write_bytes(b"existing fixture")
            common.copy_media(template, first)
            self.assertEqual(first.read_bytes(), b"existing fixture")

    def test_regeneration_detaches_only_the_old_template_alias(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            template, first, sibling = (root / name for name in ("template.mp4", "one.mp4", "sibling.mp4"))
            template.write_bytes(b"original template bytes")
            os.link(template, first)
            os.link(template, sibling)
            common.copy_media(template, first)
            self.assertEqual(first.read_bytes(), template.read_bytes())
            self.assertFalse(first.samefile(template))
            self.assertTrue(sibling.samefile(template))
            self.assertEqual(list(root.glob(".media-copy-*")), [])

    def test_every_fixture_show_keeps_its_expected_episodes_and_assets(self):
        expected = {
            "Harrow Station": 12, "Signal Ridge": 30,
            "The Quiet Fleet": 14, "Night Shift at the Pier": 9,
            "Brass Lantern": 10, "Marrow and Bone": 8,
            "The Cartographers": 9, "The Long Winter": 12,
            "The Orchard House": 24, "Orbit Classroom": 8,
            "The Understudy's Room": 12, "November Rain": 8,
            "Static Garden": 6, "Starfall Academy": 14,
            "Lantern Keepers": 24,
        }
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            template = root / "template.mp4"
            template.write_bytes(b"same bytes, distinct episodes")
            templates = collections.defaultdict(lambda: template)

            def mux(_ffmpeg, target, *_args, **_kwargs):
                # Only encoder/artwork work is substituted. The production
                # episode loop, naming, NFOs and template-copy path run intact.
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(template.read_bytes())

            with patch.object(video, "_mux", mux), \
                 patch.object(video, "_duration_seconds", return_value=8), \
                 patch.object(video, "art"), patch.object(video, "_art_set"):
                entries = video._shows(root, templates, "unused-ffmpeg")
                entries += video._anime(root, templates, "unused-ffmpeg")

            episodes = [entry for entry in entries if entry["kind"] == "episode"]
            counts = collections.Counter()
            for entry in episodes:
                folder = entry["path"].split("/")[1]
                counts[folder.rsplit(" (", 1)[0]] += 1
            self.assertEqual(dict(counts), expected)
            self.assertEqual(len(episodes), 200)
            inventory = json.loads((HERE.parents[1] / "server/internal/ingestion/testdata/synthetic_episode_inventory.json").read_text())
            self.assertEqual({entry["path"] for entry in episodes},
                             set(inventory["files"]))
            self.assertEqual({row["folder"].rsplit(" (", 1)[0]: row["episodes"]
                              for row in inventory["shows"]}, expected)

            # Multi-episode files deliberately share one pathname. Different
            # pathnames must never share inventory's device/inode identity.
            identities = {}
            for path in {entry["path"] for entry in episodes}:
                stat = (root / path).stat()
                key = stat.st_dev, stat.st_ino
                self.assertNotIn(key, identities,
                                 f"{path} aliases {identities.get(key)}")
                identities[key] = path


if __name__ == "__main__":
    unittest.main()
