#!/usr/bin/env python3
"""Author a real STN EL disc and an explicit-PMT variant from procedural HEVC.

The small pictures deliberately test structure, not UHD authoring conformance or
FEL residual reconstruction. No licensed sample, decryption or loose-file
runtime reader is used. The supplied tsMuxer authors the actual playlist/CLPI.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def crc(data):
    value = 0xffffffff
    for byte in data:
        value ^= byte << 24
        for _ in range(8):
            value = ((value << 1) ^ (0x04c11db7 if value & 0x80000000 else 0)) & 0xffffffff
    return value


def add_dependency(path):
    """Author explicit PMT dependency inside existing TS packet stuffing.

    Fixture authoring only. All PES, ATS/PCR, packet positions and navigation
    indices remain byte-identical; a runtime never repairs user PMTs this way.
    """
    raw = bytearray(path.read_bytes())
    if len(raw) % 192:
        raise ValueError("not complete M2TS packets")
    changed = 0
    for start in range(0, len(raw), 192):
        ts = memoryview(raw)[start + 4:start + 192]
        if ts[0] != 0x47:
            raise ValueError("bad M2TS synchronization")
        pid = int.from_bytes(ts[1:3], "big") & 8191
        if pid != 0x100:
            continue
        if not ts[1] & 0x40 or ts[3] & 0xc0 or not ts[3] & 0x10:
            raise ValueError("unexpected generated PMT transport")
        at = 4 + (1 + ts[4] if ts[3] & 0x20 else 0)
        at += 1 + ts[at]
        size = 3 + (int.from_bytes(ts[at + 1:at + 3], "big") & 4095)
        section = bytes(ts[at:at + size])
        if len(section) != size or crc(section) or section[0] != 2:
            raise ValueError("invalid generated PMT")
        body = bytearray(section[:-4])
        pos = 12 + (int.from_bytes(body[10:12], "big") & 4095)
        found = 0
        while pos < len(body):
            length = int.from_bytes(body[pos + 3:pos + 5], "big") & 4095
            end = pos + 5 + length
            if end > len(body):
                raise ValueError("truncated generated stream descriptor")
            stream_pid = int.from_bytes(body[pos + 1:pos + 3], "big") & 8191
            if stream_pid == 0x1015:
                if body[pos] != 0x24:
                    raise ValueError("generated EL is not HEVC")
                descriptor = (b"\xb0\x07\x01\x00" + ((7 << 9) | (1 << 3) | 6).to_bytes(2, "big") +
                              (0x1011 << 3).to_bytes(2, "big") + b"\x60")
                body[pos + 3:pos + 5] = (0xf000 | (length + len(descriptor))).to_bytes(2, "big")
                body[end:end] = descriptor
                found += 1
                break
            pos = end
        if found != 1:
            raise ValueError("no unique generated EL")
        body[1:3] = (0xb000 | (len(body) + 1)).to_bytes(2, "big")
        replacement = body + crc(body).to_bytes(4, "big")
        if at + len(replacement) > 188 or any(b != 255 for b in ts[at + size:]):
            raise ValueError("PMT would replace payload rather than stuffing")
        ts[at:] = replacement + b"\xff" * (188 - at - len(replacement))
        changed += 1
    if not changed:
        raise ValueError("no generated PMT")
    path.write_bytes(raw)
    return changed


def main():
    p = argparse.ArgumentParser(description=__doc__)
    for name in ("fixtures", "tsmuxer", "helper", "output", "scratch"):
        p.add_argument("--" + name, required=True, type=Path)
    args = p.parse_args()
    output, scratch = args.output.resolve(), args.scratch.resolve()
    output.mkdir(parents=True, exist_ok=False)
    scratch.mkdir(parents=True, exist_ok=True)
    if any(scratch.iterdir()):
        raise ValueError("scratch must be empty")
    manifest = json.loads((args.fixtures / "manifest.json").read_text())
    for name in ("base-pq.hevc", "enhancement-FEL.hevc"):
        # Match the exact qualified procedural source inventory.
        member = next(row for row in manifest["files"] if row["path"] == name)
        if sha(args.fixtures / name) != member["sha256"]:
            raise ValueError("procedural source hash mismatch")
    stn = output / "stn-fel-bdmv"
    env = {**os.environ, "TMPDIR": str(scratch), "TZ": "UTC", "LC_ALL": "C"}
    with tempfile.TemporaryDirectory(prefix="author-", dir=scratch) as temp:
        work = Path(temp)
        for name, source in (("base", "base-pq.hevc"), ("el", "enhancement-FEL.hevc")):
            (work / (name + ".hevc")).write_bytes((args.fixtures / source).read_bytes() * 66)
        meta = work / "disc.meta"
        meta.write_text("MUXOPT --blu-ray --vbr --custom-chapters=00:00:00.000;00:00:33.000 "
                        "--mplsOffset=1 --m2tsOffset=1 --constant-iso-hdr\n"
                        f'V_MPEGH/ISO/HEVC, "{work / "base.hevc"}", fps=24\n'
                        f'V_MPEGH/ISO/HEVC, "{work / "el.hevc"}", fps=24\n')
        with (scratch / "tsmuxer.log").open("wb") as log:
            subprocess.run([str(args.tsmuxer.resolve()), str(meta), str(stn)], env=env,
                           stdout=log, stderr=log, check=True, timeout=60)
    pmt = output / "pmt-fel-bdmv"
    shutil.copytree(stn, pmt)
    replacements = add_dependency(pmt / "BDMV/STREAM/00001.m2ts")
    fixtures = []
    for root, expected_base in ((stn, None), (pmt, 4113)):
        result = subprocess.run([str(args.helper.resolve()), "probe", str(root)], env=env,
                                capture_output=True, timeout=20, check=True)
        if len(result.stdout) > 1048576:
            raise ValueError("helper output bound")
        facts = json.loads(result.stdout)
        titles = facts["titles"]
        if facts["encryption"] != "none" or len(titles) != 1 or titles[0]["durationMs"] != 66000:
            raise ValueError("unexpected generated title facts")
        streams = titles[0]["streams"]
        if len(streams) != 2 or streams[1]["pid"] != 4117 or not streams[1]["dolbyVisionEnhancementLayer"]:
            raise ValueError("actual STN EL track not observed")
        if streams[1].get("dolbyVisionBasePid") != expected_base:
            raise ValueError("dependency was invented or lost")
        fixtures.append({"id": root.name, "path": root.name,
                         "files": {str(f.relative_to(root)): sha(f) for f in sorted(root.rglob("*")) if f.is_file()},
                         "expected": facts})
    result = {"schema": 1, "license": "GPL-3.0-or-later", "content": "procedural pictures and RPU only",
              "sourceManifestSha256": sha(args.fixtures / "manifest.json"),
              "generatorSha256": sha(Path(__file__)), "tsmuxerSha256": sha(args.tsmuxer),
              "tsmuxerSourceCommit": "f06ac394c55ccee1cb76ae54f4636532a2d10253",
              "pmtPacketsAuthored": replacements,
              "scope": "STN/PMT and ordered track source facts; small non-UHD-conforming geometry; no FEL reconstruction, HW1 or full disc playback claim",
              "fixtures": fixtures}
    (output / "manifest.json").write_text(json.dumps(result, indent=2) + "\n")


if __name__ == "__main__":
    main()
