#!/bin/bash
# Dolby Vision metadata conversion (profile 7 to 8.1) for Portico's dovi_profile and merge_dovi patches.

SCRIPT_REPO="https://github.com/quietvoid/dovi_tool.git"
SCRIPT_COMMIT="26ed61ff36f946313889a09c0d2da202e9ceb97a"

ffbuild_enabled() {
    [[ $TARGET == linux* ]]
}

ffbuild_dockerbuild() {
    git-mini-clone "$SCRIPT_REPO" "$SCRIPT_COMMIT" dovi
    cd dovi/dolby_vision

    # Rust links the library for the target with the same compiler the other libraries use.
    local rust_target="${FFBUILD_RUST_TARGET//-/_}"
    export "CARGO_TARGET_${rust_target^^}_LINKER=$CC" "CC_${rust_target}=$CC" "AR_${rust_target}=$AR"

    cargo cinstall --locked --features capi --release --library-type staticlib \
        --target "$FFBUILD_RUST_TARGET" --prefix "$FFBUILD_PREFIX" --libdir lib

    # The compiler links its own support library statically; the shared one is not in the toolchain.
    sed -i 's/ -lgcc_s//g' "$FFBUILD_PREFIX"/lib/pkgconfig/dovi.pc
}

ffbuild_configure() {
    echo --enable-libdovi
}

ffbuild_unconfigure() {
    echo --disable-libdovi
}
