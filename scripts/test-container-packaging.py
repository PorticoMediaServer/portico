#!/usr/bin/env python3
"""Check official deployment mounts without building or pulling an image.

Run with python3 scripts/test-container-packaging.py.
"""
import json
import os
from pathlib import Path
import subprocess
import unittest
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[1]
EXAMPLES = sorted((ROOT / "packaging/compose").glob("*.yaml")) + sorted(
    (ROOT / "packaging/truenas").glob("*.yaml")
) + [ROOT / "packaging/synology/compose.yaml"]


def compose_service(path):
    # Config validation needs neither a running daemon nor an image. Supply a
    # syntactically valid GPU selection for the required TrueNAS interpolation.
    env = dict(os.environ, PORTICO_NVIDIA_GPU="GPU-packaging-test")
    result = subprocess.run(
        ["docker", "compose", "--project-name", "portico-packaging-test",
         "-f", str(path), "config", "--format", "json"],
        env=env, capture_output=True, text=True, timeout=15, check=True,
    )
    return json.loads(result.stdout)["services"]["portico"]


class ContainerPackagingTests(unittest.TestCase):
    def test_compose_maps_config_and_conversions_to_disk(self):
        self.assertEqual(len(EXAMPLES), 6)
        for path in EXAMPLES:
            with self.subTest(example=str(path.relative_to(ROOT))):
                service = compose_service(path)
                # The server never uses a separate memory mount: conversions go to /transcode.
                self.assertNotIn("tmpfs", service)
                volumes = {v["target"]: v for v in service["volumes"]}
                for target in ["/config", "/transcode"]:
                    self.assertEqual(volumes[target]["type"], "bind")
                    self.assertFalse(volumes[target].get("read_only", False))
                self.assertTrue(volumes["/media"]["read_only"])
                self.assertFalse(service.get("privileged", False))
                self.assertNotIn("SYS_ADMIN", service.get("cap_add", []))

    def test_unraid_retains_disk_mapping_and_unprivileged_runtime(self):
        for path in sorted((ROOT / "packaging/unraid").glob("*.xml")):
            with self.subTest(example=path.name):
                template = ET.parse(path).getroot()
                self.assertEqual(template.findtext("Privileged"), "false")
                disk = template.find("Config[@Target='/transcode']")
                self.assertIsNotNone(disk)
                self.assertEqual(disk.get("Type"), "Path")
                self.assertEqual(disk.get("Mode"), "rw")
                self.assertTrue(disk.text.startswith("/mnt/cache/"))


if __name__ == "__main__":
    unittest.main()
