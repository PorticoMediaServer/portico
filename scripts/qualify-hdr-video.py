#!/usr/bin/env python3
"""Qualify real fixture packets, MP4 signalling, base pictures and RPU conversion.

Use a roundtrip test binary linked against the exact FFmpeg/libdovi under test.
This never enables a production capability or claims FEL reconstruction.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run(args):
    p = subprocess.run([str(a) for a in args], capture_output=True, timeout=120)
    if p.returncode:
        raise RuntimeError((p.stdout + p.stderr).decode(errors="replace")[-8192:])
    return p.stdout.decode(errors="replace")


def nals(data):
    starts = list(re.finditer(b"\x00\x00\x00\x01|\x00\x00\x01", data))
    if not starts or starts[0].start() != 0:
        raise ValueError("not Annex B")
    return [data[m.end():starts[i + 1].start() if i + 1 < len(starts) else len(data)]
            for i, m in enumerate(starts)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("fixtures", "scratch", "ffmpeg", "ffprobe", "dovi-tool", "roundtrip-binary"):
        parser.add_argument("--" + name, type=Path, required=True)
    args = parser.parse_args()
    fixtures, scratch = args.fixtures.resolve(), args.scratch.resolve()
    scratch.mkdir(parents=True, exist_ok=True)
    if any(scratch.iterdir()):
        raise ValueError("scratch must be empty")
    manifest = json.loads((fixtures / "manifest.json").read_text())
    for member in manifest["files"]:
        if Path(member["path"]).name != member["path"]:
            raise ValueError("unsafe fixture member")
        path = fixtures / member["path"]
        if path.stat().st_size != member["bytes"] or sha(path) != member["sha256"]:
            raise ValueError("fixture hash mismatch")
    report = {"passed": True, "fixtureManifestSha256": sha(fixtures / "manifest.json"),
              "binaries": {name: sha(path) for name, path in (
                  ("ffmpeg", args.ffmpeg), ("ffprobe", args.ffprobe),
                  ("doviTool", args.dovi_tool), ("roundtrip", args.roundtrip_binary))},
              "layers": [], "scope": "real encoded fixture repackage; no production PID join, mastered FEL or target acceptance"}

    def facts(path):
        return json.loads(run([args.ffprobe, "-v", "error", "-show_streams", "-show_packets",
                               "-show_frames", "-of", "json", path]))

    def decode(path):
        raw = run([args.ffmpeg, "-v", "error", "-threads", "1", "-i", path,
                   "-map", "0:v:0", "-c:v", "rawvideo", "-f", "framemd5", "-"])
        # Compare actual pixel hashes, independent of raw input's absent clock.
        return [line.rsplit(",", 1)[1].strip() for line in raw.splitlines()
                if line and not line.startswith("#")]

    reference = decode(fixtures / "base-pq.hevc")
    if len(reference) != 24:
        raise ValueError("base decode incomplete")
    for layer in ("MEL", "FEL"):
        source, converted = (scratch / (prefix + layer + ".mp4") for prefix in ("source-", "converted-"))
        result = json.loads(run([args.roundtrip_binary, fixtures / f"joined-{layer}.hevc", source, converted]))
        before, after = facts(source), facts(converted)
        def packets(f):
            return [{k: p.get(k) for k in ("pts", "dts", "duration", "flags")}
                    for p in f["packets_and_frames"] if p["type"] == "packet"]
        bp, ap = packets(before), packets(after)
        if len(bp) != 24 or bp != ap:
            raise ValueError("MP4 packet clock/flags changed")
        if set(p["dts"] for p in bp) == set(p["pts"] for p in bp):
            raise ValueError("fixture failed to exercise B-frame reordering")
        pts = sorted(p["pts"] for p in bp)
        if len(set(pts[i + 1] - pts[i] for i in range(23))) != 2:
            raise ValueError("fixture failed to exercise variable presentation intervals")
        sd = [s for s in after["streams"][0].get("side_data_list", [])
              if s["side_data_type"] == "DOVI configuration record"]
        if len(sd) != 1 or any(sd[0][k] != v for k, v in {
                "dv_profile": 8, "dv_level": 1, "dv_bl_signal_compatibility_id": 1,
                "rpu_present_flag": 1, "el_present_flag": 0, "bl_present_flag": 1}.items()):
            raise ValueError("wrong delivered DV configuration")
        if after["streams"][0]["codec_tag_string"] != "dvh1":
            raise ValueError("wrong MP4 sample-entry signalling")
        if decode(source) != reference or decode(converted) != reference:
            raise ValueError("base pixels changed")
        raw = scratch / f"converted-{layer}.hevc"
        run([args.ffmpeg, "-v", "error", "-i", converted, "-map", "0:v:0", "-c:v", "copy",
             "-bsf:v", "hevc_mp4toannexb", "-f", "hevc", raw])
        units = nals(raw.read_bytes())
        types = [(n[0] >> 1) & 63 for n in units]
        if types.count(62) != 24 or 63 in types:
            raise ValueError("RPU count or EL drop wrong")
        original_vcl = [n for n in nals((fixtures / "base-pq.hevc").read_bytes()) if (n[0] >> 1) & 63 <= 31]
        if [n for n in units if (n[0] >> 1) & 63 <= 31] != original_vcl:
            raise ValueError("base coded picture NAL bytes changed")
        oracle = scratch / f"oracle-{layer}.hevc"
        run([args.dovi_tool, "-m", "2", "convert", "-d", "-i",
             fixtures / f"joined-{layer}.hevc", "-o", oracle])
        oracle_rpu, actual_rpu = (scratch / (name + layer + ".rpu") for name in ("oracle-", "actual-"))
        for video, rpu in ((oracle, oracle_rpu), (raw, actual_rpu)):
            run([args.dovi_tool, "extract-rpu", "-i", video, "-o", rpu])
        if oracle_rpu.read_bytes() != actual_rpu.read_bytes():
            raise ValueError("mode2 RPU differs from official CLI oracle")
        report["layers"].append({"layer": layer, "roundtrip": result, "packets": len(ap),
            "pts": pts, "rpuSha256": sha(actual_rpu), "officialMode2RpuIdentical": True,
            "baseVCLBytesIdentical": True, "baseDecodedFramesIdentical": True,
            "elNALs": 0, "rpuNALs": 24, "configuration": sd[0]})
    (scratch / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
