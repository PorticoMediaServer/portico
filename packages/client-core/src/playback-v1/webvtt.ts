import type {SubtitleCue, SubtitleDocument} from '../subtitles.ts';

const stamp = /^(?:(\d{1,3}):)?(\d{1,2}):(\d{1,2})[.,](\d{1,3})$/;
/** A WebVTT time as whole microseconds, or undefined. */
function micros(text: string): number | undefined {
  const m = stamp.exec(text.trim());
  if (!m) return undefined;
  const [, h = '0', min, s, frac] = m;
  return ((Number(h) * 60 + Number(min)) * 60 + Number(s)) * 1_000_000 + Number((frac! + '00').slice(0, 3)) * 1000;
}

/**
 * The cues of a WebVTT file, as the page's subtitle surface draws them: plain lines, a cue that is
 * wholly italic kept as italic, and one placed at the top (`line:0` or a small percentage) kept at
 * the top. Tags are dropped. A cue that cannot be read is skipped and counted.
 */
export function parseWebVtt(text: string): SubtitleDocument {
  const cues: SubtitleCue[] = [];
  let skipped = 0;
  for (const block of text.replace(/^﻿/, '').replace(/\r\n?/g, '\n').split(/\n{2,}/)) {
    const lines = block.split('\n');
    const at = lines.findIndex(line => line.includes('-->'));
    if (at < 0) continue;
    const [from, rest = ''] = lines[at]!.split('-->');
    const [to = '', ...settings] = rest.trim().split(/\s+/);
    const start = micros(from!), end = micros(to);
    const body = lines.slice(at + 1).join('\n').trim();
    if (start === undefined || end === undefined || end <= start || !body) { skipped++; continue; }
    const italic = /^<i>[\s\S]*<\/i>$/.test(body) && !/<\/i>[\s\S]*<i>/.test(body);
    const line = settings.find(s => s.startsWith('line:'))?.slice(5).split(',')[0];
    const top = line !== undefined && (line === '0' || /^\d+(\.\d+)?%$/.test(line) && parseFloat(line) < 20);
    cues.push(Object.freeze({startUs: String(start), endUs: String(end), text: body.replace(/<[^>]*>/g, '').replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&nbsp;/g, ' '),
      ...(italic ? {italic: true} : {}), ...(top ? {top: true} : {})}));
  }
  cues.sort((a, b) => Number(a.startUs) - Number(b.startUs));
  return Object.freeze({version: 1, timeDomain: 'source-relative', cues: Object.freeze(cues), ...(skipped ? {skippedCues: skipped} : {})});
}
