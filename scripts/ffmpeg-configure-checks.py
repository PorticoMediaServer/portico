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
        values += ['SCALE_D3D11_FILTER', 'OVERLAY_D3D11_FILTER', 'BWDIF_D3D11_FILTER', 'TONEMAP_D3D11_FILTER']
    if target.startswith('macos'):
        values += ['OPENSSL', 'COREIMAGE', 'METAL', 'LIBSHADERC', 'APPKIT', 'AVFOUNDATION',
                   'AUDIOTOOLBOX', 'VIDEOTOOLBOX', 'OVERLAY_VIDEOTOOLBOX_FILTER']
    result = set('CONFIG_' + value for value in values)
    # Jellyfin probes d3dcompiler.h and requires it for these four filters,
    # but does not add d3dcompiler_h to HAVE_LIST/HEADERS_LIST. Consequently
    # HAVE_D3DCOMPILER_H is never emitted, even when every filter is enabled.
    # Check the real filter macros here; the compiler control includes the
    # actual header independently before any dependency/FFmpeg compilation.
    return sorted(result)


def shell(target):
    return '\n'.join("grep -Eq '^#define " + value + " 1$' config.h config_components.h || "
        "{ echo 'Effective configure component missing: " + value + "' >&2; exit 1; }"
        for value in macros(target))
