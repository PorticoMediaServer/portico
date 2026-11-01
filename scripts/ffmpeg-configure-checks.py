#!/usr/bin/env python3
"""Refuse missing effective configure components before any FFmpeg compilation."""


def macros(target):
    values = '''BZLIB ZLIB ICONV LZMA LIBXML2 LIBMP3LAME LIBVORBIS LIBVPX LIBWEBP
        CHROMAPRINT LIBOPUS LIBTHEORA LIBFREETYPE LIBFRIBIDI LIBHARFBUZZ
        LIBMP3LAME_ENCODER LIBOPUS_ENCODER LIBTHEORA_ENCODER DRAWTEXT_FILTER
        DASH_DEMUXER CHROMAPRINT_MUXER HTTPS_PROTOCOL TLS_PROTOCOL'''.split()
    if target != 'windows-arm64': values += ['LIBOPENMPT', 'LIBOPENMPT_DEMUXER']
    if not target.startswith('windows'): values += ['LIBSRT', 'LIBSRT_PROTOCOL']
    if target.startswith('linux'): values += ['OPENSSL']
    if not target.startswith('macos'):
        values += ['CUDA', 'CUVID', 'FFNVCODEC', 'NVDEC', 'NVENC', 'H264_CUVID_DECODER', 'HEVC_CUVID_DECODER']
    if target.startswith('windows'):
        values += ['DXVA2', 'MEDIAFOUNDATION', 'SCHANNEL', 'H264_MF_ENCODER']
    if target.startswith('macos'):
        values += ['OPENSSL', 'COREIMAGE', 'METAL', 'LIBSHADERC', 'APPKIT', 'AVFOUNDATION',
                   'AUDIOTOOLBOX', 'VIDEOTOOLBOX', 'OVERLAY_VIDEOTOOLBOX_FILTER']
    return sorted(set('CONFIG_' + value for value in values))


def shell(target):
    return '\n'.join("grep -Eq '^#define " + value + " 1$' config.h config_components.h || "
        "{ echo 'Effective configure component missing: " + value + "' >&2; exit 1; }"
        for value in macros(target))
