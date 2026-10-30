#!/usr/bin/env python3
"""Generate small mathematical HDR video inputs using explicitly supplied tools.

No download or private media is used. Metadata statistics are declared synthetic
values, not measured pixel statistics. FEL is a structural join fixture, not a
mastered residual image or proof of FEL reconstruction.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
FRAMES = 24
LIMIT = 1024 * 1024
DOVI_COMMIT = "26ed61ff36f946313889a09c0d2da202e9ceb97a"
HDR_COMMIT = "faa437e52b317d08f5d0df43278640623d318479"


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run(argv):
    result = subprocess.run([str(x) for x in argv], capture_output=True, timeout=120)
    if result.returncode:
        raise RuntimeError(f"{argv[0]} exited {result.returncode}: "
                           + (result.stdout + result.stderr).decode(errors="replace")[-8192:])
    return result.stdout.decode(errors="replace")


def nals(data):
    starts = list(re.finditer(b"\x00\x00\x00\x01|\x00\x00\x01", data))
    if not starts or starts[0].start() != 0:
        raise ValueError("expected bounded Annex B HEVC")
    units = [data[m.end():starts[i + 1].start() if i + 1 < len(starts) else len(data)]
             for i, m in enumerate(starts)]
    if any(len(n) < 2 for n in units):
        raise ValueError("short HEVC NAL")
    return units


def hdr_metadata():
    records = []
    for frame in range(FRAMES):
        scene = frame // 12
        records.append({
            "BezierCurveData": {"Anchors": [128, 256, 384, 512, 640, 768, 896],
                                "KneePointX": 2048, "KneePointY": 2048},
            "LuminanceParameters": {
                "AverageRGB": 1000 + scene * 50, "MaxScl": [10000, 9500, 9000],
                "LuminanceDistributions": {
                    "DistributionIndex": [1, 5, 10, 25, 50, 75, 90, 95, 99],
                    "DistributionValues": [v + scene * 50 for v in
                                           [100, 200, 400, 1000, 2000, 3000, 5000, 7500, 9000]]}},
            "NumberOfWindows": 1, "TargetedSystemDisplayMaximumLuminance": 1000,
            "SceneFrameIndex": frame % 12, "SceneId": scene, "SequenceFrameIndex": frame})
    return {"JSONInfo": {"HDR10plusProfile": "B", "Version": "1.0"},
            "SceneInfo": records,
            "SceneInfoSummary": {"SceneFirstFrameIndex": [0, 12], "SceneFrameNumbers": [12, 12]},
            "ToolInfo": {"Tool": "Portico procedural fixture", "Version": "1"}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ("encoder", "ffmpeg", "ffprobe", "dovi-tool", "hdr10plus-tool", "output"):
        parser.add_argument("--" + flag, type=Path, required=True)
    args = parser.parse_args()
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    # Never overwrite an existing fixture set or arbitrary files.
    if any(out.iterdir()):
        raise ValueError("output must be an empty directory")
    tool_versions = {}
    for name, tool, version_flag, expected in (
        ("encoder", args.encoder, "-version", "ffmpeg version"),
        ("ffmpeg", args.ffmpeg, "-version", "ffmpeg version"),
        ("ffprobe", args.ffprobe, "-version", "ffprobe version"),
        ("doviTool", args.dovi_tool, "--version", "dovi_tool 2.3.4"),
        ("hdr10plusTool", args.hdr10plus_tool, "--version", "hdr10plus_tool 1.7.2")):
        version = run([tool, version_flag]).splitlines()[0]
        if not version.startswith(expected):
            raise ValueError(f"unexpected {name} version: {version}")
        tool_versions[name] = {"version": version, "sha256": digest(tool)}
    commands = []
    aliases = {str(out): "${OUTPUT}", str(ROOT): "${REPO}",
               str(args.encoder): "${ENCODER}", str(args.ffmpeg): "${FFMPEG}",
               str(args.ffprobe): "${FFPROBE}", str(args.dovi_tool): "${DOVI_TOOL}",
               str(args.hdr10plus_tool): "${HDR10PLUS_TOOL}"}

    def execute(argv):
        command = []
        for arg in argv:
            value = str(arg)
            for path, alias in aliases.items():
                if value == path or value.startswith(path + "/"):
                    value = alias + value[len(path):]
                    break
            command.append(value)
        commands.append(command)
        return run(argv)

    def encode(name, source, transfer):
        params = ("pools=none:frame-threads=1:wpp=0:bframes=2:b-adapt=0:keyint=12:"
                  "min-keyint=12:scenecut=0:repeat-headers=1:aud=1:colorprim=9:"
                  f"transfer={transfer}:colormatrix=9")
        if transfer == 16:
            params += (":hdr10=1:master-display=G(13250,34500)B(7500,3000)R(34000,16000)"
                       "WP(15635,16450)L(10000000,1):max-cll=1000,400")
        execute([args.encoder, "-v", "error", "-nostdin", "-filter_threads", "1",
                 "-threads", "1", "-f", "lavfi", "-i", source, "-frames:v", str(FRAMES),
                 "-an", "-c:v", "libx265", "-pix_fmt", "yuv420p10le", "-x265-params",
                 params, "-f", "hevc", out / name])

    encode("base-pq.hevc", "testsrc2=s=320x180:r=24", 16)
    encode("base-hlg.hevc", "testsrc2=s=320x180:r=24", 18)
    encode("enhancement.hevc", "color=gray:s=160x90:r=24", 16)
    for profile in ("8.1", "8.4"):
        config = {"cm_version": "V40", "profile": profile, "length": FRAMES,
                  "level6": None, "shots": [{"start": 0, "duration": FRAMES}]}
        config_path = out / ("dv-" + profile + ".json")
        config_path.write_text(json.dumps(config, indent=2) + "\n")
        rpu = out / ("dv-" + profile + ".rpu")
        execute([args.dovi_tool, "generate", "-j", config_path, "-o", rpu])
        base = "base-pq.hevc" if profile == "8.1" else "base-hlg.hevc"
        execute([args.dovi_tool, "inject-rpu", "-i", out / base, "-r", rpu,
                 "-o", out / ("dv-" + profile + ".hevc")])
    for layer in ("MEL", "FEL"):
        # Library fixtures contain HEVC UNSPEC62 headers. The official CLI RPU
        # file format omits those two bytes, as GenerateConfig::write_rpus does.
        source = ROOT / "fixtures/video/dovi-metadata" / f"dv-7-{layer}-V40.rpu"
        units = nals(source.read_bytes())
        if len(units) != FRAMES or any(n[:2] != b"\x7c\x01" for n in units):
            raise ValueError("unexpected canonical profile 7 metadata fixture")
        rpu = out / f"dv-7-{layer}.rpu"
        rpu.write_bytes(b"".join(b"\0\0\0\1" + n[2:] for n in units))
        el = out / f"enhancement-{layer}.hevc"
        execute([args.dovi_tool, "inject-rpu", "-i", out / "enhancement.hevc", "-r", rpu,
                 "-o", el])
        joined = out / f"joined-{layer}.hevc"
        execute([args.dovi_tool, "mux", "--bl", out / "base-pq.hevc", "--el", el, "-o", joined])
        retained = [n for n in nals(joined.read_bytes()) if (n[0] >> 1) & 63 not in (62, 63)]
        if retained != nals((out / "base-pq.hevc").read_bytes()):
            raise ValueError("joined base NAL bytes changed")
    metadata = hdr_metadata()
    metadata_path = out / "hdr10plus.json"
    metadata_path.write_text(json.dumps(metadata, indent=2) + "\n")
    execute([args.hdr10plus_tool, "inject", "-i", out / "base-pq.hevc", "-j", metadata_path,
             "-o", out / "hdr10plus.hevc"])
    extracted = out / "hdr10plus-extracted.json"
    execute([args.hdr10plus_tool, "extract", "-i", out / "hdr10plus.hevc", "-o", extracted])
    parsed = json.loads(extracted.read_text())
    if any(metadata[k] != parsed[k] for k in ("SceneInfo", "SceneInfoSummary")):
        raise ValueError("HDR10+ metadata round-trip mismatch")
    frame_hashes, stream_facts = {}, {}
    for path in sorted(out.glob("*.hevc")):
        if path.stat().st_size > LIMIT:
            raise ValueError("fixture exceeds 1MiB")
        facts = json.loads(execute([args.ffprobe, "-v", "error", "-count_frames", "-show_streams",
                                   "-of", "json", path]))["streams"]
        if len(facts) != 1 or int(facts[0]["nb_read_frames"]) != FRAMES:
            raise ValueError("fixture does not decode exactly 24 frames")
        expected_transfer = "arib-std-b67" if path.name in ("base-hlg.hevc", "dv-8.4.hevc") else "smpte2084"
        expected_size = (160, 90) if path.name.startswith("enhancement") else (320, 180)
        if (facts[0].get("color_transfer") != expected_transfer or
                (facts[0].get("width"), facts[0].get("height")) != expected_size or
                facts[0].get("pix_fmt") != "yuv420p10le" or facts[0].get("has_b_frames", 0) <= 0):
            raise ValueError("actual encoded picture facts differ from recipe")
        stream_facts[path.name] = {k: facts[0][k] for k in
                                  ("codec_name", "width", "height", "pix_fmt", "color_transfer", "has_b_frames")}
        decoded = execute([args.ffmpeg, "-v", "error", "-threads", "1", "-i", path,
                           "-map", "0:v:0", "-c:v", "rawvideo", "-f", "framemd5", "-"])
        rows = [line for line in decoded.splitlines() if line and not line.startswith("#")]
        frame_hashes[path.name] = hashlib.sha256("\n".join(rows).encode()).hexdigest()
    for name in ("joined-MEL.hevc", "joined-FEL.hevc", "dv-8.1.hevc", "hdr10plus.hevc"):
        if frame_hashes[name] != frame_hashes["base-pq.hevc"]:
            raise ValueError(f"base picture changed in {name}")
    if frame_hashes["dv-8.4.hevc"] != frame_hashes["base-hlg.hevc"]:
        raise ValueError("HLG base picture changed")
    # Container-less Annex B has no authoritative PTS/DTS timeline. The 24-frame
    # recipe and encoded VUI rate do not pretend to prove a container duration.
    frames = json.loads(execute([args.ffprobe, "-v", "error", "-show_frames", "-of", "json",
                                out / "hdr10plus.hevc"]))["frames"]
    if len(frames) != FRAMES or not all(any(sd["side_data_type"] ==
            "HDR Dynamic Metadata SMPTE2094-40 (HDR10+)" for sd in f.get("side_data_list", []))
            for f in frames):
        raise ValueError("HDR10+ is not present on all decoded frames")
    files = [{"path": p.name, "bytes": p.stat().st_size, "sha256": digest(p)}
             for p in sorted(out.iterdir()) if p.is_file()]
    manifest = {"schema": 1, "license": "GPL-3.0-only", "frames": FRAMES,
                "source": "mathematical testsrc2 / constant gray; no supplied licensed media",
                "scope": "structural BL/EL join oracle and base decode; no mastered FEL residual reconstruction",
                "unknown": ["physical display output", "container timeline", "profile 5 image encoding"],
                "metadata": "synthetically declared; not measured mastering or luminance statistics",
                "tools": tool_versions, "dependencies": {"doviToolCommit": DOVI_COMMIT,
                "hdr10plusToolCommit": HDR_COMMIT}, "files": files, "decodedFrameHashes": frame_hashes,
                "encodedFacts": stream_facts, "commands": commands}
    (out / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(json.dumps({"passed": True, "videoFiles": len(frame_hashes), "framesEach": FRAMES,
                      "hdr10PlusFrames": len(frames), "manifestSha256": digest(out / "manifest.json")}))


if __name__ == "__main__":
    main()
