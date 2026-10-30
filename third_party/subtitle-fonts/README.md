# Fallback fonts for styled subtitles

A styled subtitle names its own fonts and usually carries them. When it does not, or when a line uses a script its font lacks, the browser renderer needs a font that has those letters. The default is Liberation Sans (Latin, Greek, Cyrillic), which comes with the libass build in `third_party/libass-wasm`. The fonts here cover other scripts and are fetched only when a subtitle uses them; the build serves them under `/vendor/subtitle-fonts-1/` (`web/scripts/config.mjs`).

| Script | Font | Licence |
| --- | --- | --- |
| Arabic | Noto Sans Arabic | SIL Open Font License 1.1 |
| Hebrew | Noto Sans Hebrew | SIL Open Font License 1.1 |
| Thai | Noto Sans Thai | SIL Open Font License 1.1 |
| Devanagari | Noto Sans Devanagari | SIL Open Font License 1.1 |
| Bengali | Noto Sans Bengali | SIL Open Font License 1.1 |
| Tamil | Noto Sans Tamil | SIL Open Font License 1.1 |

The files are unmodified copies from the Ubuntu package `fonts-noto-core`; `provenance.json` records each package version and digest. Licence text: `OFL-1.1.txt`.

Chinese, Japanese and Korean are not here. The server already carries Noto Sans CJK for burned-in subtitles (`server/internal/mediaexec/fonts`, about 16 MB) and serves that one copy to the browser at `GET /v1/subtitle-fonts/cjk`, fetched only when a subtitle has CJK letters.
