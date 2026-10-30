#!/usr/bin/env bash
# Gathers the licence texts of every component a platform build contains and the corresponding
# source bundle a release must ship (or offer) beside the app. The build scripts run it at the
# end; it can also be run on its own after a build:
#
#   ./collect-notices.sh android
#
# Writes, under out/<platform>/:
#   notices/THIRD-PARTY-NOTICES.txt   one file with every component's notice and licence text
#   notices/<component>/...           the same licence files, one folder per component
#   source/portico-media-engine-<platform>-source-r<revision>.tar
#                                     the pinned source archives exactly as downloaded (their
#                                     SHA-256 sums are in sources.lock.json), libdovi's vendored
#                                     Rust dependencies, this recipe and its patches
#   source/SHA256SUMS
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/common.sh"

PLATFORM="${1:-}"
[[ "$PLATFORM" == android ]] || me_die "Usage: collect-notices.sh android"
REVISION=$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["revision"])' "$ME_LOCK")
NOTICES="$ME_OUT/$PLATFORM/notices"
SOURCE="$ME_OUT/$PLATFORM/source"
TMP="$ME_WORK/notices-tmp"
rm -rf "$NOTICES" "$SOURCE" "$TMP"
mkdir -p "$NOTICES" "$SOURCE" "$TMP"

me_fetch_all "$PLATFORM"

# 1. Licence files, straight from the pinned archives.
python3 -I - "$ME_LOCK" "$PLATFORM" <<'EOF' > "$TMP/licences.txt"
import json, sys
data = json.load(open(sys.argv[1]))
for name, entry in data["components"].items():
    if sys.argv[2] not in entry["platforms"]:
        continue
    print(name, entry["file"], ".", " ".join(entry["licenceFiles"]))
    for path, sub in entry.get("subsources", {}).items():
        print(name + "/" + path.split("/")[-1], sub["file"], path, " ".join(sub["licenceFiles"]))
EOF
while read -r label archive _ files; do
    work="$TMP/x/$label"
    mkdir -p "$work" "$NOTICES/$label"
    tar -xf "$ME_DOWNLOADS/$archive" -C "$work" --strip-components=1
    for f in $files; do
        [[ -f "$work/$f" ]] || me_die "$label has no licence file $f (update licenceFiles in sources.lock.json)."
        mkdir -p "$NOTICES/$label/$(dirname "$f")"
        cp "$work/$f" "$NOTICES/$label/$f"
    done
    rm -rf "$work"
done < "$TMP/licences.txt"

# libdovi's Rust dependencies are compiled into libdovi; their licences travel with it.
VENDOR="$ME_WORK/src/libdovi/vendor"
if [[ -d "$VENDOR" ]]; then
    for crate in "$VENDOR"/*/; do
        name=$(basename "$crate")
        mkdir -p "$NOTICES/libdovi/crates/$name"
        found=0
        for f in "$crate"LICENSE* "$crate"LICENCE* "$crate"COPYING* "$crate"UNLICENSE*; do
            [[ -f "$f" ]] && { cp "$f" "$NOTICES/libdovi/crates/$name/"; found=1; }
        done
        # A crate that ships no licence file still names its licence in Cargo.toml.
        (( found )) || grep -m1 '^license *=' "$crate/Cargo.toml" > "$NOTICES/libdovi/crates/$name/LICENSE-NAME" || true
    done
else
    me_die "libdovi's vendored crates are missing ($VENDOR); run the build first."
fi

# 2. The combined notice.
python3 -I - "$ME_LOCK" "$PLATFORM" "$NOTICES" "$REVISION" <<'EOF'
import json, sys, pathlib
lock = json.load(open(sys.argv[1])); platform = sys.argv[2]; root = pathlib.Path(sys.argv[3]); rev = sys.argv[4]
lgpl = [n for n, e in lock["components"].items() if platform in e["platforms"] and "LGPL" in e["licence"]]
out = []
out.append("Portico media engine: third-party notices")
out.append("=" * 41)
out.append("")
out.append(f"Built from sources.lock.json revision {rev} for {platform}.")
out.append("")
out.append("The mpv player engine in this app is built from the open-source components below. Each is")
out.append("a separate shared library (lib*.so files in the Android app package).")
out.append("")
out.append("The components licensed under the GNU Lesser General Public License, version 2.1 or (at")
out.append("your option) any later version, are: " + ", ".join(lgpl) + ".")
out.append("You may modify them and replace the libraries in the app with your own builds. Their complete")
out.append("corresponding source code, with the exact build recipe and patches used, is published with each")
out.append(f"release as portico-media-engine-{platform}-source-r{rev}.tar, from the same place as the release.")
out.append("")
for name, entry in lock["components"].items():
    if platform not in entry["platforms"]:
        continue
    out.append("-" * 78)
    out.append(f"{name} {entry['version']}")
    out.append(f"Licence: {entry['licence']}")
    out.append(f"Source: {entry['url']}")
    if entry.get("prebuilt"):
        out.append("Used as the project's own release build.")
    if entry.get("linkedInto"):
        out.append(f"Linked into: {entry['linkedInto']}")
    out.append("")
    folder = root / name
    for f in sorted(p for p in folder.rglob("*") if p.is_file()):
        out.append(f"--- {f.relative_to(root)} ---")
        out.append(f.read_text(errors="replace").rstrip())
        out.append("")
(root / "THIRD-PARTY-NOTICES.txt").write_text("\n".join(out) + "\n")
EOF

# 3. The corresponding source bundle.
STAGE="$TMP/source/portico-media-engine-$PLATFORM-source-r$REVISION"
mkdir -p "$STAGE/archives" "$STAGE/recipe"
python3 -I - "$ME_LOCK" "$PLATFORM" <<'EOF' > "$TMP/archives.txt"
import json, sys
for name, entry in json.load(open(sys.argv[1]))["components"].items():
    if sys.argv[2] not in entry["platforms"] or entry.get("prebuilt"):
        continue
    print(entry["file"])
    for sub in entry.get("subsources", {}).values():
        print(sub["file"])
EOF
while read -r f; do cp "$ME_DOWNLOADS/$f" "$STAGE/archives/"; done < "$TMP/archives.txt"
tar -cf "$STAGE/archives/libdovi-vendored-crates.tar" -C "$ME_WORK/src/libdovi" vendor
( cd "$ME_ROOT" && tar -cf - --exclude=./work --exclude=./out --exclude='./apple/Frameworks' . ) | tar -xf - -C "$STAGE/recipe"
cat > "$STAGE/README.txt" <<EOF
Corresponding source for the Portico media engine ($PLATFORM), sources.lock.json revision $REVISION.

archives/   the upstream source archives exactly as downloaded; their SHA-256 sums are in
            recipe/sources.lock.json. libdovi-vendored-crates.tar holds libdovi's Rust
            dependencies as pinned by its Cargo.lock.
recipe/     the build recipe: build-android.sh, lib/common.sh, patches/, and
            README.md, which explains how to build and how to replace a library.

To rebuild offline: copy everything in archives/ (the crates tar included) into
recipe/work/downloads/ and run recipe/build-android.sh.
EOF
tar -cf "$SOURCE/portico-media-engine-$PLATFORM-source-r$REVISION.tar" -C "$TMP/source" "portico-media-engine-$PLATFORM-source-r$REVISION"
( cd "$SOURCE" && me_sha256 "portico-media-engine-$PLATFORM-source-r$REVISION.tar" | sed "s/\$/  portico-media-engine-$PLATFORM-source-r$REVISION.tar/" > SHA256SUMS )
rm -rf "$TMP"
me_say "Notices: $NOTICES/THIRD-PARTY-NOTICES.txt"
me_say "Source bundle: $SOURCE/portico-media-engine-$PLATFORM-source-r$REVISION.tar"
