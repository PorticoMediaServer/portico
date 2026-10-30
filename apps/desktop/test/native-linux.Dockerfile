# Development conformance only; this distribution mpv is not the release FFmpeg8.1.3 bundle.
# The checked package lock/toolchain receipt and physical-output qualification remain separate.
FROM rust:1.99.0-slim-bookworm@sha256:2c3a22f0a5533ea2dd5a16627bc841228151faa2d4de2644ac9987e4a2f1f2fa AS rust-toolchain
FROM ubuntu:26.04@sha256:c098726dd9b9733f3e885f8de0ef375206a311b0a317eadc40d347b1115fb944
LABEL portico="desktop-r29"
ENV DEBIAN_FRONTEND=noninteractive \
    CARGO_HOME=/opt/portico/cargo RUSTUP_HOME=/opt/portico/rustup \
    PATH=/opt/portico/cargo/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
COPY --from=rust-toolchain /usr/local/cargo /opt/portico/cargo
COPY --from=rust-toolchain /usr/local/rustup /opt/portico/rustup
RUN apt-get -o Acquire::Retries=0 -o Acquire::http::Timeout=20 update && apt-get install -y --no-install-recommends \
    build-essential pkg-config ca-certificates python3 curl \
    libwebkit2gtk-4.1-dev libgtk-3-dev libmpv-dev ffmpeg xvfb xauth dbus-x11 \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /source
RUN apt-get -o Acquire::Retries=0 -o Acquire::http::Timeout=20 update && apt-get install -y --no-install-recommends \
    gstreamer1.0-plugins-base gstreamer1.0-plugins-good gstreamer1.0-gl \
    libegl1 libgl1-mesa-dri libglx-mesa0 at-spi2-core \
    && rm -rf /var/lib/apt/lists/*
