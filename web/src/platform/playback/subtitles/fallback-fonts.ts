/**
 * Fonts for scripts the default subtitle font (Latin, Greek, Cyrillic) does not have.
 *
 * A styled subtitle usually brings its own fonts. When a line uses letters none of them has,
 * libass looks through every font it was given, so the fonts for the scripts actually present in
 * the subtitle are added to the list. A script that is not in the subtitle costs nothing.
 *
 * Chinese, Japanese and Korean come from the server: the one Noto Sans CJK copy it already has for
 * burned-in subtitles (about 16 MB, so it is never part of the web app), fetched only for a
 * subtitle that uses those letters and kept by the browser's cache.
 */
import {fallbackFontUrl} from './vendor.ts';

/** `file` is one of the web app's own fonts; a script without one is the server's (`serverFont`). */
type Script = Readonly<{name: string; file?: string; ranges: readonly (readonly [number, number])[]}>;
export const fallbackScripts: readonly Script[] = Object.freeze([
  {name: 'arabic', file: 'NotoSansArabic-Regular.ttf', ranges: [[0x0600, 0x06ff], [0x0750, 0x077f], [0x08a0, 0x08ff], [0xfb50, 0xfdff], [0xfe70, 0xfeff]]},
  {name: 'hebrew', file: 'NotoSansHebrew-Regular.ttf', ranges: [[0x0590, 0x05ff], [0xfb1d, 0xfb4f]]},
  {name: 'thai', file: 'NotoSansThai-Regular.ttf', ranges: [[0x0e00, 0x0e7f]]},
  {name: 'devanagari', file: 'NotoSansDevanagari-Regular.ttf', ranges: [[0x0900, 0x097f], [0xa8e0, 0xa8ff]]},
  {name: 'bengali', file: 'NotoSansBengali-Regular.ttf', ranges: [[0x0980, 0x09ff]]},
  {name: 'tamil', file: 'NotoSansTamil-Regular.ttf', ranges: [[0x0b80, 0x0bff]]},
  // Chinese, Japanese and Korean (Hangul jamo and syllables too), from the server.
  {name: 'cjk', ranges: [[0x1100, 0x11ff], [0x2e80, 0x30ff], [0x3100, 0x318f], [0x31a0, 0x4dbf], [0x4e00, 0x9fff], [0xa960, 0xa97f], [0xac00, 0xd7ff], [0xf900, 0xfaff], [0xfe30, 0xfe4f], [0xff00, 0xffef]]},
]);

/** The scripts a subtitle's text uses, among those a fallback font exists for. */
export function scriptsIn(text: string): string[] {
  const found = new Set<string>();
  for (let i = 0; i < text.length && found.size < fallbackScripts.length; i++) {
    const code = text.charCodeAt(i);
    if (code < 0x0590) continue;
    for (const script of fallbackScripts) {
      if (found.has(script.name)) continue;
      for (const [from, to] of script.ranges) if (code >= from && code <= to) { found.add(script.name); break; }
    }
  }
  return fallbackScripts.filter(s => found.has(s.name)).map(s => s.name);
}

/** Addresses of the fallback fonts this subtitle needs. `serverFont` names the server's font for a
 * script the web app has none of; without it that script is left to the subtitle's own fonts. */
export function fallbackFontsFor(text: string, serverFont?: (script: string) => string | undefined): string[] {
  const wanted = scriptsIn(text);
  return fallbackScripts.filter(s => wanted.includes(s.name)).flatMap(s => {
    const url = s.file ? fallbackFontUrl(s.file) : serverFont?.(s.name);
    return url ? [url] : [];
  });
}
