#!/bin/bash
# Some older Matroska files compress their tracks with bzip2.

SCRIPT_URL="https://sourceware.org/pub/bzip2/bzip2-1.0.8.tar.gz"
SCRIPT_SHA256="ab5a03176ee106d3f0fa90e381da478ddae405918153cca248e682cd0c4a2269"

ffbuild_enabled() {
    [[ $TARGET == linux* ]]
}

ffbuild_dockerbuild() {
    retry-tool wget -O bzip2.tar.gz "$SCRIPT_URL"
    echo "$SCRIPT_SHA256  bzip2.tar.gz" | sha256sum -c -
    tar xaf bzip2.tar.gz
    cd bzip2-1.0.8

    make -j$(nproc) libbz2.a CC="$CC" AR="$AR" RANLIB="$RANLIB" CFLAGS="$CFLAGS -D_FILE_OFFSET_BITS=64"
    install -D -m 644 bzlib.h "$FFBUILD_PREFIX"/include/bzlib.h
    install -D -m 644 libbz2.a "$FFBUILD_PREFIX"/lib/libbz2.a
}

ffbuild_configure() {
    echo --enable-bzlib
}

ffbuild_unconfigure() {
    echo --disable-bzlib
}
