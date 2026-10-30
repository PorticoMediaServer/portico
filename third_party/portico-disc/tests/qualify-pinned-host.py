#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Build the standalone host helper from immutable PUBLIC sources and probe fixtures.

Run inside a disposable remote job with cc/git/meson/ninja/pkg-config available.
No system installation, canonical FFmpeg recipe, licensed sample, decryption or
runtime availability claim. Pristine libraries may still have optional dlopen
paths: this test DOES NOT qualify the Hardware-owned no-decryption closure.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess

PINS = [
    ("libudfread", "0151d9d61e5f832664f502608d41a77c28846efa", "dc626520dcd53a22f727af3ee42c770e56c97a64fe3adb063799d8ab032fe551", []),
    ("libdvdread", "ce9bdb7775601f4e6b95136b875bc72a53c5ba7d", "d6cb0e9e560f51085556949a84af12b79a00f10ab8b66c752537faf7cd665572", ["-Dlibdvdcss=disabled"]),
    ("libdvdnav", "2ffc50b5c37a6ddc086829203fc44e95588198dd", "32b1062f7da84967e7019d01ab805935caa7ab7321a7ced0e30ebe75e5df1670", ["-Denable_examples=false"]),
    ("libbluray", "065247e5ef40ccf39857db81e2c1368354a23ef8", "b3aa400aca6d2ba1f0bd03bd98d03d1fe7489a3bbb26969d72016360af8a5c9d", ["-Dbdj_jar=disabled", "-Denable_tools=false", "-Dfontconfig=disabled", "-Dfreetype=disabled", "-Dlibxml2=disabled"]),
]


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--work", required=True, type=Path)
    parser.add_argument("--static", action="store_true", help="link the test executable statically for runner host compatibility")
    args = parser.parse_args()
    repo = Path(__file__).resolve().parents[3]
    work = args.work.resolve()
    work.relative_to(Path(os.environ["TMPDIR"]).resolve())
    work.mkdir(parents=True, exist_ok=False)
    for tool in ("cc", "git", "meson", "ninja", "pkg-config"):
        if not shutil.which(tool):
            raise RuntimeError("job-scoped build tool unavailable: " + tool)
    prefix = work / "prefix"
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    env.update({"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_TERMINAL_PROMPT": "0",
                "PKG_CONFIG_PATH": str(prefix / "lib/pkgconfig"), "PKG_CONFIG_LIBDIR": str(prefix / "lib/pkgconfig"),
                "SOURCE_DATE_EPOCH": "1761177600", "LC_ALL": "C"})

    def run(argv, capture=False, timeout=180, git_source=None):
        print(json.dumps({"command": [str(a) for a in argv]}), flush=True)
        command_env = env if git_source is None else {**env, "GIT_DIR": str(git_source / ".git"), "GIT_WORK_TREE": str(git_source)}
        result = subprocess.run([str(a) for a in argv], env=command_env, cwd=work, check=True,
                                stdout=subprocess.PIPE if capture else None, timeout=timeout)
        return result.stdout.decode() if capture else ""

    report = {"scope": "standalone host C/helper source and procedural fixture proof only; NOT HW1, no-decryption closure, full VS4, FEL reconstruction or remote provider support",
              "dependencies": [], "actualProbes": [], "compiler": run(["cc", "--version"], True).splitlines()[0]}
    for name, commit, license_hash, options in PINS:
        source, build = work / name, work / (name + "-build")
        run(["git", "init", "-q", source])
        origin = "https://code.videolan.org/videolan/" + name + ".git"
        run(["git", "-C", source, "-c", "credential.helper=", "fetch", "--depth=1", origin, commit])
        run(["git", "-C", source, "checkout", "--detach", "FETCH_HEAD"])
        if run(["git", "-C", source, "rev-parse", "HEAD"], True).strip() != commit or sha(source / "COPYING") != license_hash:
            raise ValueError("public source/license pin mismatch: " + name)
        run(["meson", "setup", build, source, "--prefix=" + str(prefix), "--libdir=lib",
             "--default-library=static", "--buildtype=release", "--wrap-mode=nodownload", "-Db_staticpic=true", *options], git_source=source)
        run(["meson", "compile", "-C", build, "-j1"], git_source=source)
        run(["meson", "install", "-C", build], git_source=source)
        report["dependencies"].append({"name": name, "origin": origin, "commit": commit, "licenseSha256": license_hash})
    flags = shlex.split(run(["pkg-config", "--cflags", "--libs", "--static", "libbluray", "dvdread", "dvdnav"], True))
    binary = work / "portico-disc"
    run(["cc", "-std=c11", "-O2", "-Wall", "-Wextra", "-Werror", *( ["-static"] if args.static else []),
         repo / "third_party/portico-disc/portico-disc.c", "-o", binary, *flags])
    report["helperSha256"] = sha(binary)
    report["sourceSha256"] = sha(repo / "third_party/portico-disc/portico-disc.c")
    report["headerSha256"] = sha(repo / "third_party/portico-disc/mpegts_dovi.h")
    report["staticArchives"] = {f.name: sha(f) for f in sorted((prefix / "lib").glob("*.a"))}
    psi = work / "psi-test"
    run(["cc", "-std=c11", "-O1", "-g", "-Wall", "-Wextra", "-Werror", "-fsanitize=address,undefined", "-fno-omit-frame-pointer",
         repo / "third_party/portico-disc/tests/mpegts_dovi_test.c", "-o", psi])
    report["ownedPsiSanitizer"] = json.loads(run([psi], True))
    transport = json.loads((repo / "fixtures/video/dovi-dual-pid/manifest.json").read_text())
    report["transportCases"] = []
    for f in transport["fixtures"]:
        expected = 0 if f["path"] in ("wrong-dependency.ts", "no-descriptor.ts", "removed-descriptor.ts", "private-missing-hevc.ts", "private-missing-registration.ts") else 4113
        report["transportCases"].append({"path": f["path"], "result": json.loads(run([psi, repo / "fixtures/video/dovi-dual-pid" / f["path"], "192" if f["path"].endswith(".m2ts") else "188", str(expected)], True))})
    for folder, role_facts in (("fixtures/discs", False), ("fixtures/discs/dovi", True)):
        inventory = json.loads((repo / folder / "manifest.json").read_text())
        for f in inventory["fixtures"]:
            root = repo / folder / f["path"]
            for member, expected in f["files"].items():
                if sha(root if member == "." else root / member) != expected:
                    raise ValueError("fixture member hash changed")
            actual = json.loads(run([binary, "probe", root], True, 20))
            compare = json.loads(json.dumps(actual))
            if role_facts:
                streams = compare["titles"][0]["streams"]
                if [s.get("videoRole") for s in streams] != ["primary", "enhancement"]:
                    raise ValueError("actual libbluray STN roles not retained")
            if compare != f["expected"]:
                raise ValueError("existing source facts changed: " + f["id"])
            report["actualProbes"].append({"id": f["id"], "probe": actual})
            if not role_facts and actual["kind"] == "bluray":
                seek = json.loads(run([binary, "seek", root, "1", "37500"], True, 20))
                if seek["titleTimeMs"] != 37000 or seek["requestedMs"] != 37500:
                    raise ValueError("actual cross-clip seek changed")
    report["passed"] = True
    print("PORTICO_DISC_HOST_RESULT=" + json.dumps(report), flush=True)


if __name__ == "__main__":
    main()
