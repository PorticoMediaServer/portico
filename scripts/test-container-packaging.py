#!/usr/bin/env python3
"""Check official deployment mounts without building or pulling an image.

Run with python3 scripts/test-container-packaging.py. Set
PORTICO_PACKAGING_TEST_IMAGE to an existing Linux image with sh, stat and awk
to also verify the runtime mounts. TMPDIR must then point to owned disk scratch.
"""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import uuid
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[1]
EXAMPLES = sorted((ROOT / "packaging/compose").glob("*.yaml")) + sorted(
    (ROOT / "packaging/truenas").glob("*.yaml")
) + [ROOT / "packaging/synology/compose.yaml"]
IMAGE = os.environ.get("PORTICO_PACKAGING_TEST_IMAGE")


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
    def test_compose_keeps_bounded_memory_separate_from_disk(self):
        self.assertEqual(len(EXAMPLES), 6)
        for path in EXAMPLES:
            with self.subTest(example=str(path.relative_to(ROOT))):
                service = compose_service(path)
                mounts = service["tmpfs"]
                self.assertEqual(len(mounts), 1)
                target, options = mounts[0].split(":", 1)
                self.assertEqual(target, "/memory-transcode")
                self.assertEqual(set(options.split(",")), {
                    "rw", "noexec", "nosuid", "nodev", "size=512m",
                    "nr_inodes=8192", "mode=1777",
                })
                volumes = {v["target"]: v for v in service["volumes"]}
                for target in ["/config", "/transcode"]:
                    self.assertEqual(volumes[target]["type"], "bind")
                    self.assertFalse(volumes[target].get("read_only", False))
                self.assertTrue(volumes["/media"]["read_only"])
                self.assertNotIn("/memory-transcode", volumes)
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

    @unittest.skipUnless(IMAGE, "set PORTICO_PACKAGING_TEST_IMAGE for runtime checks")
    def test_kernel_mount_limits_permissions_and_builtin_shm(self):
        explicit = compose_service(EXAMPLES[0])["tmpfs"][0]
        # No producer stress/load: write one tiny file and inspect kernel facts.
        for mount, target, size, inodes in [
            (explicit, "/memory-transcode", 512 << 20, 8192),
            (None, "/dev/shm", 64 << 20, 0),
        ]:
            with self.subTest(target=target), tempfile.TemporaryDirectory(
                prefix="container-packaging-", dir=os.environ["TMPDIR"]
            ) as temporary:
                disk = Path(temporary) / "transcode"
                disk.mkdir(mode=0o777)
                disk.chmod(0o777)
                name = "portico-packaging-test-" + uuid.uuid4().hex[:12]
                argv = [
                    "docker", "run", "--rm", "--pull=never", "--network=none",
                    "--name", name, "--memory=128m", "--memory-swap=128m",
                    "--cpus=0.5", "--user=1001:1002",
                    "--mount", f"type=bind,src={disk},dst=/transcode",
                ]
                if mount:
                    argv += ["--tmpfs", mount]
                command = r'''
set -eu
root=$1; size=$2; inodes=$3
test "$(stat -f -c %T "$root")" = tmpfs
block_size=$(stat -f -c %S "$root")
blocks=$(stat -f -c %b "$root")
test "$((block_size * blocks))" -eq "$size"
if [ "$inodes" != 0 ]; then test "$(stat -f -c %c "$root")" -eq "$inodes"; fi
test "$(stat -c %a "$root")" = 1777
awk -v root="$root" '$2 == root { n=split($4,opts,","); for(i=1;i<=n;i++) flags[opts[i]]=1; found=flags["rw"] && flags["noexec"] && flags["nosuid"] && flags["nodev"] } END {exit !found}' /proc/mounts
umask 077
mkdir "$root/portico-packaging-test"
test "$(stat -c %a "$root/portico-packaging-test")" = 700
printf memory > "$root/portico-packaging-test/segment"
printf disk > /transcode/segment
test "$(cat /transcode/segment)" = disk
'''
                try:
                    result = subprocess.run(
                        argv + ["--entrypoint=/bin/sh", IMAGE, "-c", command,
                                "packaging-test", target, str(size), str(inodes)],
                        capture_output=True, text=True, timeout=30,
                    )
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertEqual((disk / "segment").stat().st_size, 4)
                finally:
                    subprocess.run(["docker", "rm", "-f", name],
                                   capture_output=True, timeout=15)


if __name__ == "__main__":
    unittest.main()
