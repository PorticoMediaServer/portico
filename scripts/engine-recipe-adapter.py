"""Bounded local recipe adapter, derived from reviewed 86a539f. No pin changes."""
import re
from pathlib import Path
import shutil


def compiler_custody(recipe):
    directory = recipe / 'images/base-win64'
    path = directory / 'Dockerfile'
    text = path.read_text()
    marker = '    ./ct-ng build && \\\n    cd / && \\\n    rm -rf ct-ng'
    if text.count(marker) != 1:
        raise ValueError('pinned compiler source-retention boundary changed')
    capture = '''    ./ct-ng build && \\
    mkdir -p /opt/portico-compiler-source/tarballs && \\
    cp -a build/tarballs/. /opt/portico-compiler-source/tarballs/ && \\
    cp .config /opt/portico-compiler-source/crosstool-ng.config && \\
    git rev-parse HEAD > /opt/portico-compiler-source/crosstool-ng.commit && \\
    git archive -o /opt/portico-compiler-source/crosstool-ng.tar HEAD && \\
    xz -T1 -0 /opt/portico-compiler-source/crosstool-ng.tar && \\
    cd / && \\
    rm -rf ct-ng'''
    text = text.replace(marker, capture)
    # Source-only installation, for this exact installed Rust toolchain.
    text += '\nCOPY portico-capture.py /opt/portico-compiler-source/capture.py\nRUN rustup component add rust-src && python3 /opt/portico-compiler-source/capture.py\n'
    path.write_text(text)
    shutil.copyfile(Path(__file__).with_name('capture-engine-compiler.py'), directory / 'portico-capture.py')


def selected_download(text):
    marker = "bash -c 'set -xe && for STAGE in /stages/*.sh; do bash $STAGE; done'"
    if text.count(marker) != 1:
        raise ValueError('pinned source download invocation changed')
    # The runner calls this once per selected source stage, preserving the
    # upstream verified download/cache implementation and its failure status.
    return text.replace(marker, "bash -c 'set -xe; for STAGE in \"$@\"; do bash \"/stages/$STAGE\"; done' -- \"$@\"")


def meson_diagnostics(text, command, label):
    marker = '    ' + command + '\n'
    if text.count(marker) != 1:
        raise ValueError('pinned ' + label + ' Meson diagnostic boundary changed')
    # The file lives inside a disposable compiler container, with no CI token
    # in its environment. Preserve Meson's original failure and all checks.
    return text.replace(marker, '''    if MESON_COMMAND; then
        :
    else
        local meson_status=$?
        printf '%s\\n' 'BEGIN MESON_LABEL meson-log.txt (at most 1048576 bytes)'
        if [[ -f meson-logs/meson-log.txt && ! -L meson-logs/meson-log.txt ]]; then
            head -c 1048576 -- meson-logs/meson-log.txt || :
        else
            printf '%s\\n' 'Meson log unavailable or not a regular non-symlink file'
        fi
        printf '\\n%s\\n' 'END MESON_LABEL meson-log.txt'
        return "$meson_status"
    fi
'''.replace('MESON_COMMAND', command).replace('MESON_LABEL', label))


def libva_diagnostics(text):
    return meson_diagnostics(text, 'meson "${myconf[@]}" ..', 'libva')


def prepare_recipe(recipe, owner):
    # Task-local generated recipe only. Hard limits include dependency pools,
    # not just FFmpeg's final make invocation. No personal Docker state touched.
    for p in recipe.rglob('*'):
        if '.git' in p.parts or not p.is_file() or p.suffix not in ('.sh', ''):
            continue
        try:
            text = p.read_text()
        except UnicodeError:
            continue
        text = text.replace('$(nproc)', '2').replace('$(getconf _NPROCESSORS_ONLN)', '2').replace('xz -T0', 'xz -T1')
        text = re.sub(r'(?<![\w/"-])ninja(?=\s|$)', 'ninja -j2', text) if p.suffix == '.sh' else text
        text = re.sub(r'-j\s*[0-9]+', '-j2', text)
        if p.name == 'Dockerfile':
            # Plain make includes libiconv's order-sensitive autogen cleanup
            # and macro generation. Keep that serial; explicit compile -j2
            # above still overrides this default within the same job ceiling.
            # Host TMPDIR is not inherited by Docker build stages. Shaderc's
            # checked staging recipe requires a writable container-local path.
            # download.sh uses the runner UID, not root: use normal sticky
            # temporary-directory permissions inside the disposable image.
            text = re.sub(r'(?m)^(FROM .+)$', r'\1\nENV MAKEFLAGS=-j1 CARGO_BUILD_JOBS=2 CMAKE_BUILD_PARALLEL_LEVEL=2 RAYON_NUM_THREADS=2 TMPDIR=/var/tmp/portico-ci\nRUN mkdir -p "$TMPDIR" && chmod 1777 "$TMPDIR"', text)
        if p.name == 'download.sh':
            text = text.replace('docker run ', 'docker run --cpus 2 --memory 4g --memory-swap 4g ')
            text = selected_download(text)
        p.write_text(text)
    for p in recipe.glob('images/*/ct-ng-config'):
        text = p.read_text()
        p.write_text(re.sub(r'(?m)^CT_PARALLEL_JOBS=.*$', 'CT_PARALLEL_JOBS=2', text))
    p = recipe / 'util/vars.sh'
    text = p.read_text()
    marker = '${GITHUB_REPOSITORY:-btbn/ffmpeg-builds}'
    if text.count(marker) != 1:
        raise ValueError('pinned local repository hook changed')
    p.write_text(text.replace(marker, '${PORTICO_ENGINE_REPOSITORY:?Explicit local engine namespace required}'))
    p = recipe / 'makeimage.sh'
    text = p.read_text()
    if 'max-parallelism = 4' not in text or 'ffbuilder' not in text or '--driver-opt network=host' not in text:
        raise ValueError('pinned builder resource hooks changed')
    text = text.replace('max-parallelism = 4', 'max-parallelism = 1')
    text = text.replace('ffbuilder', owner)
    text = text.replace('--driver-opt network=host', '--driver-opt memory=4g --driver-opt cpu-quota=200000 --driver-opt cpu-period=100000')
    p.write_text(text)
    p = recipe / 'build.sh'
    text = p.read_text()
    if 'docker run --rm' not in text:
        raise ValueError('pinned runtime resource hook changed')
    p.write_text(text.replace('docker run --rm', 'docker run --cpus 2 --memory 4g --memory-swap 4g --rm'))
    p = recipe / 'scripts.d/50-vaapi/50-libva.sh'
    p.write_text(libva_diagnostics(p.read_text()))
    for relative, command, label in [
        ('50-dav1d.sh', 'meson "${myconf[@]}" ..', 'dav1d'),
        ('50-dvd/40-libdvdread.sh', 'meson setup "${myconf[@]}" ..', 'libdvdread'),
    ]:
        p = recipe / 'scripts.d' / relative
        p.write_text(meson_diagnostics(p.read_text(), command, label))
    compiler_custody(recipe)
