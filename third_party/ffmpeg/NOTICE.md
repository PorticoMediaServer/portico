# Portico media tools

`build.sh` builds ffmpeg, ffprobe and portico-disc from jellyfin-ffmpeg at the tag in
`upstream.lock`, with jellyfin-ffmpeg's own builder, Portico's patches (`*.patch`, `patches/`)
and library recipes (`recipes/`). The build is GPL version 3 (`--enable-gpl --enable-version3`);
it refuses `--enable-nonfree` and libfdk-aac. libbluray and libdvdread are patched so they never
load a decryption module, and no libdvdcss, libaacs or libbdplus is built.

Every bundle carries `LICENSES/` (the GPL text and each built-in library's own licence text),
`NOTICE.md` and `manifest.json` (each library, its licence and exact source, and each file's
SHA-256). Every Portico release that ships a bundle attaches its complete source: see
RELEASING.md, "The media tools (FFmpeg)". `wanted/<platform>.txt` lists what each bundle must have.
