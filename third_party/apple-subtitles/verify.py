"""Fail closed on the Apple subtitle library's actual built feature and dependency surface."""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

out = Path(sys.argv[1])
recipe = Path(__file__).resolve().parent
lock = json.loads((recipe / 'sources.lock.json').read_text())
source_lock = json.loads((recipe / lock['components']['ffmpeg']['sourceLock']).read_text())
ffmpeg = source_lock['ffmpeg']
if (ffmpeg['distributionVersion'] != '8.1.3-1' or len(ffmpeg['patches']) != 100
        or ffmpeg['sha256'] != lock['components']['ffmpeg']['sha256']
        or ffmpeg['license'] != 'GPL-3.0-or-later'):
    raise SystemExit('Incorrect checked Jellyfin source lock')
expected_receipt = {'sourceCommit': ffmpeg['commit'], 'sourceSha256': ffmpeg['sha256'],
                    'patches': ffmpeg['patches'], 'sourceDateEpoch': source_lock['sourceDateEpoch']}
stems = {"avcodec", "avformat", "avutil", "swresample", "porticosubtitle", "ass", "freetype", "harfbuzz", "fribidi", "unibreak"}
expected = {"Lib" + s for s in stems}
for config in sorted((out / "meta").glob("*/ffmpeg-config_components.h")):
    text = config.read_text()
    for required in ["PGSSUB_DECODER", "DVDSUB_DECODER", "DVBSUB_DECODER", "OPUS_DECODER", "SUP_DEMUXER", "VOBSUB_DEMUXER", "MATROSKA_DEMUXER", "OGG_DEMUXER"]:
        if not re.search(r"#define CONFIG_" + required + r" 1\b", text):
            raise SystemExit(f"Missing {required}: {config}")
    if re.search(r"#define CONFIG_(H264_DECODER|HEVC_DECODER|HTTP_PROTOCOL|HTTPS_PROTOCOL|LIBFDK_AAC_DECODER|LIBFDK_AAC_ENCODER) 1\b", text):
        raise SystemExit(f"Unexpected video/network/nonfree feature: {config}")
    general = config.with_name('ffmpeg-config.h').read_text()
    for required in ['GPL', 'VERSION3', 'GPLV3']:
        if not re.search(r'#define CONFIG_' + required + r' 1\b', general):
            raise SystemExit(f'Missing GPLv3 configuration: {config}')
    if re.search(r'#define CONFIG_NONFREE 1\b', general):
        raise SystemExit(f'Nonfree configuration: {config}')
    if json.loads(config.with_name('portico-source-receipt.json').read_text()) != expected_receipt:
        raise SystemExit(f'Incorrect Jellyfin patch receipt: {config}')
configs = list((out / "meta").glob("*/ffmpeg-config_components.h"))
if {c.parent.name for c in configs} != {'ios-arm64', 'ios-simulator-arm64', 'ios-simulator-x86_64', 'tvos-arm64', 'tvos-simulator-arm64'}:
    raise SystemExit('Expected configuration and patch receipts for all five Apple targets')
manifest = []
for xc in sorted((out / "xcframeworks").glob("*.xcframework")):
    if xc.stem not in expected:
        raise SystemExit(f"Unexpected framework {xc}")
    for binary in xc.glob("*/*.framework/" + xc.stem):
        links = subprocess.check_output(["otool", "-L", str(binary)], text=True)
        for line in links.splitlines():
            if not line[:1].isspace():
                continue
            dep = line.strip().split(" ")[0]
            if dep.startswith(("/System/Library/", "/usr/lib/")):
                continue
            if not any(dep == f"@rpath/{name}.framework/{name}" for name in expected):
                raise SystemExit(f"Unexpected dependency {dep}: {binary}")
        manifest.append({"path": str(binary.relative_to(out)), "sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "bytes": binary.stat().st_size})
if {xc.stem for xc in (out / "xcframeworks").glob("*.xcframework")} != expected or len(manifest) != 40:
    raise SystemExit("Missing framework slice")
if not (out / "notices/THIRD-PARTY-NOTICES.txt").is_file() or not (out / "source/SHA256SUMS").is_file():
    raise SystemExit("Missing licence/source bundle")
if json.loads((out / 'source/portico-source-receipt.json').read_text()) != expected_receipt:
    raise SystemExit('Missing corresponding-source patch receipt')
(out / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
print(f"Verified {len(manifest)} subtitle framework slices")
