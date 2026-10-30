"""Disposable, offline release inputs for packaging regressions.

The production verifier scripts are copied without modification. The fixture
repository has its own explicit rclone contract and a fresh release signer;
it never substitutes a verifier or fetches an upstream binary. This exercises
packaging/seal behavior, not upstream rclone authentication or OS signing.
Real launchers, installer recipes and license inputs must come from checkout.
"""

import hashlib
import json
import pathlib
import shutil

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

ROOT = pathlib.Path(__file__).resolve().parent.parent
SCRIPTS = (
    "package-release.sh", "package-macos.sh", "rclone-bundle.py",
    "seal-macos-payload.py", "verify-ffmpeg-manifest.py",
    "build-server-release.py", "release-build-tags.py", "sonic-bundle.py", "env.sh",
)
PORTABLE_INPUTS = (
    "LICENSE", "THIRD-PARTY-NOTICES.md", "packaging/portable",
    "packaging/linux", "packaging/windows",
    "server/internal/imagework/SCALE_LICENSE",
    "server/internal/thirdparty/sqlite/LICENSE",
    "server/internal/mediaexec/fonts/LICENSE", "third_party/hls.js/LICENSE",
    "third_party/reader", "packages/reader-core/assets/OFL.txt",
    "web/src/kit/fonts/OFL.txt", "packages/design/LICENSE-LUCIDE",
)


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


class ReleaseFixture:
    def __init__(self, root, source=ROOT):
        self.source = source
        self.root = root
        self.repo = root / "fixture-repository"
        self.repo.mkdir()
        self.copy_inputs(tuple("scripts/" + name for name in SCRIPTS))
        # The exported work package may not retain git's executable mode.
        for name in SCRIPTS:
            if name.endswith(".sh"):
                (self.repo / "scripts" / name).chmod(0o755)
        self.key = Ed25519PrivateKey.generate()
        self.keyfile = root / "disposable-release-key"
        self.keyfile.write_bytes(self.key.private_bytes_raw())
        self.keyfile.chmod(0o600)
        self.publicfile = root / "disposable-release-public-key"
        self.publicfile.write_bytes(self.key.public_key().public_bytes_raw())
        self.contract = self.repo / "third_party/rclone"
        self.contract.mkdir(parents=True)
        self.payloads = {}
        targets = {}
        for target in ("linux-amd64", "windows-amd64", "darwin-arm64"):
            binary = "rclone.exe" if target.startswith("windows-") else "rclone"
            payload = ("disposable rclone payload: " + target + "\n").encode()
            self.payloads[target] = {binary: payload}
            targets[target] = {
                "binary": binary, "archiveRoot": "fixture-" + target,
                "url": "fixture://unused/rclone-" + target + ".zip",
                "sha256": hashlib.sha256(("fixture archive " + target).encode()).hexdigest(),
                "files": {binary: hashlib.sha256(payload).hexdigest()},
            }
        for name in ("LICENSE", "DEPENDENCY-NOTICES.txt", "KEYS"):
            (self.contract / name).write_text("Disposable fixture " + name + "\n")
        (self.contract / "SHA256SUMS").write_text("".join(
            value["sha256"] + "  " + value["url"].rsplit("/", 1)[1] + "\n"
            for value in targets.values()))
        self.lock = {
            "version": "0.0.0-fixture", "revision": "disposable-fixture",
            "checksumsSha256": digest(self.contract / "SHA256SUMS"),
            "targets": targets,
        }
        write_json(self.contract / "bundle.lock.json", self.lock)

    def copy_inputs(self, paths):
        missing = [name for name in paths if not (self.source / name).exists()]
        if missing:
            raise FileNotFoundError("Packaging tests require real checkout inputs: " + ", ".join(missing))
        for name in paths:
            source, destination = self.source / name, self.repo / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            if source.is_dir():
                shutil.copytree(source, destination)
            else:
                shutil.copy2(source, destination)

    def rclone(self, destination, target):
        destination.mkdir(parents=True)
        selected = self.lock["targets"][target]
        for name, raw in self.payloads[target].items():
            (destination / name).write_bytes(raw)
        (destination / selected["binary"]).chmod(0o755)
        for path in self.contract.iterdir():
            shutil.copy2(path, destination / path.name)
        write_json(destination / "bundle-manifest.json", {
            "schema": 1, "target": target, "version": self.lock["version"],
            "revision": self.lock["revision"], "archiveSha256": selected["sha256"],
            "files": {path.name: digest(path) for path in destination.iterdir()},
        })

    def sign(self, directory, target):
        binary = "portico-server.exe" if target.startswith("windows-") else "portico-server"
        manifest = {
            "schema": 1, "component": "server", "target": target,
            "version": "0.1.0", "sourceDigest": "a" * 64,
            "buildId": "fixture", "officialRoot": "fixture-root",
            "artifact": {"path": binary, "sha256": digest(directory / binary)},
            "toolchainManifestSha256": digest(directory / "third_party/ffmpeg/toolchain-manifest.json"),
            "rcloneManifestSha256": digest(directory / "third_party/rclone/bundle-manifest.json"),
        }
        raw = (json.dumps(manifest, indent=2) + "\n").encode()
        (directory / "manifest.json").write_bytes(raw)
        (directory / "manifest.sig").write_bytes(self.key.sign(raw))
        return raw
