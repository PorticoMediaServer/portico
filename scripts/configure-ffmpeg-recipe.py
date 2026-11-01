#!/usr/bin/env python3
"""Apply Portico's target build requirements to pinned dependency recipes."""
from pathlib import Path
import json
import hashlib
import sys

recipe, source, target = Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3]
root = Path(__file__).resolve().parent.parent
common = '--disable-autodetect --enable-gpl --enable-version3 --enable-libdav1d --enable-libsvtav1 --enable-libx264 --enable-libx265 --enable-libzimg --enable-libdovi --enable-libass --enable-fontconfig --enable-libsoxr --enable-libbluray --enable-libdvdnav --enable-libdvdread --enable-libplacebo --enable-vulkan --disable-nonfree --disable-libfdk-aac'
flags = common
if target.startswith('linux'):
    flags += ' --enable-vaapi --enable-libdrm --enable-opencl'
if target in ('linux-x64', 'windows-x64', 'windows-arm64'):
    flags += ' --enable-libvpl --enable-ffnvcodec --enable-cuda-llvm --enable-nvenc --enable-nvdec --enable-amf'
if target.startswith('windows'):
    # OpenCL's D3D11 path needs the C COM helpers even when the independent
    # HAVE_OPENCL_DXVA2 block (where FFmpeg defines this macro) is disabled.
    flags += ' --enable-d3d11va --enable-d3d12va --enable-opencl --extra-cflags=-DCOBJMACROS'
if target == 'linux-arm64':
    flags += ' --enable-rkmpp --enable-rkrga --enable-v4l2-m2m'
if target.startswith('macos'):
    flags += ' --enable-videotoolbox --enable-metal'
    # Extras are built from the pinned source recipes into this private prefix.
    build = recipe / 'build-ffmpeg'
    text = build.read_text()
    marker = 'build "ffmpeg" "${FFMPEG_VERSION}"'
    addition = 'source "${CWD}/portico-macos-extras.sh"\nCONFIGURE_OPTIONS+=(' + ' '.join('"' + x + '"' for x in flags.split()) + ')\n'
    if marker not in text:
        raise ValueError('macOS recipe changed')
    text = text.replace(marker, addition + marker, 1)
    build.write_text(text)
    import shutil
    shutil.copy2(root / 'scripts/build-macos-ffmpeg-extras.sh', recipe / 'portico-macos-extras.sh')
    shutil.copy2(root / 'third_party/ffmpeg/macos-extras.lock.json', recipe / 'portico-macos-extras.lock.json')
    shutil.copy2(root / 'third_party/ffmpeg/sources.lock.json', recipe / 'portico-ffmpeg-sources.lock.json')
    patches = recipe / 'portico-no-decryption'
    patches.mkdir(exist_ok=True)
    for name in ['libdvdread-no-decryption.patch', 'libbluray-no-decryption.patch']:
        shutil.copy2(root / 'third_party/ffmpeg/patches' / name, patches / name)
    sys.exit(0)

# BtbN disables Linux ARM VAAPI by default, although libva supports it.
if target.startswith('windows'):
    import subprocess
    subprocess.run([sys.executable, root / 'scripts/configure-windows-libdovi.py', recipe], check=True)
    subprocess.run([sys.executable, root / 'scripts/configure-windows-rav1e.py', recipe], check=True)
# Shaderc's install staging predates TMPDIR support. Keep even that temporary
# tree in the job's on-disk bind mount, including inside the compiler container.
path = recipe / 'scripts.d/47-vulkan/50-shaderc.sh'
text = path.read_text()
if '"/tmp/staging$FFBUILD_DESTDIR"' not in text:
    raise ValueError('Shaderc staging boundary changed')
text = text.replace('"/tmp/staging$FFBUILD_DESTDIR"', '"${TMPDIR:?}/staging$FFBUILD_DESTDIR"')
# Bind mounts may expose the same host filesystem through distinct mount IDs;
# hard links still fail with EXDEV. The short-lived install copy is removed by
# the upstream stage, before the next dependency is built.
if 'cp -al "$DESTDIR"/. "$FFBUILD_DESTDIR"' not in text:
    raise ValueError('Shaderc install copy boundary changed')
path.write_text(text.replace('cp -al "$DESTDIR"/. "$FFBUILD_DESTDIR"',
                             'cp -a "$DESTDIR"/. "$FFBUILD_DESTDIR"'))

# Its GPL recipe normally adds CSS decryption. Keep navigation/readers, with
# no decryption library stage or dvdread decryption dependency.
(recipe / 'scripts.d/50-dvd/30-libdvdcss.sh').unlink(missing_ok=True)
path = recipe / 'scripts.d/50-dvd/40-libdvdread.sh'
text = path.read_text()
if '-Dlibdvdcss=enabled' not in text:
    raise ValueError('BtbN dvdread decryption option changed')
path.write_text(text.replace('-Dlibdvdcss=enabled', '-Dlibdvdcss=disabled'))

# A disabled pkg-config dependency does not disable upstream's runtime dlopen.
# Remove those loader entry points too, retaining encryption detection and the
# plain filesystem/stream readers. Embed the exact patch in the source recipe so
# native and cross-container stages consume identical changes.
for script_name, patch_name in [
    ('50-dvd/40-libdvdread.sh', 'libdvdread-no-decryption.patch'),
    ('50-libbluray.sh', 'libbluray-no-decryption.patch'),
]:
    path = recipe / 'scripts.d' / script_name
    text = path.read_text()
    marker = 'ffbuild_dockerbuild() {\n'
    if text.count(marker) != 1:
        raise ValueError('Disc source recipe boundary changed')
    patch_path = root / 'third_party/ffmpeg/patches' / patch_name
    lock = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())
    expected = next(p['sha256'] for p in lock['linuxSourceBuild']['sourcePatches'] if p['path'] == str(patch_path.relative_to(root)))
    if hashlib.sha256(patch_path.read_bytes()).hexdigest() != expected:
        raise ValueError('Disc source patch checksum mismatch: ' + patch_name)
    patch = patch_path.read_text()
    text = text.replace(marker, marker + "    patch --batch --fuzz=0 -p1 <<'PORTICO_NO_DECRYPTION'\n" + patch + 'PORTICO_NO_DECRYPTION\n', 1)
    path.write_text(text)

path = recipe / 'scripts.d/50-vaapi/50-libva.sh'
text = path.read_text().replace('    [[ $TARGET == linuxarm64 ]] && return -1\n', '')
if target.startswith('windows'):
    marker = 'ffbuild_enabled() {\n'
    if text.count(marker) != 1:
        raise ValueError('BtbN libva selection boundary changed')
    # VA-API is a Linux-only requirement. Do not source-build libva or run its
    # Wine/Meson cross sanity check for either Windows engine architecture.
    text = text.replace(marker, marker + '    [[ $TARGET == win* ]] && return -1\n', 1)
text = text.replace('elif [[ $TARGET == win* ]]; then', '''elif [[ $TARGET == linuxarm64 ]]; then
        myconf+=(--cross-file=/cross.meson --default-library=shared --sysconfdir="/etc"
                 -Ddriverdir="/usr/lib/aarch64-linux-gnu/dri" -Ddisable_drm=false
                 -Dwith_x11=yes -Dwith_glx=no -Dwith_wayland=no)
    elif [[ $TARGET == win* ]]; then''')
path.write_text(text)
# The ARM Windows build must attempt these headers/dispatchers and pass the
# feature verifier; a dependency failure is a build failure, never a downgrade.
for name in ['50-onevpl.sh', '50-ffnvcodec.sh']:
    path = recipe / 'scripts.d' / name
    text = path.read_text()
    text = text.replace('    [[ $TARGET == *arm64 ]] && return -1', '    [[ $TARGET == linuxarm64 ]] && return -1')
    text = text.replace('    [[ $TARGET == winarm64 ]] && (( $(ffbuild_ffver) <= 801 )) && return -1\n', '')
    path.write_text(text)
# Reuse the exact Rockchip dependency commits in the checked Jellyfin source.
for name, directory in [('50-rkmpp.sh', 'rkmpp'), ('50-rkrga.sh', 'rkrga')]:
    text = (source / 'builder/scripts.d' / name).read_text()
    start = text.index('    git-mini-clone ')
    end = text.index('\n', text.index('    cd ' + directory, start))
    text = text[:start] + text[end:]
    # BtbN stages sources before invoking ffbuild_dockerbuild, and installs into
    # a stage DESTDIR which is copied into the final static dependency prefix.
    text = text.replace('ninja install', 'DESTDIR="$FFBUILD_DESTDIR" ninja install')
    text = text.replace('>> "$FFBUILD_PREFIX"/lib/pkgconfig/', '>> "$FFBUILD_DESTPREFIX"/lib/pkgconfig/')
    (recipe / 'scripts.d' / name).write_text(text)
# Final command-line flags override recipe defaults without hiding omissions.
path = recipe / 'build.sh'
text = path.read_text()
needle = '--extra-version="\\$(date +%Y%m%d)"'
if needle not in text:
    raise ValueError('BtbN configure invocation changed')
text = text.replace(needle, '--extra-version="Portico-Jellyfin" ' + flags)
text = text.replace('make -j\\$(nproc) V=1', 'make -j' + str(max(1, int(__import__('os').environ.get('PORTICO_BUILD_JOBS', '4')))) + ' V=1')
if __import__('os').environ.get('PORTICO_FFMPEG_DEPS_ONLY') == '1':
    path.write_text(text)
    print('Configured dependency source recipe for ' + target)
    sys.exit(0)
# This is the final FFmpeg container, after the same dependency prefix has been
# built. The helper uses its compiler, pc files and static archives, not a host SDK.
install = '    make install install-doc\n'
if install not in text:
    raise ValueError('BtbN install boundary changed')
disc = ('    CFLAGS="\\$FF_CFLAGS" LDFLAGS="\\$FF_LDFLAGS \\$FF_LDEXEFLAGS" LIBS="\\$FF_LIBS" '
        'python3 /portico-disc/build-portico-disc.py --source /portico-disc/portico-disc.c '
        '--source-commit "' + __import__('os').environ['PORTICO_DISC_SOURCE_COMMIT'] + '" '
        '--prefix "\\$FFBUILD_PREFIX" --output /ffbuild/prefix/bin/portico-disc' +
        ('.exe' if target.startswith('windows') else '') +
        ' --metadata /ffbuild/prefix/share/doc/ffmpeg/portico-disc-build.json --target ' + target + '\n'
        '    cp /portico-disc/LICENSE /ffbuild/prefix/share/doc/ffmpeg/portico-disc-LICENSE\n')
text = text.replace(install, install + disc, 1)
mount = '-v "$BUILD_SCRIPT":/build.sh "$IMAGE" bash /build.sh'
if mount not in text:
    raise ValueError('BtbN final build mount changed')
text = text.replace(mount, '-v "$BUILD_SCRIPT":/build.sh -v "$PWD/portico-disc":/portico-disc:ro -e SOURCE_DATE_EPOCH "$IMAGE" bash /build.sh', 1)
path.write_text(text)
print('Configured source recipe for ' + target)
