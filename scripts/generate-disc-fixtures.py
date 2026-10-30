#!/usr/bin/env python3
"""Author mathematical disc fixtures, then record real helper output and hashes.

No input media is read. Tools are supplied explicitly; a system FFmpeg is never
described as the qualified Portico bundle. Output must be a new directory.
The committed fixtures are GPL-3.0-or-later, like the repository.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import struct
import subprocess
import tempfile
import xml.etree.ElementTree as ET


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        while data := f.read(1024 * 1024):
            h.update(data)
    return h.hexdigest()


def udf_revision(path):
    """Read the real anchor/LVD domain suffix, independently of its filename."""
    with path.open("rb") as f:
        f.seek(256 * 2048)
        anchor = f.read(2048)
        if len(anchor) != 2048 or struct.unpack_from("<H", anchor)[0] != 2:
            raise ValueError("missing UDF anchor")
        size, start = struct.unpack_from("<II", anchor, 16)
        if size > 1024 * 1024 or size % 2048:
            raise ValueError("invalid UDF descriptor extent")
        for block in range(start, start + size // 2048):
            f.seek(block * 2048)
            data = f.read(2048)
            if len(data) != 2048:
                raise ValueError("truncated UDF descriptor")
            if struct.unpack_from("<H", data)[0] == 6:
                if data[217:240].rstrip(b"\0") != b"*OSTA UDF Compliant":
                    raise ValueError("invalid UDF domain")
                return struct.unpack_from("<H", data, 240)[0]
    raise ValueError("missing logical volume descriptor")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    for name in ("ffmpeg", "tsmuxer", "dvdauthor", "helper"):
        parser.add_argument("--" + name, required=True, type=Path)
    args = parser.parse_args()
    tools = {k: getattr(args, k).resolve(strict=True) for k in
             ("ffmpeg", "tsmuxer", "dvdauthor", "helper")}
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    env = {**os.environ, "TMPDIR": str(output), "VIDEO_FORMAT": "PAL"}
    with (output / "generation.log").open("wb") as log:
        def run(command):
            subprocess.run([str(x) for x in command], env=env, stdout=log,
                           stderr=log, check=True, timeout=180)

        ff = [tools["ffmpeg"], "-nostdin", "-hide_banner", "-loglevel", "warning", "-y"]
        with tempfile.TemporaryDirectory(prefix="author-", dir=output) as scratch:
            work = Path(scratch)
            for title, seconds in ((1, 72), (2, 66)):
                run(ff + ["-f", "lavfi", "-i", "testsrc2=size=352x288:rate=25",
                          "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
                          "-t", str(seconds), "-target", "pal-dvd", "-threads", "1",
                          "-b:v", "600k", "-maxrate", "2000k", "-bufsize", "1835008",
                          "-g", "12", "-b:a", "128k", "-ac", "2", work / f"title{title}.mpg"])
            dvd = output / "dvd-two-title-video-ts"
            doc = ET.Element("dvdauthor", dest=str(dvd))
            ET.SubElement(doc, "vmgm")
            titles = ET.SubElement(ET.SubElement(doc, "titleset"), "titles")
            ET.SubElement(titles, "video", format="pal", aspect="4:3")
            ET.SubElement(titles, "audio", format="ac3", lang="en")
            for title, chapter in ((1, 36), (2, 33)):
                ET.SubElement(ET.SubElement(titles, "pgc"), "vob",
                              file=str(work / f"title{title}.mpg"), chapters=f"0,{chapter}")
            xml = work / "dvd.xml"
            ET.ElementTree(doc).write(xml, encoding="unicode")
            run([tools["dvdauthor"], "-x", xml])
            run(ff + ["-f", "lavfi", "-i", "color=c=blue:s=1920x1080:r=24", "-t", "72",
                      "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast",
                      "-pix_fmt", "yuv420p", "-profile:v", "high", "-level:v", "4.1",
                      "-g", "24", "-keyint_min", "24", "-sc_threshold", "0",
                      "-x264-params", "aud=1:repeat-headers=1", "-f", "h264", work / "bd.h264"])
            run(ff + ["-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "72",
                      "-c:a", "ac3", "-threads", "1", "-b:a", "128k", "-ac", "2", work / "bd.ac3"])
            meta = work / "bd.meta"
            meta.write_text("MUXOPT --blu-ray --vbr --split-duration=36 "
                            "--custom-chapters=00:00:00.000;00:00:36.000 --mplsOffset=1 "
                            "--m2tsOffset=1 --constant-iso-hdr\n"
                            f'V_MPEG4/ISO/AVC, "{work / "bd.h264"}", fps=24, insertSEI, contSPS\n'
                            f'A_AC3, "{work / "bd.ac3"}", lang=eng\n')
            for name in ("bluray-two-clip-bdmv", "bluray-udf250.iso"):
                run([tools["tsmuxer"], meta, output / name])
        if udf_revision(output / "bluray-udf250.iso") != 0x0250:
            raise ValueError("author did not create UDF 2.50")
        fixtures = []
        for ident, name in (("bluray-two-clip-bdmv", "bluray-two-clip-bdmv"),
                            ("dvd-two-title-video-ts", "dvd-two-title-video-ts"),
                            ("bluray-udf250-iso", "bluray-udf250.iso")):
            path = output / name
            result = subprocess.run([str(tools["helper"]), "probe", str(path)],
                                    env=env, capture_output=True, timeout=20, check=True)
            if len(result.stdout) > 1024 * 1024:
                raise ValueError("oversized helper output")
            expected = json.loads(result.stdout)
            if expected.get("schema") != 1 or expected.get("encryption") != "none":
                raise ValueError("generated fixture did not probe as unencrypted")
            titles = expected["titles"]
            if ident == "dvd-two-title-video-ts":
                if len(titles) != 2 or any(len(t["chapters"]) != 2 for t in titles):
                    raise ValueError("DVD did not contain two chaptered titles")
            elif len(titles) != 1 or len(titles[0]["clips"]) != 2:
                raise ValueError("Blu-ray did not contain a two-clip playlist")
            members = sorted(p for p in path.rglob("*") if p.is_file()) if path.is_dir() else [path]
            files = {str(p.relative_to(path)) if path.is_dir() else ".": digest(p) for p in members}
            fixtures.append({"id": ident, "path": name, "files": files, "expected": expected})
        (output / "manifest.json").write_text(json.dumps({"schema": 1, "fixtures": fixtures}, indent=2) + "\n")
        provenance = {"licence": "GPL-3.0-or-later", "input": "generated mathematical pictures and sine tone only",
                      "tools": {name: {"name": p.name, "sha256": digest(p)} for name, p in tools.items()},
                      "udfRevision": "2.50", "claim": "fixture generation and observed helper facts only; no HW1/runtime acceptance"}
        (output / "provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")


if __name__ == "__main__":
    main()
