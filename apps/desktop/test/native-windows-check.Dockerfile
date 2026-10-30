# Cross-target type/SDK check only. This never runs Windows or qualifies its output.
FROM rust:1.99.0-slim-bookworm@sha256:2c3a22f0a5533ea2dd5a16627bc841228151faa2d4de2644ac9987e4a2f1f2fa
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc-mingw-w64-x86-64 g++-mingw-w64-x86-64 binutils-mingw-w64-x86-64 \
    && rm -rf /var/lib/apt/lists/*
RUN rustup target add --toolchain 1.99.0 x86_64-pc-windows-gnu
