#!/bin/bash
# The resampler the server uses to convert and analyse music.

SCRIPT_REPO="https://git.code.sf.net/p/soxr/code"
SCRIPT_COMMIT="945b592b70470e29f917f4de89b4281fbbd540c0"

ffbuild_enabled() {
    [[ $TARGET == linux* || $TARGET == win64 || $TARGET == mac* ]]
}

ffbuild_dockerbuild() {
    git-mini-clone "$SCRIPT_REPO" "$SCRIPT_COMMIT" soxr
    cd soxr
    mkdir build && cd build

    # On the Mac the system's compiler builds for the Mac itself; elsewhere upstream's toolchain file.
    local toolchain=()
    [[ $TARGET == mac* ]] || toolchain=(-DCMAKE_TOOLCHAIN_FILE="$FFBUILD_CMAKE_TOOLCHAIN")

    # soxr asks for a CMake older than current CMake accepts unless told otherwise.
    cmake -GNinja "${toolchain[@]}" -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_INSTALL_PREFIX="$FFBUILD_PREFIX" -DCMAKE_POLICY_VERSION_MINIMUM=3.5 \
        -DBUILD_SHARED_LIBS=OFF -DBUILD_TESTS=OFF -DBUILD_EXAMPLES=OFF \
        -DWITH_OPENMP=OFF -DWITH_LSR_BINDINGS=OFF ..
    ninja -j$(nproc)
    ninja install
}

ffbuild_configure() {
    echo --enable-libsoxr
}

ffbuild_unconfigure() {
    echo --disable-libsoxr
}
