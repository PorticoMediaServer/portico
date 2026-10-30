#!/usr/bin/env python3
"""Bounded host qualification of the post-series BSF, not a Portico bundle build.

Use an already prepared Jellyfin source tree and cargo-c-installed libdovi.
Source preparation, canonical dependencies and target bundles remain HW-owned.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import subprocess


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ffmpeg-source", type=Path, required=True)
    parser.add_argument("--dovi-prefix", type=Path, required=True)
    parser.add_argument("--scratch", type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    source, dovi, scratch = (p.resolve() for p in (args.ffmpeg_source, args.dovi_prefix, args.scratch))
    receipt = json.loads((source / "portico-source-receipt.json").read_text())
    if receipt["sourceCommit"] != "253db2a7b0a8045c54ce68ce33d7f601229b1822" or len(receipt["patches"]) != 100:
        raise SystemExit("expected the qualified Jellyfin source plus all 100 upstream patches")
    patch = root / "third_party/ffmpeg/portico-dovi-profile.patch"
    patch_check = subprocess.run(["patch", "--reverse", "--dry-run", "--fuzz=0", "-p1", "-i", str(patch)],
                                 cwd=source, check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    scratch.mkdir(parents=True, exist_ok=True)
    build, prefix, temporary = (scratch / name for name in ("build", "prefix", "tmp"))
    for path in (build, prefix, temporary):
        path.mkdir(exist_ok=True)
    env = dict(os.environ, TMPDIR=str(temporary), PKG_CONFIG_PATH=str(dovi / "lib/pkgconfig"))
    configure = [str(source / "configure"), "--disable-everything", "--disable-autodetect",
                 "--disable-network", "--disable-doc", "--disable-x86asm", "--enable-ffmpeg", "--enable-ffprobe",
                 "--enable-libdovi", "--enable-bsf=dovi_profile,dovi_rpu", "--pkg-config-flags=--static",
                 "--prefix=" + str(prefix)]
    with (scratch / "configure.log").open("w") as log:
        subprocess.run(configure, cwd=build, env=env, check=True, stdout=log, stderr=subprocess.STDOUT)
    with (scratch / "build.log").open("w") as log:
        subprocess.run(["make", "-j1"], cwd=build, env=env, check=True, stdout=log, stderr=subprocess.STDOUT)
        subprocess.run(["make", "-j1", "install"], cwd=build, env=env, check=True, stdout=log, stderr=subprocess.STDOUT)
    env["PKG_CONFIG_PATH"] = str(prefix / "lib/pkgconfig") + os.pathsep + env["PKG_CONFIG_PATH"]
    flags = subprocess.check_output(["pkg-config", "--cflags", "--libs", "--static", "libavcodec", "libavutil", "dovi"], env=env, text=True)
    test_source = root / "third_party/ffmpeg/tests/dovi-profile-packets.c"
    test_binary = scratch / "dovi-profile-packets"
    subprocess.run(["cc", "-std=c11", "-O2", "-Wall", "-Wextra", "-Werror", str(test_source), "-o", str(test_binary)] + shlex.split(flags), env=env, check=True)
    result = subprocess.run([str(test_binary), str(root / "fixtures/video/dovi-metadata")], env=env, check=True, stdout=subprocess.PIPE, text=True)
    packet_report = json.loads(result.stdout)
    help_text = subprocess.check_output([str(prefix / "bin/ffmpeg"), "-hide_banner", "-h", "bsf=dovi_profile"], env=env, stderr=subprocess.STDOUT, text=True)
    stock_text = subprocess.check_output([str(prefix / "bin/ffmpeg"), "-hide_banner", "-h", "bsf=dovi_rpu"], env=env, stderr=subprocess.STDOUT, text=True)
    option = lambda name, text: re.search(r"(?m)^\s+-?" + re.escape(name) + r"\s+<", text)
    if not option("mode", help_text) or not option("strip", stock_text) or not option("compression", stock_text) or option("mode", stock_text):
        raise SystemExit("actual BSF option introspection mismatch")
    (scratch / "dovi-profile-help.txt").write_text(help_text)
    (scratch / "dovi-rpu-help.txt").write_text(stock_text)
    report = {"sourceCommit": receipt["sourceCommit"], "upstreamPatchCount": 100,
              "porticoPatchSha256": sha(patch), "patchDryRun": patch_check.stdout, "testSourceSha256": sha(test_source),
              "binarySha256": sha(prefix / "bin/ffmpeg"), "packetTests": packet_report,
              "profileHelp": help_text, "stockHelp": stock_text,
              "scope": "minimal host FFmpeg/C-ABI/metadata-packet proof; not HW1, decoded BL/EL video or target acceptance"}
    (scratch / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
