#!/usr/bin/env python3
"""Checks that the media engine builds the same FFmpeg as the Portico server.

Usage: check-ffmpeg-pin.py /path/to/server/third_party/ffmpeg/sources.lock.json

The server's lock is the reference: its version, tag, source URL and SHA-256 must all equal the
"ffmpeg" component in this package's sources.lock.json. Exits 1 (and says what differs) when they
do not, 2 when a file cannot be read.
"""
import json
import pathlib
import sys

HERE = pathlib.Path(__file__).resolve().parent


def main() -> int:
    if len(sys.argv) != 2:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    try:
        server = json.loads(pathlib.Path(sys.argv[1]).read_text())["ffmpeg"]
        ours = json.loads((HERE / "sources.lock.json").read_text())["components"]["ffmpeg"]
    except (OSError, KeyError, json.JSONDecodeError) as error:
        print(f"Could not read a lock file: {error}", file=sys.stderr)
        return 2
    pairs = [("version", "version"), ("tag", "tag"), ("source", "url"), ("sha256", "sha256")]
    differences = [
        f"  {theirs}: server {server.get(theirs)!r}, media engine {ours.get(mine)!r}"
        for theirs, mine in pairs
        if server.get(theirs) != ours.get(mine)
    ]
    if differences:
        print("The media engine's FFmpeg does not match the server's pin:", file=sys.stderr)
        print("\n".join(differences), file=sys.stderr)
        return 1
    print(f"FFmpeg {ours['version']} matches the server's pin ({ours['sha256']}).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
