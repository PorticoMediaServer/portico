#!/usr/bin/env python3
"""Verify the original published ISC musicnn weights before staging them.

This offline tool accepts only the exact publisher wheel and pinned model files.
It does not install TensorFlow, fetch recordings, or enable Sonic analysis.
"""
import argparse
import email.parser
import hashlib
import json
from pathlib import Path
import shutil
import tempfile
import zipfile


ROOT = Path(__file__).resolve().parent.parent


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def prepare(wheel, output, lock):
    distribution = lock["distribution"]
    if wheel.stat().st_size != distribution["bytes"] or digest(wheel) != distribution["sha256"]:
        raise ValueError("publisher wheel size or SHA256 mismatch")
    if output.exists():
        raise ValueError("output already exists; never overwrite an installed model")
    license_source = ROOT / "third_party/musicnn/LICENSE.md"
    if digest(license_source) != lock["licenseSHA256"] or lock["license"] != "ISC":
        raise ValueError("pinned original licence mismatch")
    verified = {}
    if lock["sourceModel"] not in ("MTT_musicnn", "MSD_musicnn"):
        raise ValueError("unknown original musicnn model")
    with zipfile.ZipFile(wheel) as archive:
        names = archive.namelist()
        if len(names) != len(set(names)):
            raise ValueError("duplicate wheel members")

        def read(name, maximum):
            info = archive.getinfo(name)
            if info.file_size > maximum or info.flag_bits & 1:
                raise ValueError("unbounded or encrypted wheel member")
            with archive.open(info) as member:
                data = member.read(maximum + 1)
            if len(data) > maximum:
                raise ValueError("unbounded wheel member")
            return data

        metadata = email.parser.BytesParser().parsebytes(read(distribution["metadata"], 1 << 20))
        if metadata["Name"] != "musicnn" or metadata["Version"] != "0.1.0" or metadata["License"] != "ISC":
            raise ValueError("publisher package does not declare the expected ISC distribution")
        license_data = read(distribution["licenseFile"], 16 << 10)
        if hashlib.sha256(license_data).hexdigest() != lock["licenseSHA256"]:
            raise ValueError("publisher licence differs from original pinned licence")
        for expected in lock["files"]:
            name = expected["path"]
            relative = Path(name)
            if (not relative.is_relative_to("musicnn/" + lock["sourceModel"]) and name not in ("musicnn/models.py", "musicnn/configuration.py")) or ".." in relative.parts:
                raise ValueError("model path outside the original weights/source")
            data = read(name, expected["bytes"])
            if len(data) != expected["bytes"] or hashlib.sha256(data).hexdigest() != expected["sha256"]:
                raise ValueError("original model file mismatch: " + name)
            verified[relative.name] = data
    # Every file and its licence are verified before anything is published.
    output.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix="musicnn-", dir=output.parent))
    try:
        for name, data in verified.items():
            (staging / name).write_bytes(data)
        (staging / "LICENSE.md").write_bytes(license_source.read_bytes())
        (staging / "model.json").write_text(json.dumps({
            "id": lock["modelId"], "version": lock["modelVersion"],
            "licence": "ISC", "format": "tensorflow-checkpoint",
            "files": lock["files"],
        }, indent=2) + "\n")
        staging.rename(output)
    finally:
        if staging.exists():
            shutil.rmtree(staging)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    parser.add_argument("--wheel", required=True, type=Path)
    args = parser.parse_args()
    lock = json.loads((ROOT / "third_party/musicnn/model.lock.json").read_text())
    try:
        prepare(args.wheel, args.output, lock)
    except (OSError, ValueError, KeyError, zipfile.BadZipFile) as error:
        parser.exit(1, "Model not staged: " + str(error) + "\n")
    print("Verified original ISC musicnn weights staged; inference runtime is not enabled.")


if __name__ == "__main__":
    main()
