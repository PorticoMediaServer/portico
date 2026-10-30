# Portico server, containerised.
#
# Two things about this image are load-bearing and easy to get wrong, so they are
# both written down here rather than left to whoever deploys it.
#
# 1. PID 1. The server handles SIGTERM correctly — signal.NotifyContext in
#    cmd/server/main.go — so running it as PID 1 shuts down cleanly. What it
#    cannot do as PID 1 is reap. The media pipeline runs ffmpeg and ffprobe as
#    children of the server and, on Unix, in their own process groups so a
#    cancelled request kills the whole group. If one of those children forks
#    again and the grandchild outlives its parent, the grandchild is reparented
#    to PID 1. A normal init reaps it; a Go program does not, and the process
#    table fills with zombies over days of use. So the image declares an init
#    process as its entrypoint, and a deployment that overrides the entrypoint
#    must pass `--init` (or run under a supervisor that reaps) instead.
#
# 2. ffmpeg and ffprobe. The server shells out to them for conversion, analysis
#    and subtitle rendering. They are installed here and named explicitly, rather
#    than left to PATH, so that a missing one is a build failure instead of a
#    runtime one that only appears when somebody presses play.
#
# 3. The decoder sandbox. bubblewrap is installed so FFmpeg and ffprobe run
#    sandboxed wherever the container may create user namespaces (Docker's
#    default seccomp and AppArmor profiles usually refuse them). Where it can't,
#    the server still plays, records and converts, with the baseline
#    restrictions, and Server › Logs & diagnostics says so and why
#    (server/internal/mediaexec). PORTICO_DECODER_SANDBOX=required
#    makes media jobs fail instead; =off skips the sandbox.

FROM node:24.21.0-bookworm@sha256:3d27e5c11e5786e309ec3e03f93ae536eb36e6e5eb3714d5eb3300a36157add0 AS web
WORKDIR /src
COPY package.json package-lock.json ./
COPY web ./web
COPY packages ./packages
COPY third_party/hls.js ./third_party/hls.js
# The subtitle renderer and its fonts: the web build copies them, unchanged, into the application.
COPY third_party/libass-wasm ./third_party/libass-wasm
COPY third_party/subtitle-fonts ./third_party/subtitle-fonts
COPY server ./server
COPY apikit ./apikit
COPY scripts/container-release.mjs ./scripts/container-release.mjs
COPY Dockerfile go.work ./
ARG PORTICO_VERSION
ARG PORTICO_BUILT_AT
RUN PORTICO_VERSION="$PORTICO_VERSION" PORTICO_BUILT_AT="$PORTICO_BUILT_AT" node scripts/container-release.mjs
RUN npm ci --ignore-scripts --no-audit --no-fund
WORKDIR /src/web
# This is the server-hosted application: no VITE_* switch reaches the build, so
# the Portico Services entry (VITE_HOSTED_WEB with VITE_ACCOUNT_WORKSPACE) stays off.
RUN test -z "$(env | grep '^VITE_')" && test -z "$(find . -maxdepth 1 -name '.env*')" \
 && node --input-type=module -e "import {build} from 'vite'; await build({build:{outDir:'/out/web',sourcemap:false}})" \
 && test -f /out/web/vendor/libass-wasm-4.2.4/subtitles-octopus-worker.js && test -d /out/web/vendor/subtitle-fonts-1
RUN cp /out/portico-build.json /out/web/portico-build.json

FROM golang:1.26.10-bookworm@sha256:632dff7351799a5a9954a48dc06670ade72ee44e8c351cdc0e5a57af2fea21af AS build
WORKDIR /src/server
# Official Go build tags, from scripts/release-build-tags.py. The default is the
# release argument for the same list; a CI guard (scripts/test-official-build-tags.py)
# fails when this default and the helper disagree or when a build drops the tag.
ARG PORTICO_GO_TAGS=release,portico_rar7z,portico_cb7
COPY trust/hosted-root-public-key.pem /identity/root-public-key.pem
COPY scripts/inspect-hosted-root.go /identity/inspect-hosted-root.go
COPY go.work /src/go.work
COPY apikit /src/apikit
COPY server/go.mod server/go.sum ./
RUN test "$(go env GOVERSION)" = "go$(sed -n 's/^go //p' go.mod)" && go mod download
COPY server ./
COPY --from=web /out/build.env /identity/build.env
ENV CGO_ENABLED=0
RUN ROOT_KEY=$(go run /identity/inspect-hosted-root.go /identity/root-public-key.pem) && test -n "$ROOT_KEY" && . /identity/build.env && go build -tags "$PORTICO_GO_TAGS" -buildvcs=false -mod=readonly -trimpath -ldflags "-s -w -X github.com/PorticoMediaServer/portico/server/internal/hostedtrust.OfficialRoot=$ROOT_KEY -X github.com/PorticoMediaServer/portico/server/internal/buildinfo.Version=$VERSION -X github.com/PorticoMediaServer/portico/server/internal/buildinfo.BuildID=$BUILD_ID -X github.com/PorticoMediaServer/portico/server/internal/buildinfo.SourceDigest=$SOURCE_DIGEST -X github.com/PorticoMediaServer/portico/server/internal/buildinfo.BuiltAt=$BUILT_AT" -o /out/portico-server ./cmd/server

# The qualified FFmpeg bundle for the architecture this image is being built
# for. A multi-architecture build supplies one bundle per architecture, in
# third_party/ffmpeg/bundle/linux-amd64 and .../linux-arm64, and each image
# takes its own. A single-architecture build may instead place the native
# bundle directly in third_party/ffmpeg/bundle. Either way the final stage
# verifies what it was given before the image is finished.
FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS ffmpeg
RUN apt-get update && apt-get install --yes --no-install-recommends python3 bash binutils && rm -rf /var/lib/apt/lists/*
ARG TARGETOS
ARG TARGETARCH
COPY third_party/ffmpeg/bundle/ /bundle/
RUN if [ -d "/bundle/${TARGETOS}-${TARGETARCH}" ]; then mv "/bundle/${TARGETOS}-${TARGETARCH}" /ffmpeg; else mv /bundle /ffmpeg; fi

COPY scripts/verify-ffmpeg-*.py scripts/verify-disc-bundle.py scripts/verify-ffmpeg-bundle.sh /tmp/
COPY fixtures/discs/ /fixtures/discs/
COPY packages/reader-core/assets/Literata[[]opsz,wght].ttf /fixtures/Literata.ttf
ENV PORTICO_DISC_FIXTURES=/fixtures/discs/manifest.json \
    PORTICO_DISC_FIXTURES_SHA256=d531e2332adb16261add1fca78f3670be0b4ec2385453018a45fa96c6add77fa \
    PORTICO_FFMPEG_TEST_FONT=/fixtures/Literata.ttf \
    TMPDIR=/tmp/portico-verify
RUN mkdir -p "$TMPDIR" && bash /tmp/verify-ffmpeg-bundle.sh /ffmpeg full

# Sonic is independently verified before composition. Keep the sealed FFmpeg
# inventory unchanged; the final installed union is checked by sonic-bundle.py.
FROM ffmpeg AS media-tools
ARG TARGETARCH
COPY scripts/sonic-bundle.py scripts/verify-ffmpeg-manifest.py /src/scripts/
COPY third_party/portico-sonic/runtime.lock.json /src/third_party/portico-sonic/runtime.lock.json
COPY third_party/musicnn/model.lock.json /src/third_party/musicnn/model.lock.json
COPY third_party/portico-sonic/bundle/ /sonic-bundles/
RUN if [ "$TARGETARCH" = amd64 ]; then target=linux-x64; else target=linux-arm64; fi \
 && python3 /src/scripts/sonic-bundle.py install /ffmpeg --bundle "/sonic-bundles/linux-${TARGETARCH}" --target "$target"

# Fetch only the pinned architecture, verify the upstream signature and every payload byte.
FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS rclone
RUN apt-get update && apt-get install --yes --no-install-recommends python3 curl gnupg ca-certificates && rm -rf /var/lib/apt/lists/*
ARG TARGETOS
ARG TARGETARCH
COPY scripts/rclone-bundle.py /src/scripts/rclone-bundle.py
COPY third_party/rclone/ /src/third_party/rclone/
RUN python3 /src/scripts/rclone-bundle.py prepare /rclone --target "${TARGETOS}-${TARGETARCH}" --execute-version

# Driver userspace is independent of the GPLv3 FFmpeg build. Intel's current
# OpenCL packages need the newer runtime; keep old and new Intel ICDs together.
FROM debian:trixie-slim@sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad AS runtime-drivers
ARG TARGETARCH
ARG DEBIAN_FRONTEND=noninteractive
RUN set -eu; if [ "$TARGETARCH" = amd64 ]; then sed -i 's/Components: main$/Components: main non-free/' /etc/apt/sources.list.d/debian.sources; fi \
 && apt-get update \
 && apt-get install --yes --no-install-recommends bubblewrap ca-certificates util-linux tini tzdata python3 libva2 libva-drm2 mesa-va-drivers mesa-vulkan-drivers mesa-opencl-icd libvulkan1 ocl-icd-libopencl1 vainfo clinfo vulkan-tools \
 && if [ "$TARGETARCH" = amd64 ]; then \
      apt-get install --yes --no-install-recommends intel-media-va-driver-non-free i965-va-driver libvpl2 libmfx-gen1.2; \
      mkdir -p /usr/share/doc/portico/licenses/runtime-drivers; \
      cp /usr/share/doc/intel-media-va-driver-non-free/copyright /usr/share/doc/portico/licenses/runtime-drivers/intel-media-va-driver-non-free.txt; \
    fi \
 && rm -rf /var/lib/apt/lists/*

COPY scripts/install-intel-opencl.py /tmp/install-intel-opencl.py
COPY third_party/ffmpeg/intel-drivers.lock.json /usr/share/doc/portico/intel-drivers.lock.json
RUN apt-get update \
 && python3 /tmp/install-intel-opencl.py /usr/share/doc/portico/intel-drivers.lock.json \
 && rm -rf /var/lib/apt/lists/* /tmp/install-intel-opencl.py

FROM runtime-drivers

# Runtime writes use /config, disk /transcode and a separate bounded tmpfs.
# Docker supplies bounded /dev/shm (64 MiB by default). Portico discovers it
# when /memory-transcode is not mounted, using a private installation child.
# A Dockerfile cannot declare runtime tmpfs; VOLUME would create disk storage.
# Compose examples mount /memory-transcode explicitly; see INSTALLING.md for
# docker run options and cap sizing. No privileged mounts are needed.
# Numeric identities work with Unraid and TrueNAS without editing /etc/passwd.
RUN mkdir -p /config /transcode
COPY packaging/docker/entrypoint.sh /usr/local/bin/portico-entrypoint
COPY LICENSE THIRD-PARTY-NOTICES.md /usr/share/doc/portico/
COPY server/internal/imagework/SCALE_LICENSE /usr/share/doc/portico/licenses/image-scaling.txt
COPY server/internal/thirdparty/sqlite/LICENSE /usr/share/doc/portico/licenses/sqlite-driver.txt
COPY server/internal/mediaexec/fonts/LICENSE /usr/share/doc/portico/licenses/decoder-fonts.txt
COPY third_party/hls.js/LICENSE /usr/share/doc/portico/licenses/hls-js.txt
COPY web/src/kit/fonts/OFL.txt /usr/share/doc/portico/licenses/manrope.txt
COPY packages/design/LICENSE-LUCIDE /usr/share/doc/portico/licenses/lucide.txt
COPY --from=rclone /rclone/LICENSE /usr/share/doc/portico/licenses/rclone.txt
COPY --from=rclone /rclone/DEPENDENCY-NOTICES.txt /usr/share/doc/portico/licenses/rclone-dependencies.txt
COPY --from=rclone /rclone/ /opt/portico/third_party/rclone/
COPY --from=build /out/portico-server /usr/local/bin/portico-server
COPY --from=web /out/web /opt/portico/web
COPY --from=web /out/release.json /opt/portico/release.json
# Supply the qualified bundle from the manual toolchain workflow (see the
# ffmpeg stage above for where it goes).
COPY --from=media-tools /ffmpeg/ /opt/portico/third_party/ffmpeg/

ENV PORTICO_WEB_DIR=/opt/portico/web \
    PORTICO_DATA_DIR=/config \
    PORTICO_CONTAINER=1 \
    NVIDIA_VISIBLE_DEVICES=all \
    NVIDIA_DRIVER_CAPABILITIES=compute,video,utility \
    HOME=/config \
    TMPDIR=/config/Temp \
    PORTICO_BIND=[::]:32500 \
    PORTICO_RCLONE=/opt/portico/third_party/rclone/rclone \
    PORTICO_FFMPEG=/opt/portico/third_party/ffmpeg/bin/ffmpeg \
    PORTICO_FFPROBE=/opt/portico/third_party/ffmpeg/bin/ffprobe
VOLUME ["/config"]
EXPOSE 32500
WORKDIR /config

# tini forwards signals and reaps orphans. It is the whole of point 1 above; do
# not replace it with the binary directly unless the runtime supplies its own
# init (`docker run --init`, or a Kubernetes pod with shareProcessNamespace and
# a reaping PID 1).
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/portico-entrypoint"]
CMD ["/usr/local/bin/portico-server"]

# The readiness route needs no authentication and no database, by design, so it
# answers while the server is still opening its state directory — which is what
# makes it usable as a container health check rather than only as a liveness one.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/usr/local/bin/portico-server", "--health-check"]
