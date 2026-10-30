These two short Dolby Vision profile 7 files are retained procedural test
outputs, not licensed film samples. The base picture is mathematical testsrc2;
the enhancement picture and RPU metadata are synthetic. Each contains 24 HEVC
pictures with B-frame reordering and one authored variable presentation interval.
The MEL/FEL names describe the synthetic RPU NLQ coefficients, not mastered FEL
residual image reconstruction.

Generator: scripts/qualify-hdr-video.py with
third_party/ffmpeg/tests/dovi-video-roundtrip.c, using the public generated HEVC
inputs described by fixtures/video/hdr-generated/README.md. The C fixture writer
adds the explicit profile 7 configuration and authors packet timestamps. Plain
FFmpeg stream-copy of the raw HEVC input does not add this configuration, so it
is not a substitute for these container fixtures. The ordinary qualified-bundle
Go test rechecks the actual configuration and decoded RPU facts independently.
These stable retained bytes do not assert byte-identical container generation
across toolchain versions.

Fixture source and generated clips: GPL-3.0-only, as the generator and adjacent
procedural HDR fixtures. No private media, supplied samples or credentials.
