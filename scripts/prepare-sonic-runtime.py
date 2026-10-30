#!/usr/bin/env python3
"""Stage a pinned original ONNX CPU SDK and its licences without installing it."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import shutil
import tarfile
import tempfile


ROOT = Path(__file__).resolve().parent.parent


def prepare(archive, output, target, lock):
    package = lock["packages"][target]
    if output.exists():
        raise ValueError("output already exists; never replace an installed runtime")
    if "bytes" in package and archive.stat().st_size != package["bytes"]:
        raise ValueError("original runtime package byte count mismatch")
    if archive.stat().st_size > 100 << 20 or hashlib.sha256(archive.read_bytes()).hexdigest() != package["sha256"]:
        raise ValueError("original runtime package SHA256 mismatch")
    if lock["license"] != "MIT" or lock["version"] != "1.20.1":
        raise ValueError("unapproved runtime or licence")
    prefix = "onnxruntime-" + target + "-" + lock["version"]
    with tarfile.open(archive) as source:
        members = source.getmembers()
        if len(members) > 4096 or sum(m.size for m in members) > 150 << 20:
            raise ValueError("unbounded runtime SDK")
        for member in members:
            path = PurePosixPath(member.name)
            if path.is_absolute() or ".." in path.parts or not path.parts or path.parts[0] != prefix:
                raise ValueError("runtime archive path outside the pinned SDK")
            if not (member.isfile() or member.isdir() or member.issym()):
                raise ValueError("unsupported runtime archive member")
        legal = source.extractfile(prefix + "/LICENSE")
        if legal is None or b"Permission is hereby granted, free of charge" not in legal.read(16 << 10):
            raise ValueError("original MIT licence missing")
        output.parent.mkdir(parents=True, exist_ok=True)
        staging = Path(tempfile.mkdtemp(prefix="sonic-runtime-", dir=output.parent))
        try:
            source.extractall(staging, filter="data")
            sdk = staging / prefix
            if not (sdk / "ThirdPartyNotices.txt").is_file():
                raise ValueError("runtime third-party notices missing")
            sdk.rename(output)
        finally:
            shutil.rmtree(staging)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    parser.add_argument("--archive", required=True, type=Path)
    parser.add_argument("--target", required=True)
    args = parser.parse_args()
    lock = json.loads((ROOT / "third_party/portico-sonic/runtime.lock.json").read_text())
    try:
        prepare(args.archive, args.output, args.target, lock)
    except (OSError, ValueError, KeyError, tarfile.TarError) as error:
        parser.exit(1, "Runtime not staged: " + str(error) + "\n")
    print("Verified original ONNX Runtime SDK/licences staged; inference is not enabled.")


if __name__ == "__main__":
    main()
