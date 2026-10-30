#!/usr/bin/env python3
"""Check the vendored libass WebAssembly files against provenance.json.

Reads the pinned archive as data (nothing in it is run): the archive must have the recorded
digest, and every file kept in this folder must be byte-identical to its member of the archive.
Exit status 0 when everything matches.
"""
import hashlib
import json
import pathlib
import sys
import tarfile

here = pathlib.Path(__file__).resolve().parent
record = json.loads((here / "provenance.json").read_text())
problems = []

archive = here / record["artifact"]
if hashlib.sha256(archive.read_bytes()).hexdigest() != record["artifactSha256"]:
    problems.append("archive digest differs from provenance.json")

with tarfile.open(archive, "r:gz") as tar:
    for kept, member in record["archiveMembers"].items():
        handle = tar.extractfile(member)
        original = handle.read() if handle else b""
        ours = (here / kept).read_bytes()
        if ours != original:
            problems.append(f"{kept} differs from {member} in the archive")
        if hashlib.sha256(ours).hexdigest() != record["distSha256"][kept]:
            problems.append(f"{kept} digest differs from provenance.json")

for line in problems:
    print(line)
print("ok" if not problems else "FAILED")
sys.exit(1 if problems else 0)
