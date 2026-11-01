#!/usr/bin/env python3
"""Add the checked official bzip2 static source stage to a pinned BtbN graph."""
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[1]


def configure(recipe):
    pin = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())['bzip2']
    if (pin['source'] != 'https://sourceware.org/pub/bzip2/bzip2-' + pin['version'] + '.tar.gz' or
            not re.fullmatch('[a-f0-9]{64}', pin['sha256'])):
        raise ValueError('exact official bzip2 source and SHA-256 required')
    script = recipe / 'scripts.d/20-portico-bzip2.sh'
    if script.exists(): raise ValueError('fresh bzip2 source stage required')
    script.write_text('''#!/bin/bash
ffbuild_enabled() { return 0; }
ffbuild_dockerdl() {
    echo 'curl --fail --location --retry 3 --proto =https "''' + pin['source'] + '''" -o bzip2.tar.gz'
    echo 'echo "''' + pin['sha256'] + '''  bzip2.tar.gz" | sha256sum -c -'
    echo 'mkdir source; tar -xzf bzip2.tar.gz -C source --strip-components=1'
}
ffbuild_dockerbuild() {
    cd source
    # Build only the static library: upstream all/install run target programs.
    make -j2 CC="$CC" AR="$AR" RANLIB="$RANLIB" CFLAGS="$CFLAGS -fPIC" libbz2.a
    install -Dm644 libbz2.a "$FFBUILD_DESTPREFIX/lib/libbz2.a"
    install -Dm644 bzlib.h "$FFBUILD_DESTPREFIX/include/bzlib.h"
    install -Dm644 LICENSE "$FFBUILD_DESTPREFIX/share/licenses/bzip2/LICENSE"
}
ffbuild_configure() { echo --enable-bzlib; }
''')
    final = recipe / 'scripts.d/zz-final.sh'; text = final.read_text()
    marker = 'ffbuild_depends() {\n'
    if text.count(marker) != 1: raise ValueError('pinned final dependency boundary changed')
    final.write_text(text.replace(marker, marker + '    echo portico-bzip2\n', 1))
