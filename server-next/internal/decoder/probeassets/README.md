# HDR10 probe input

hdr10.hevc is five black frames, 160x120 at 10 fps, HEVC Main 10,
limited-range BT.2020 non-constant luminance, SMPTE ST 2084 (PQ). It carries
mastering-display metadata (1000 nit maximum, 0.005 nit minimum, BT.2020
primaries, D65 white) and content-light levels (1000/400 nit). The VA-API
tone mapper requires that side data; a synthetic SDR lavfi source lacks it.

Recreate with an FFmpeg installation that has libx265:

    ffmpeg -v error -nostdin -f lavfi \
      -i 'color=black:size=160x120:rate=10,format=yuv420p10le' \
      -frames:v 5 -c:v libx265 -preset ultrafast \
      -x265-params 'pools=none:frame-threads=1:log-level=error:repeat-headers=1:hdr10=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50):max-cll=1000,400' \
      -f hevc hdr10.hevc

Check with ffprobe -v error -show_frames -of json hdr10.hevc. No generator
runs at startup: the decoder embeds the clip and exposes only a private
temporary copy, read-only, to its bounded probe process.
