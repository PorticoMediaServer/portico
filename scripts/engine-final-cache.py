"""Make the pinned Windows compile payload an ordinary BuildKit cache stage."""
from pathlib import Path
import hashlib
import re
import subprocess
import zipfile


def round_fingerprint(prefix, source):
    no_links(prefix)
    def digest(path):
        with path.open('rb') as stream:
            return hashlib.file_digest(stream, 'sha256').hexdigest()
    binaries = {}
    for name in ('ffmpeg', 'ffprobe', 'portico-disc'):
        path = prefix / 'bin' / (name + '.exe')
        if not path.is_file() or path.stat().st_size == 0:
            raise ValueError('repeat compile missing executable: ' + name)
        binaries[name] = digest(path)
    if source.is_symlink() or not source.is_file():
        raise ValueError('repeat compile needs a regular sealed source archive')
    return {'sourceArchiveSHA256': digest(source), 'binaries': binaries}


def repeat_rounds(first, second, source, first_fingerprint):
    if first.resolve() == second.resolve():
        raise ValueError('independent compiler output directories required')
    if round_fingerprint(first, source) != first_fingerprint:
        raise ValueError('first compile or source changed during the repeat')
    if round_fingerprint(second, source) != first_fingerprint:
        raise ValueError('repeated engine executable fingerprints differ')
    return [dict(round=index, **first_fingerprint) for index in (1, 2)]


def shell_setup():
    return '''set -e
source util/vars.sh "$1" gpl 8.1
source "variants/${TARGET}-${VARIANT}.sh"
for addin in "${ADDINS[@]}"; do source "addins/${addin}.sh"; done
'''


def compile_program(recipe, target, output, env):
    text = (recipe / 'build.sh').read_text()
    matches = re.findall(r'(?ms)^cat <<EOF >"\$BUILD_SCRIPT"\n(.*?)^EOF\n', text)
    if len(matches) != 1:
        raise ValueError('pinned FFmpeg compile payload boundary changed')
    # The same shell expansion as build.sh, with the same variant/addin inputs.
    # The result is mounted directly into the consumed Dockerfile, once.
    factory = shell_setup() + 'BUILD_SCRIPT="$2"\ncat <<EOF >"$BUILD_SCRIPT"\n' + matches[0] + 'EOF\n'
    subprocess.run(['bash', '-c', factory, 'portico-compile', target, str(output)],
                   cwd=recipe, env=env, check=True)
    subprocess.run(['bash', '-n', output], check=True)


def stages(text, uid, gid, epoch):
    if any(type(value) is not int or value < 0 for value in (uid, gid, epoch)):
        raise ValueError('numeric build identity/epoch required')
    unnamed = re.findall(r'(?m)^FROM (\S+)$', text)
    if len(unnamed) != 1 or not re.findall(r'(?m)^FROM \S+ AS combine-layer$', text):
        raise ValueError('pinned final dependency prefix boundary changed')
    text = text.replace('FROM ' + unnamed[0] + '\n', 'FROM ' + unnamed[0] + ' AS portico-dependency-prefix\n', 1)
    return text + f'''
FROM portico-dependency-prefix AS portico-ffmpeg
ENV SOURCE_DATE_EPOCH={epoch}
USER 0:0
RUN mkdir -p /ffbuild && chown {uid}:{gid} /ffbuild
USER {uid}:{gid}
RUN --mount=src=portico-ffmpeg-source.tar.xz,dst=/portico-source.tar.xz --mount=src=portico-disc,dst=/portico-disc --mount=src=portico-compile.sh,dst=/portico-compile.sh cp /portico-source.tar.xz /ffbuild/source.tar.xz && bash /portico-compile.sh && rm -rf /ffbuild/ffmpeg && rm -f /ffbuild/source.tar.xz
FROM scratch AS portico-engine-output
COPY --from=portico-ffmpeg /ffbuild/prefix/ /
'''


def no_links(root):
    if root.is_symlink() or any(path.is_symlink() for path in root.rglob('*')):
        raise ValueError('engine prefix/package links are not accepted')


def package(recipe, target, prefix, source, output, env):
    no_links(prefix)
    if output.exists():
        raise ValueError('raw engine package must be fresh')
    output.mkdir(parents=True)
    factory = shell_setup() + '''
[[ "$LICENSE_FILE" == COPYING.GPLv3 ]] || { echo 'GPLv3 licence selection changed' >&2; exit 1; }
package_variant "$2" "$4"
cp "$3/$LICENSE_FILE" "$4/LICENSE.txt"
'''
    subprocess.run(['bash', '-c', factory, 'portico-package', target, str(prefix), str(source), str(output)],
                   cwd=recipe, env=env, check=True)
    no_links(output)
    for name in ('ffmpeg.exe', 'ffprobe.exe', 'portico-disc.exe'):
        path = output / 'bin' / name
        if not path.is_file() or path.stat().st_size == 0:
            raise ValueError('missing compiled engine executable: ' + name)


def archive(package, output):
    no_links(package)
    with zipfile.ZipFile(output, 'x', compression=zipfile.ZIP_STORED) as archive:
        for path in sorted(package.rglob('*')):
            if path.is_file():
                archive.write(path, package.name + '/' + path.relative_to(package).as_posix())
