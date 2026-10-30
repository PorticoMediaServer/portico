#!/usr/bin/env python3
"""Run real dual-PID MPEGTS join torture inputs against an exact tested build."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run(argv):
    p = subprocess.run([str(v) for v in argv], capture_output=True, timeout=30)
    if p.returncode:
        raise RuntimeError((p.stdout + p.stderr).decode(errors="replace")[-8192:])
    return p.stdout.decode(errors="replace")


def nals(path):
    data = path.read_bytes()
    starts = list(re.finditer(b"\x00\x00\x00\x01|\x00\x00\x01", data))
    if not starts or starts[0].start() != 0:
        raise ValueError("not Annex B")
    return [data[m.end():starts[i + 1].start() if i + 1 < len(starts) else len(data)]
            for i, m in enumerate(starts)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("fixtures", "hdr-fixtures", "scratch", "ffmpeg", "ffprobe", "test-binary"):
        parser.add_argument("--" + name, type=Path, required=True)
    args = parser.parse_args()
    fixtures, hdr, scratch = args.fixtures.resolve(), args.hdr_fixtures.resolve(), args.scratch.resolve()
    scratch.mkdir(parents=True, exist_ok=True)
    if any(scratch.iterdir()):
        raise ValueError("scratch must be empty")
    manifest = json.loads((fixtures / "manifest.json").read_text())
    report = {"passed": True, "fixtureManifestSha256": sha(fixtures / "manifest.json"),
              "binarySha256": sha(args.ffmpeg), "probeSha256": sha(args.ffprobe),
              "testBinarySha256": sha(args.test_binary), "fixtures": [],
              "scope": "host real dual-PID structural join, source clock, seek, decode and 8.1 conversion; not canonical target/disc activation or FEL residual reconstruction"}
    help_text = run([args.ffmpeg, "-hide_banner", "-h", "demuxer=mpegts"])
    if not re.search(r"(?m)^\s+-merge_dovi\s+<boolean>.*\(default false\)", help_text):
        raise ValueError("missing actual default-off join option")

    def pixels(path):
        text = run([args.ffmpeg, "-v", "error", "-threads", "1", "-i", path,
                    "-map", "0:v:0", "-c:v", "rawvideo", "-f", "framemd5", "-"])
        return [line.rsplit(",", 1)[1].strip() for line in text.splitlines()
                if line and not line.startswith("#")]

    reference = pixels(hdr / "base-pq.hevc")
    if len(reference) != 24:
        raise ValueError("reference frame count")
    for fixture in manifest["fixtures"]:
        name = fixture["path"]
        if Path(name).name != name:
            raise ValueError("unsafe fixture member")
        path = fixtures / name
        if path.stat().st_size != fixture["bytes"] or sha(path) != fixture["sha256"]:
            raise ValueError("fixture hash mismatch")
        joined = scratch / (name + ".hevc")
        result = json.loads(run([args.test_binary, path, joined, fixture["expected"]]))
        row = {"path": name, "result": result}
        if fixture["expected"] == "joined24":
            clocks = fixture["sourcePacketClocks"]
            if result["pts"] != [c["pts"] for c in clocks] or result["dts"] != [c["dts"] for c in clocks]:
                raise ValueError("actual source clock changed")
            # Compare every NAL byte in order with the official CLI mux oracle,
            # including all wrapped EL pictures and RPU, not just BL frames.
            if nals(joined) != nals(hdr / ("joined-" + fixture["layer"] + ".hevc")):
                raise ValueError("joined NALs differ from official mux oracle")
            if pixels(joined) != reference:
                raise ValueError("joined base pixels changed")
            converted = scratch / (name + ".mp4")
            run([args.ffmpeg, "-v", "error", "-xerror", "-copyts", "-merge_dovi", "1",
                 "-i", path, "-map", "0:v:0", "-c:v", "copy", "-bsf:v", "dovi_profile=mode=2",
                 "-tag:v", "dvh1", "-strict", "unofficial", converted])
            facts = json.loads(run([args.ffprobe, "-v", "error", "-show_streams", "-show_packets",
                                    "-of", "json", converted]))
            if len(facts["streams"]) != 1 or len(facts["packets"]) != 24:
                raise ValueError("converted packet count")
            stream = facts["streams"][0]
            config = [d for d in stream.get("side_data_list", []) if d["side_data_type"] == "DOVI configuration record"]
            if (stream["codec_tag_string"] != "dvh1" or stream["time_base"] != "1/90000" or
                    len(config) != 1 or any(config[0][k] != v for k, v in {
                        "dv_profile": 8, "dv_bl_signal_compatibility_id": 1, "el_present_flag": 0,
                        "bl_present_flag": 1, "rpu_present_flag": 1}.items())):
                raise ValueError("converted sample entry/configuration")
            # MOV may apply an edit-list origin. Preserve every relative PTS/DTS
            # together, never hide a different per-stream or per-frame shift.
            shift = facts["packets"][0]["pts"] - clocks[0]["pts"]
            if any(p["pts"] - c["pts"] != shift or p["dts"] - c["dts"] != shift
                   for p, c in zip(facts["packets"], clocks)):
                raise ValueError("repackage source timing changed")
            if pixels(converted) != reference:
                raise ValueError("converted base pixels changed")
            converted_raw = scratch / (name + ".converted.hevc")
            run([args.ffmpeg, "-v", "error", "-i", converted, "-map", "0:v:0", "-c:v", "copy",
                 "-bsf:v", "hevc_mp4toannexb", "-f", "hevc", converted_raw])
            expected_rpu = [n for n in nals(hdr / "dv-8.1.hevc") if (n[0] >> 1) & 63 == 62]
            delivered_nals = nals(converted_raw)
            if ([n for n in delivered_nals if (n[0] >> 1) & 63 == 62] != expected_rpu or
                    any((n[0] >> 1) & 63 == 63 for n in delivered_nals)):
                raise ValueError("delivered mode2 RPU/EL differs from official generated8.1 oracle")
            row.update({"allNALBytesMatchOfficialMux": True, "baseDecodedFramesIdentical": True,
                        "convertedPackets": 24, "mp4OriginShift90k": shift, "config": config[0],
                        "deliveredRpuMatchesOfficial81": True, "deliveredElNALs": 0})
        report["fixtures"].append(row)
    report["joinedCases"] = sum(f["expected"] == "joined24" for f in manifest["fixtures"])
    report["refusalCases"] = sum(f["expected"] == "refused" for f in manifest["fixtures"])
    (scratch / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
