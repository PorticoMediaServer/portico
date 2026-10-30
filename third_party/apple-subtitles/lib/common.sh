# Apple subtitle recipe helpers. AVPlayer owns playback; this recipe has no player engine.
set -euo pipefail
ME_NEUTRAL_WORK=/portico-apple-subtitles
ME_NEUTRAL_TOOLCHAIN=/toolchain

me_say() { printf '\033[1m[apple-subtitles]\033[0m %s\n' "$*" >&2; }
me_die() { printf '\033[1;31m[apple-subtitles] error:\033[0m %s\n' "$*" >&2; exit 1; }

me_lock() {
    python3 -I - "$ME_LOCK" "$@" <<'EOF'
import json, sys
data = json.load(open(sys.argv[1]))
node = data['tools'] if sys.argv[2] == 'tool' else data['components']
keys = sys.argv[3:] if sys.argv[2] == 'tool' else sys.argv[2:]
for key in keys:
    node = node.get(key, {}) if isinstance(node, dict) else {}
print(node if isinstance(node, str) else ('' if node == {} else json.dumps(node)))
EOF
}

me_components() {
    python3 -I - "$ME_LOCK" "$1" <<'EOF'
import json, sys
for name, entry in json.load(open(sys.argv[1]))['components'].items():
    if sys.argv[2] in entry['platforms']:
        print(name)
EOF
}

me_sha256() {
    if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

me_fetch() {
    local file="$1" url="$2" sum="$3" path="$ME_DOWNLOADS/$1"
    mkdir -p "$ME_DOWNLOADS"
    if [[ ! -f "$path" ]]; then
        me_say "Downloading $file"
        curl -fL --retry 3 --connect-timeout 30 -o "$path.part" "$url" || { rm -f "$path.part"; me_die "Could not download $url"; }
        mv "$path.part" "$path"
    fi
    [[ "$(me_sha256 "$path")" == "$sum" ]] || me_die "$file does not match its pinned SHA-256."
}

# FFmpeg must go through the checked Jellyfin preparer, never this plain extractor.
me_extract() {
    local name="$1" file dest
    [[ "$name" != ffmpeg ]] || me_die "Use prepare-ffmpeg-source.py for Jellyfin FFmpeg."
    file=$(me_lock "$name" file); dest="$ME_WORK/src/$name"
    [[ "$(me_sha256 "$ME_DOWNLOADS/$file")" == "$(me_lock "$name" sha256)" ]] || me_die "Source checksum mismatch: $name"
    rm -rf "$dest"; mkdir -p "$dest"
    tar -xf "$ME_DOWNLOADS/$file" -C "$dest" --strip-components=1
}

me_check_disk() {
    local need_gb="${1:-20}" free_kb
    mkdir -p "$ME_WORK"
    free_kb=$(df -Pk "$ME_WORK" | awk 'NR==2 {print $4}')
    (( free_kb / 1048576 >= need_gb )) || me_die "Less than ${need_gb} GB free under $ME_WORK."
}

me_path_forms() {
    local real
    printf '%s\n' "$1"
    real=$(cd "$1" 2>/dev/null && pwd -P) || return 0
    [[ "$real" == "$1" ]] || printf '%s\n' "$real"
}

me_path_map_pairs() {
    local form
    mkdir -p "$ME_WORK"
    while read -r form; do printf '%s=%s\n' "$form" "$ME_NEUTRAL_WORK"; done < <(me_path_forms "$ME_WORK")
    [[ -n "${1:-}" ]] && while read -r form; do printf '%s=%s\n' "$form" "$ME_NEUTRAL_TOOLCHAIN"; done < <(me_path_forms "$1")
    return 0
}

me_path_map_cflags() {
    local pair out=""
    while read -r pair; do
        [[ "$pair" == *[[:space:]]* ]] && { me_say "Cannot map path with whitespace: ${pair%%=*}"; continue; }
        out+="-ffile-prefix-map=$pair -fdebug-prefix-map=$pair -fmacro-prefix-map=$pair "
    done < <(me_path_map_pairs "$1")
    printf '%s' "${out% }"
}

me_neutralize() {
    local toolchain="$1"; shift
    python3 -I - "$ME_WORK" "$ME_ROOT" "$toolchain" "${HOME:-}" "$ME_NEUTRAL_WORK" "$ME_NEUTRAL_TOOLCHAIN" "$@" <<'EOF'
import os, sys
work, recipe, toolchain, user_home, neutral_work, neutral_toolchain, *files = sys.argv[1:]
pairs = []
for path, target in [(work, neutral_work), (recipe, '/portico-apple-subtitles-recipe'), (toolchain, neutral_toolchain), (user_home, '~')]:
    if path:
        pairs.extend((form.rstrip('/'), target) for form in {path, os.path.realpath(path)})
pairs.sort(key=lambda pair: len(pair[0]), reverse=True)
for name in files:
    with open(name, encoding='utf-8', errors='surrogateescape') as stream:
        data = stream.read()
    for old, new in pairs:
        data = data.replace(old, new)
    with open(name, 'w', encoding='utf-8', errors='surrogateescape') as stream:
        stream.write(data)
EOF
}

me_libass_common_flags() {
    printf '%s\n' -Dtest=disabled -Dcompare=disabled -Dprofile=disabled -Dfuzz=disabled \
        -Dcheckasm=disabled -Dfontconfig=disabled -Ddirectwrite=disabled -Dlibunibreak=enabled
}

me_harfbuzz_common_flags() {
    printf '%s\n' -Dglib=disabled -Dgobject=disabled -Dcairo=disabled -Dchafa=disabled -Dicu=disabled \
        -Dgraphite2=disabled -Dfreetype=disabled -Dfontations=disabled -Dtests=disabled \
        -Dintrospection=disabled -Ddocs=disabled -Dutilities=disabled -Dbenchmark=disabled \
        -Dsubset=disabled -Draster=disabled -Dvector=disabled -Dgpu=disabled -Dgpu_demo=disabled \
        -Dwasm=disabled -Dpng=disabled -Dzlib=disabled -Dcoretext=disabled -Dwith_libstdcxx=false
}

me_freetype_common_flags() {
    printf '%s\n' -Dzlib=system -Dbzip2=disabled -Dpng=disabled -Dharfbuzz=disabled \
        -Dbrotli=disabled -Derror_strings=false -Dtests=disabled
}

me_fribidi_common_flags() { printf '%s\n' -Ddocs=false -Dbin=false -Dtests=false; }

me_write_build_info() {
    python3 -I - "$ME_LOCK" "$1" "$2" <<'EOF'
import json, sys, subprocess, platform
lock = json.load(open(sys.argv[1]))
def run(*cmd):
    try:
        return subprocess.run(cmd, capture_output=True, text=True, check=True).stdout.splitlines()[0].strip()
    except Exception:
        return None
info = {'platform': sys.argv[2], 'lockRevision': lock['revision'],
        'components': {k: v['version'] for k, v in lock['components'].items()},
        'toolsSeen': {'meson': run('meson', '--version'), 'ninja': run('ninja', '--version'),
                      'nasm': run('nasm', '-v'), 'pkg-config': run('pkg-config', '--version'),
                      'xcode': run('xcodebuild', '-version')},
        'buildHost': platform.platform()}
with open(sys.argv[3], 'w') as stream:
    json.dump(info, stream, indent=2, sort_keys=True)
EOF
}
