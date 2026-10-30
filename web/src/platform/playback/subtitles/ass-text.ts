/**
 * A styled subtitle (ASS, SSA) read as plain lines, for a viewer who has turned "Styled subtitles"
 * off: the words and their timing, shown in the viewer's own caption style instead of the file's
 * fonts, colours and placement. It follows the server's reading of the same script
 * (`ParseASSAsText`): vector drawings are left out, an authored top-of-picture line stays at the
 * top, and a line that is italic throughout stays italic.
 */
export type PlainCue = Readonly<{startUs: number; endUs: number; text: string; italic: boolean; top: boolean}>;

export const plainCueLimits = Object.freeze({cues: 20000, cueBytes: 8192, durationMs: 24 * 60 * 60 * 1000});

const override = /\{\\[^{}]{0,512}\}/g;
const alignment = /\\an?([0-9]{1,2})/;
const markup = /<\/?[A-Za-z][^<>]{0,255}>/g;
const italicWrapped = /^\s*<i>[\s\S]*<\/i>\s*$/i;
const drawing = /\\p[1-9]/;
const timestamp = /^([0-9]{1,2}):([0-9]{2}):([0-9]{2})\.([0-9]{1,3})$/;
const encoder = new TextEncoder();
const entity = /&(#x[0-9a-f]{1,6}|#[0-9]{1,7}|amp|lt|gt|quot|apos|nbsp);/gi;
const named: Record<string, string> = {amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: '\u00a0'};
function unescape(whole: string, name: string): string {
  if (name[0] !== '#') return named[name.toLowerCase()] ?? whole;
  const code = name[1] === 'x' || name[1] === 'X' ? parseInt(name.slice(2), 16) : Number(name.slice(1));
  return code > 0 && code <= 0x10ffff && (code < 0xd800 || code > 0xdfff) ? String.fromCodePoint(code) : whole;
}

function milliseconds(value: string): number | undefined {
  const m = timestamp.exec(value.trim());
  if (!m) return undefined;
  const minutes = Number(m[2]), seconds = Number(m[3]);
  if (minutes > 59 || seconds > 59) return undefined;
  return ((Number(m[1]) * 60 + minutes) * 60 + seconds) * 1000 + Number(m[4]!.padEnd(3, '0'));
}

function plainText(body: string): {text: string; italic: boolean; top: boolean} {
  let italic = false, top = false;
  for (const block of body.match(override) ?? []) {
    const align = alignment.exec(block);
    if (align && Number(align[1]) >= 7 && Number(align[1]) <= 9) top = true;
    if (block.includes('\\i1') && body.trim().startsWith(block)) italic = true;
  }
  let text = body.replace(override, '');
  italic ||= italicWrapped.test(text);
  text = text.replace(markup, '').replace(/\\N|\\n/g, '\n').replace(/\\h/g, ' ').replace(entity, unescape);
  // Control and format characters go (joiners stay); a no-break space is a space.
  text = text.replace(/ /g, ' ').replace(/[\p{Cc}\p{Cf}]/gu, c => c === '\n' || c === '\t' || c === '‌' || c === '‍' ? c : '');
  text = text.split('\n').map(line => line.trim()).filter(Boolean).join('\n');
  // A line this long is a broken file, not a subtitle; keep the part that fits.
  const bytes = encoder.encode(text);
  if (bytes.length > plainCueLimits.cueBytes) text = new TextDecoder().decode(bytes.subarray(0, plainCueLimits.cueBytes)).replace(/�$/, '');
  return {text, italic, top};
}

/** The lines of a script, in time order. Throws when it has none, or more than a subtitle can. */
export function assPlainCues(script: string): PlainCue[] {
  const lines = script.replace(/^﻿/, '').split(/\r\n|\r|\n/);
  let inEvents = false, start = 1, end = 2, textAt = 9, columns = 10;
  const seen = new Set<string>(), cues: PlainCue[] = [];
  for (const raw of lines) {
    const line = raw.trim();
    if (line.length > plainCueLimits.cueBytes * 4) continue;
    if (line.startsWith('[')) { inEvents = line.toLowerCase() === '[events]'; continue; }
    if (!inEvents) continue;
    if (/^format:/i.test(line)) {
      const fields = line.slice(7).split(',');
      if (fields.length < 3 || fields.length > 32) { columns = 0; continue; }
      start = 1; end = 2; textAt = 9; columns = fields.length;
      fields.forEach((field, i) => { const name = field.trim().toLowerCase(); if (name === 'start') start = i; else if (name === 'end') end = i; else if (name === 'text') textAt = i; });
      continue;
    }
    if (!/^dialogue:/i.test(line) || columns < 3 || textAt !== columns - 1) continue;
    const rest = line.slice(9), fields: string[] = [];
    let at = 0;
    for (let i = 0; i < columns - 1; i++) { const comma = rest.indexOf(',', at); if (comma < 0) break; fields.push(rest.slice(at, comma)); at = comma + 1; }
    fields.push(rest.slice(at));
    if (fields.length !== columns) continue;
    const from = milliseconds(fields[start]!), to = milliseconds(fields[end]!);
    if (from === undefined || to === undefined || to <= from || to > plainCueLimits.durationMs) continue;
    const body = fields[textAt]!;
    // \p1 and above switch the line to vector drawing; its "text" is a path.
    if (drawing.test(body)) continue;
    const {text, italic, top} = plainText(body);
    const key = from + '|' + to + '|' + text;
    if (!text || seen.has(key)) continue;
    seen.add(key);
    cues.push(Object.freeze({startUs: from * 1000, endUs: to * 1000, text, italic, top}));
    if (cues.length > plainCueLimits.cues) throw new Error('This subtitle has more lines than can be shown.');
  }
  if (!cues.length) throw new Error('This subtitle has no lines to show.');
  return cues.sort((a, b) => a.startUs - b.startUs);
}
