#!/usr/bin/env python3
"""Preserve the source seal and authenticate the final codesigned payload."""
import argparse
import hashlib
import json
import pathlib
import runpy
import subprocess

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey, Ed25519PublicKey

ROOT = pathlib.Path(__file__).resolve().parent.parent
RCLONE = runpy.run_path(str(ROOT / "scripts/rclone-bundle.py"))
BUILDER = runpy.run_path(str(ROOT / "scripts/build-server-release.py"))
artifact_identity = BUILDER["artifact_identity"]
tags_are_official = BUILDER["tags_are_official"]


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def verify_payload(directory, public_key):
    raw = (directory / "manifest.json").read_bytes()
    public_key.verify((directory / "manifest.sig").read_bytes(), raw)
    manifest = json.loads(raw)
    if manifest.get("component") != "server" or manifest.get("target") != "darwin-arm64":
        raise ValueError("Expected an Apple Silicon server payload")
    if manifest["artifact"]["path"] != "portico-server":
        raise ValueError("Invalid server artifact path")
    binary = directory / "portico-server"
    if binary.is_symlink() or digest(binary) != manifest["artifact"]["sha256"]:
        raise ValueError("Server artifact changed")
    bundle = directory / "third_party/ffmpeg"
    if digest(bundle / "toolchain-manifest.json") != manifest["toolchainManifestSha256"]:
        raise ValueError("Toolchain manifest changed")
    subprocess.run(["python3", str(ROOT / "scripts/verify-ffmpeg-manifest.py"), str(bundle)], check=True)
    if json.loads((bundle / "toolchain-manifest.json").read_text())["target"] != "macos-arm64":
        raise ValueError("Toolchain target does not match the server")
    rclone = directory / "third_party/rclone"
    if digest(rclone / "bundle-manifest.json") != manifest["rcloneManifestSha256"]:
        raise ValueError("rclone manifest changed")
    RCLONE["verify"](rclone, "darwin-arm64", codesigned="sourceSeal" in manifest)
    embedded, settings = artifact_identity(binary)
    prefix = "github.com/PorticoMediaServer/portico/server/internal/buildinfo."
    for symbol, field in [("SourceDigest", "sourceDigest"), ("BuildID", "buildId"), ("Version", "version")]:
        if embedded.get(prefix + symbol) != manifest[field]:
            raise ValueError("Embedded build identity does not match the source seal")
    if not tags_are_official("server", settings.get("-tags")) or settings.get("GOOS") != "darwin" or settings.get("GOARCH") != "arm64":
        raise ValueError("Invalid compiler target or release mode")
    if embedded.get("github.com/PorticoMediaServer/portico/server/internal/hostedtrust.OfficialRoot") != manifest.get("officialRoot"):
        raise ValueError("Official trust root changed")
    if "sourceSeal" in manifest:
        original = directory / "source-seal"
        if set(manifest["sourceSeal"]) != {"manifest.json", "manifest.sig", "toolchain-manifest.json", "rclone-manifest.json"}:
            raise ValueError("Incomplete source seal inventory")
        for name, expected in manifest["sourceSeal"].items():
            if name not in ("manifest.json", "manifest.sig", "toolchain-manifest.json", "rclone-manifest.json"):
                raise ValueError("Invalid source seal inventory")
            if digest(original / name) != expected:
                raise ValueError("Original source seal changed")
        public_key.verify((original / "manifest.sig").read_bytes(), (original / "manifest.json").read_bytes())
        source_manifest = json.loads((original / "manifest.json").read_bytes())
        comparable = json.loads(raw)
        comparable.pop("sourceSeal")
        comparable["artifact"]["sha256"] = source_manifest["artifact"]["sha256"]
        comparable["toolchainManifestSha256"] = source_manifest["toolchainManifestSha256"]
        comparable["rcloneManifestSha256"] = source_manifest["rcloneManifestSha256"]
        if comparable != source_manifest:
            raise ValueError("Original source seal describes a different build")
        if digest(original / "toolchain-manifest.json") != source_manifest["toolchainManifestSha256"]:
            raise ValueError("Original toolchain provenance does not match the source seal")
        if digest(original / "rclone-manifest.json") != source_manifest["rcloneManifestSha256"]:
            raise ValueError("Original rclone provenance does not match the source seal")
    return manifest, (embedded, settings)


def seal_payload(directory, key, identity):
    public_key = key.public_key()
    manifest, original_identity = verify_payload(directory, public_key)
    if "sourceSeal" in manifest:
        raise ValueError("Payload already has a codesigned derivative seal")
    original = directory / "source-seal"
    original.mkdir()
    bundle = directory / "third_party/ffmpeg"
    for name, source in [("manifest.json", directory / "manifest.json"),
                         ("manifest.sig", directory / "manifest.sig"),
                         ("toolchain-manifest.json", bundle / "toolchain-manifest.json"),
                         ("rclone-manifest.json", directory / "third_party/rclone/bundle-manifest.json")]:
        (original / name).write_bytes(source.read_bytes())
    # Source provenance is authenticated before the first platform mutation.
    # App signing later must not re-sign these already sealed nested binaries.
    media_binaries = sorted((bundle / "bin").iterdir())
    rclone = directory / "third_party/rclone"
    binaries = [directory / "portico-server"] + media_binaries + [rclone / "rclone"]
    for binary in binaries:
        subprocess.run(["codesign", "--force", "--options", "runtime", "--timestamp=none",
                        "--sign", identity, str(binary)], check=True)
        subprocess.run(["codesign", "--verify", "--strict", str(binary)], check=True)
    if artifact_identity(directory / "portico-server") != original_identity:
        raise ValueError("Codesigning changed the compiler/source identity")
    toolchain_path = bundle / "toolchain-manifest.json"
    toolchain = json.loads(toolchain_path.read_text())
    for binary in media_binaries:
        toolchain["files"][str(binary.relative_to(bundle))] = digest(binary)
    toolchain["sourceManifestSha256"] = digest(original / "toolchain-manifest.json")
    toolchain_path.write_text(json.dumps(toolchain, indent=2) + "\n")
    rclone_manifest_path = rclone / "bundle-manifest.json"
    rclone_manifest = json.loads(rclone_manifest_path.read_text())
    rclone_manifest["upstreamBinarySha256"] = rclone_manifest["files"]["rclone"]
    rclone_manifest["files"]["rclone"] = digest(rclone / "rclone")
    rclone_manifest_path.write_text(json.dumps(rclone_manifest, indent=2) + "\n")
    manifest["rcloneManifestSha256"] = digest(rclone_manifest_path)
    manifest["artifact"]["sha256"] = digest(directory / "portico-server")
    manifest["toolchainManifestSha256"] = digest(toolchain_path)
    manifest["sourceSeal"] = {name: digest(original / name) for name in
                              ("manifest.json", "manifest.sig", "toolchain-manifest.json", "rclone-manifest.json")}
    raw = (json.dumps(manifest, indent=2) + "\n").encode()
    (directory / "manifest.json").write_bytes(raw)
    (directory / "manifest.sig").write_bytes(key.sign(raw))
    verify_payload(directory, public_key)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("directory", type=pathlib.Path)
    parser.add_argument("--key", type=pathlib.Path)
    parser.add_argument("--public-key", type=pathlib.Path)
    parser.add_argument("--trusted-public-key", type=pathlib.Path)
    parser.add_argument("--public-key-output", type=pathlib.Path)
    parser.add_argument("--identity")
    parser.add_argument("--verify", action="store_true")
    args = parser.parse_args()
    if args.verify:
        if args.public_key is None or args.key is not None or args.public_key_output is not None:
            parser.error("--verify requires --public-key and does not accept a private key")
        public_key = Ed25519PublicKey.from_public_bytes(args.public_key.read_bytes())
        verify_payload(args.directory, public_key)
        return
    if args.key is None or args.public_key is not None or not args.identity:
        parser.error("sealing requires --key and --identity")
    # Never generate a replacement release key on the packaging runner.
    if args.key.is_symlink() or not args.key.is_file() or args.key.stat().st_mode & 0o077:
        raise ValueError("An existing private release signing key is required")
    if args.public_key_output is not None:
        if args.public_key_output.is_symlink() or args.public_key_output.resolve() == args.key.resolve():
            raise ValueError("Public output must not replace the private signing key")
    key = Ed25519PrivateKey.from_private_bytes(args.key.read_bytes())
    if args.trusted_public_key is None or args.trusted_public_key.read_bytes()!=key.public_key().public_bytes_raw():
        raise ValueError("Provide the pinned release public key matching the approved signer")
    seal_payload(args.directory, key, args.identity)
    if args.public_key_output is not None:
        args.public_key_output.write_bytes(key.public_key().public_bytes_raw())


if __name__ == "__main__":
    main()
