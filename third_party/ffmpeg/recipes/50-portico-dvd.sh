#!/bin/bash
# DVD reading: FFmpeg's dvdvideo input and the portico-disc helper both need these two libraries.

SCRIPT_REPO="https://code.videolan.org/videolan/libdvdread.git"
SCRIPT_COMMIT="ce9bdb7775601f4e6b95136b875bc72a53c5ba7d"
SCRIPT_REPO2="https://code.videolan.org/videolan/libdvdnav.git"
SCRIPT_COMMIT2="2ffc50b5c37a6ddc086829203fc44e95588198dd"

ffbuild_enabled() {
    [[ $TARGET == linux* ]]
}

ffbuild_dockerstage() {
    to_df "RUN --mount=src=${SELF},dst=/stage.sh --mount=src=patches/portico,dst=/patches run_stage /stage.sh"
}

ffbuild_dockerbuild() {
    local myconf=(
        --prefix="$FFBUILD_PREFIX"
        --buildtype=release
        -Ddefault_library=static
        --cross-file=/cross.meson
    )

    git-mini-clone "$SCRIPT_REPO" "$SCRIPT_COMMIT" dvdread
    cd dvdread
    # Portico never loads a decryption module.
    patch -p1 < /patches/libdvdread-no-decryption.patch
    mkdir build && cd build
    meson setup "${myconf[@]}" -Dlibdvdcss=disabled ..
    ninja -j$(nproc)
    ninja install
    cd ../..

    git-mini-clone "$SCRIPT_REPO2" "$SCRIPT_COMMIT2" dvdnav
    cd dvdnav
    mkdir build && cd build
    meson setup "${myconf[@]}" ..
    ninja -j$(nproc)
    ninja install
}

ffbuild_configure() {
    echo --enable-libdvdread --enable-libdvdnav
}

ffbuild_unconfigure() {
    echo --disable-libdvdread --disable-libdvdnav
}
