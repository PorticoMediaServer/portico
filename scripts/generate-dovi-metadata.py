#!/usr/bin/env python3
"""Generate procedural RPU fixtures from the qualified public dovi_tool source.

The caller supplies the archive-verified source from portico-libdovi-source.json.
No licensed sample, download, or production FFmpeg binary is involved.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dovi-source", type=Path, required=True)
    parser.add_argument("--dovi-archive", type=Path, required=True)
    parser.add_argument("--scratch", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    authority = json.loads((root / "third_party/ffmpeg/portico-libdovi-source.json").read_text())
    source, scratch, output = (p.resolve() for p in (args.dovi_source, args.scratch, args.output))
    if sha(args.dovi_archive) != authority["sha256"]:
        raise SystemExit("libdovi archive does not match the qualified source")
    prefix = "dovi_tool-" + authority["gitCommit"] + "/"
    with tarfile.open(args.dovi_archive, "r:gz") as archive:
        for member in archive:
            relative = member.name.removeprefix(prefix)
            if member.name == relative or not member.isfile():
                continue
            if relative in ("Cargo.toml", "Cargo.lock") or relative.startswith("dolby_vision/") and not relative.startswith("dolby_vision/target/"):
                if (source / relative).read_bytes() != archive.extractfile(member).read():
                    raise SystemExit("libdovi extracted source differs from archive: " + relative)
    if sha(source / authority["licensePath"]) != authority["licenseSha256"]:
        raise SystemExit("libdovi licence does not match the qualified source")
    if output.exists():
        raise SystemExit("output must be new; compare a repeat before replacing fixtures")
    scratch.mkdir(parents=True, exist_ok=True)
    temporary = scratch / "tmp"
    temporary.mkdir(exist_ok=True)
    manifest = (
        '[package]\nname="portico-dovi-metadata-fixtures"\nversion="0.0.0"\nedition="2024"\n'
        '[[bin]]\nname="portico-dovi-metadata-fixtures"\npath='
        + json.dumps(str(root / "third_party/ffmpeg/tests/generate-dovi-metadata.rs"))
        + '\n[dependencies]\ndolby_vision={path='
        + json.dumps(str(source / "dolby_vision"))
        + ',features=["serde"]}\nserde_json="=1.0.150"\n'
    )
    (scratch / "Cargo.toml").write_text(manifest)
    shutil.copyfile(root / "third_party/ffmpeg/tests/dovi-metadata.Cargo.lock", scratch / "Cargo.lock")
    env = dict(os.environ, TMPDIR=str(temporary), CARGO_TARGET_DIR=str(scratch / "target"), CARGO_BUILD_JOBS="1")
    result = subprocess.run(
        ["cargo", "run", "--offline", "--locked", "--manifest-path", str(scratch / "Cargo.toml"),
         "--jobs", "1", "--", str(output)],
        env=env, check=True, stdout=subprocess.PIPE, text=True,
    )
    fixtures = json.loads(result.stdout)
    for fixture in fixtures:
        fixture["sha256"] = sha(output / fixture["path"])
    inventory = {
        "schema": 1, "source": {"repository": authority["repository"], "commit": authority["gitCommit"],
        "archiveSha256": authority["sha256"], "crate": authority["crateVersion"]},
        "license": "GPL-3.0-only; mathematical generated metadata, no supplied licensed samples",
        "generatorLockSha256": sha(scratch / "Cargo.lock"), "fixtures": fixtures,
    }
    (output / "manifest.json").write_text(json.dumps(inventory, indent=2) + "\n")
    print(json.dumps(inventory, indent=2))


if __name__ == "__main__":
    main()
