/**
 * DVD subtitles (VobSub): an `.idx` text index and a `.sub` MPEG program stream.
 *
 * The index gives the canvas size, the sixteen-colour palette and, for each subtitle, its time
 * and where its packet starts in the `.sub`. A packet is a subpicture unit: two-bit pixels,
 * run-length coded in two interlaced fields, and a list of timed commands that say which four
 * palette colours and transparencies the pixel values mean, where the picture sits, and when it
 * goes on and off.
 */
import {bitmapDecodeLimits, type BitmapDecodeLimits, SubtitleDataError, bitmapLimits, checkedSize, finishCues, type BitmapCue, type BitmapRect, type BitmapTrack} from './bitmap.ts';

export type VobSubIndex = Readonly<{
  width: number; height: number; originX: number; originY: number; alpha: number;
  palette: readonly (readonly [number, number, number])[];
  /** Colours a file substitutes for the disc's (`custom colors: ON`), with which of them are transparent. */
  custom: Readonly<{transparent: readonly boolean[]; colours: readonly (readonly [number, number, number])[]}> | null;
  entries: readonly Readonly<{timeMs: number; position: number}>[];
  stream: number;
}>;

/** Reads the index. Of several languages in one file, the one `langidx` names is used, else the first. */
export function parseVobSubIndex(text: string): VobSubIndex {
  let width = 720, height = 480, originX = 0, originY = 0, alpha = 1, offset = 0;
  let palette: [number, number, number][] = [];
  let custom: VobSubIndex['custom'] = null;
  let wanted: number | null = null, current = -1;
  const tracks = new Map<number, {timeMs: number; position: number}[]>();
  const colourOf = (hex: string): [number, number, number] => { const n = parseInt(hex, 16) || 0; return [(n >> 16) & 255, (n >> 8) & 255, n & 255]; };
  for (const raw of text.slice(0, 8 * 1024 * 1024).split(/\r?\n/)) {
    const line = raw.trim();
    if (!line || line.startsWith('#')) continue;
    const colon = line.indexOf(':');
    if (colon < 1) continue;
    const key = line.slice(0, colon).trim().toLowerCase(), value = line.slice(colon + 1).trim();
    if (key === 'size') { const m = /^(\d{1,5})\s*x\s*(\d{1,5})/i.exec(value); if (m) { width = Number(m[1]); height = Number(m[2]); } }
    else if (key === 'org') { const m = /^(-?\d{1,5})\s*,\s*(-?\d{1,5})/.exec(value); if (m) { originX = Number(m[1]); originY = Number(m[2]); } }
    else if (key === 'alpha') { const m = /^(\d{1,3})\s*%/.exec(value); if (m) alpha = Math.min(100, Number(m[1])) / 100; }
    else if (key === 'palette') palette = value.split(',').slice(0, 16).map(part => colourOf(part.trim()));
    else if (key === 'custom colors') {
      const m = /^(on|off)\s*,\s*tridx\s*:\s*([01]{4})\s*,\s*colors\s*:\s*(.+)$/i.exec(value);
      if (m && m[1]!.toLowerCase() === 'on') custom = {transparent: [...m[2]!].map(c => c === '1'), colours: m[3]!.split(',').slice(0, 4).map(part => colourOf(part.trim()))};
    }
    else if (key === 'langidx') { const n = Number(value); if (Number.isInteger(n) && n >= 0 && n < 32) wanted = n; }
    else if (key === 'id') { const m = /index\s*:\s*(\d{1,2})/i.exec(value); current = m ? Number(m[1]) : current + 1; offset = 0; if (current >= 0 && current < 32 && !tracks.has(current)) tracks.set(current, []); }
    else if (key === 'time offset' || key === 'delay') { const n = Number(value); offset = Number.isFinite(n) ? n : clock(value) ?? 0; }
    else if (key === 'timestamp') {
      const m = /^(-?[\d:.,]+)\s*,\s*filepos\s*:\s*([0-9a-f]{1,12})/i.exec(value), at = m ? clock(m[1]!) : null;
      if (!m || at === null) continue;
      if (current < 0) { current = 0; tracks.set(0, []); }
      const list = tracks.get(current);
      if (list && list.length < bitmapLimits.maxCues) list.push({timeMs: at + offset, position: parseInt(m[2]!, 16)});
    }
  }
  const stream = wanted !== null && tracks.get(wanted)?.length ? wanted : [...tracks.entries()].find(([, list]) => list.length)?.[0] ?? 0;
  while (palette.length < 16) palette.push([0, 0, 0]);
  return {width, height, originX, originY, alpha, palette, custom, entries: tracks.get(stream) ?? [], stream};
}

/** `HH:MM:SS.mmm` (a leading minus allowed) to milliseconds. */
function clock(value: string): number | null {
  const m = /^(-)?(\d{1,3}):(\d{1,2}):(\d{1,2})[.:,](\d{1,3})$/.exec(value.trim());
  if (!m) return null;
  const ms = ((Number(m[2]) * 60 + Number(m[3])) * 60 + Number(m[4])) * 1000 + Number(m[5]!.padEnd(3, '0'));
  return m[1] ? -ms : ms;
}

/**
 * Collects one subpicture unit starting at `position` in the program stream: the payloads of the
 * private-stream packets that follow, until the unit's declared size is reached. The stream is
 * whichever one the first packet belongs to (the index pointed here); packets of other languages
 * in between are passed over.
 */
export function readSubpicture(sub: Uint8Array, position: number, spend: (bytes: number) => boolean = () => true): {data: Uint8Array; ptsMs: number | null} | null {
  let p = position, want = 0, have = 0, pts: number | null = null, stream = -1;
  const parts: Uint8Array[] = [];
  for (let packs = 0; packs < 64 && p + 4 <= sub.length; packs++) {
    if (!spend(14)) return null;
    if (sub[p] !== 0 || sub[p + 1] !== 0 || sub[p + 2] !== 1) return null;
    const code = sub[p + 3]!;
    if (code === 0xba) {
      // Pack header: MPEG-2 has a stuffing count in its fourteenth byte, MPEG-1 is twelve bytes.
      if (p + 14 > sub.length) return null;
      p += (sub[p + 4]! & 0xc0) === 0x40 ? 14 + (sub[p + 13]! & 7) : 12;
      continue;
    }
    if (code === 0xb9) break;
    if (p + 6 > sub.length) return null;
    const length = (sub[p + 4]! << 8) | sub[p + 5]!, end = p + 6 + length;
    if (end > sub.length || !spend(length)) return null;
    if (code === 0xbd && length >= 4) {
      const header = sub[p + 8]!, payload = p + 9 + header;
      if (payload < end && (sub[payload]! & 0xe0) === 0x20 && (stream < 0 || (sub[payload]! & 0x1f) === stream)) {
        stream = sub[payload]! & 0x1f;
        if (pts === null && (sub[p + 7]! & 0x80) && header >= 5) {
          const b = sub.subarray(p + 9, p + 14);
          pts = (((b[0]! & 0x0e) * 536870912) + ((b[1]! << 22) | ((b[2]! & 0xfe) << 14) | (b[3]! << 7) | (b[4]! >> 1))) / 90;
        }
        let part = sub.subarray(payload + 1, end);
        if (!want) { if (part.length < 2) return null; want = (part[0]! << 8) | part[1]!; if (want < 4) return null; }
        if (part.length > want - have) part = part.subarray(0, want - have);
        parts.push(part);
        have += part.length;
        if (have >= want) break;
      }
    }
    p = end;
  }
  if (!want || have < want) return null;
  if (parts.length === 1) return {data: parts[0]!, ptsMs: pts};
  const data = new Uint8Array(want);
  let at = 0;
  for (const part of parts) { data.set(part, at); at += part.length; }
  return {data, ptsMs: pts};
}

/** Colours that change part-way along lines (`CHG_COLCON`): from column `x` on, lines `first`–`last` (picture coordinates). */
type ColourChange = {first: number; last: number; points: {x: number; colours: number[]; alphas: number[]}[]};
type Control = {startMs: number | null; stopMs: number | null; forced: boolean; colours: number[]; alphas: number[]; x1: number; y1: number; x2: number; y2: number; top: number; bottom: number; changes: ColourChange[]};

/**
 * The line ranges of a `CHG_COLCON` command: each names its lines and up to eight points along
 * them where another four colours and transparencies take over, and the list ends with
 * `0f ff ff ff`. A range that does not read is left out; the rest still apply.
 */
export function readColourChanges(data: Uint8Array, from: number, to: number): ColourChange[] {
  const out: ColourChange[] = [];
  let p = from;
  while (p + 4 <= to && out.length < 64) {
    const a = (data[p]! << 8) | data[p + 1]!, b = (data[p + 2]! << 8) | data[p + 3]!;
    if (a === 0x0fff && b === 0xffff) break;
    const first = a & 0xfff, count = b >> 12, last = b & 0xfff;
    p += 4;
    const points: ColourChange['points'] = [];
    for (let i = 0; i < count && p + 6 <= to; i++, p += 6) {
      points.push({x: ((data[p]! << 8) | data[p + 1]!) & 0xfff, colours: [data[p + 3]! & 15, data[p + 3]! >> 4, data[p + 2]! & 15, data[p + 2]! >> 4], alphas: [data[p + 5]! & 15, data[p + 5]! >> 4, data[p + 4]! & 15, data[p + 4]! >> 4]});
    }
    if (points.length && last >= first) out.push({first, last, points: points.sort((x, y) => x.x - y.x)});
  }
  return out;
}

/** Reads a unit's command sequences: what the picture is, and when it goes on and off (relative to its time). */
export function readSubpictureControl(data: Uint8Array): Control | null {
  if (data.length < 4) return null;
  const first = (data[2]! << 8) | data[3]!;
  const control: Control = {startMs: null, stopMs: null, forced: false, colours: [0, 0, 0, 0], alphas: [0, 0, 0, 0], x1: 0, y1: 0, x2: -1, y2: -1, top: -1, bottom: -1, changes: []};
  let p = first;
  for (let sequences = 0; sequences < 64 && p + 4 <= data.length; sequences++) {
    const delayMs = (((data[p]! << 8) | data[p + 1]!) * 1024) / 90, next = (data[p + 2]! << 8) | data[p + 3]!;
    let q = p + 4, done = false;
    while (!done && q < (next > p && next < data.length ? next : data.length)) {
      const command = data[q++]!;
      switch (command) {
        case 0x00: control.forced = true; control.startMs ??= delayMs; break;
        case 0x01: control.startMs ??= delayMs; break;
        case 0x02: control.stopMs ??= delayMs; break;
        case 0x03: if (q + 2 > data.length) return null; control.colours = [data[q + 1]! & 15, data[q + 1]! >> 4, data[q]! & 15, data[q]! >> 4]; q += 2; break;
        case 0x04: if (q + 2 > data.length) return null; control.alphas = [data[q + 1]! & 15, data[q + 1]! >> 4, data[q]! & 15, data[q]! >> 4]; q += 2; break;
        case 0x05:
          if (q + 6 > data.length) return null;
          control.x1 = (data[q]! << 4) | (data[q + 1]! >> 4); control.x2 = ((data[q + 1]! & 15) << 8) | data[q + 2]!;
          control.y1 = (data[q + 3]! << 4) | (data[q + 4]! >> 4); control.y2 = ((data[q + 4]! & 15) << 8) | data[q + 5]!;
          q += 6; break;
        case 0x06: if (q + 4 > data.length) return null; control.top = (data[q]! << 8) | data[q + 1]!; control.bottom = (data[q + 2]! << 8) | data[q + 3]!; q += 4; break;
        case 0x07: { if (q + 2 > data.length) return null; const size = (data[q]! << 8) | data[q + 1]!; control.changes = readColourChanges(data, q + 2, Math.min(data.length, q + Math.max(2, size))); q += Math.max(2, size); break; }
        default: done = true;
      }
    }
    if (next <= p || next >= data.length) break;
    p = next;
  }
  if (control.top < 4 || control.bottom < 4 || control.top >= data.length || control.bottom >= data.length) return null;
  if (!checkedSize(control.x2 - control.x1 + 1, control.y2 - control.y1 + 1)) return null;
  return control;
}

/** One field's run-length lines into every other row of `out`, starting at row `row`. */
function decodeField(data: Uint8Array, offset: number, out: Uint8Array, width: number, height: number, row: number) {
  let nibble = offset * 2;
  const end = data.length * 2;
  const read = () => { if (nibble >= end) return 0; const byte = data[nibble >> 1]!; return nibble++ & 1 ? byte & 15 : byte >> 4; };
  for (let y = row; y < height && nibble < end; y += 2) {
    let x = 0;
    while (x < width && nibble < end) {
      let code = read();
      if (code < 4) { code = (code << 4) | read(); if (code < 0x10) { code = (code << 4) | read(); if (code < 0x40) code = (code << 4) | read(); } }
      const colour = code & 3;
      let run = code >> 2;
      if (run === 0 || run > width - x) run = width - x;
      if (colour) out.fill(colour, y * width + x, y * width + x + run);
      x += run;
    }
    nibble += nibble & 1;
  }
}

export function parseVobSub(indexText: string, sub: Uint8Array, lower: BitmapDecodeLimits = {}): BitmapTrack {
  if (sub.byteLength > bitmapLimits.maxBytes) throw new SubtitleDataError('Subtitle file is too large.');
  const limits = bitmapDecodeLimits(lower);
  let retainedBytes = sub.byteLength, workBytes = 0;
  const units = new Map<number, {unit: {data: Uint8Array; ptsMs: number | null}; control: Control} | null>();
  const index = parseVobSubIndex(indexText);
  if (!checkedSize(index.width, index.height)) return {kind: 'vobsub', width: 0, height: 0, cues: [], skipped: index.entries.length};
  let skipped = 0;
  const cues: (BitmapCue & {startMs: number; endMs: number})[] = [];
  for (const entry of index.entries) {
    if (entry.position < 0 || entry.position >= sub.length) { skipped++; continue; }
    let cached = units.get(entry.position);
    if (!units.has(entry.position)) {
      const unit = readSubpicture(sub, entry.position, count => { workBytes += count; return workBytes <= limits.maxWorkPixels; });
      if (unit) workBytes += unit.data.length;
      const control = unit && workBytes <= limits.maxWorkPixels ? readSubpictureControl(unit.data) : null;
      cached = unit && control ? {unit, control} : null;
      if (cached && retainedBytes + cached.unit.data.length > limits.maxRetainedBytes) { skipped++; continue; }
      if (cached) retainedBytes += cached.unit.data.length;
      units.set(entry.position, cached);
    }
    const unit = cached?.unit, control = cached?.control;
    if (!unit || !control) { skipped++; continue; }
    const startMs = entry.timeMs + (control.startMs ?? 0);
    const endMs = control.stopMs !== null && control.stopMs > (control.startMs ?? 0) ? entry.timeMs + control.stopMs : 0;
    cues.push({startMs, endMs, forced: control.forced, paint: () => [paintSubpicture(unit.data, control, index)].filter((r): r is BitmapRect => r !== null)});
  }
  return {kind: 'vobsub', width: index.width, height: index.height, cues: finishCues(cues), skipped};
}

function paintSubpicture(data: Uint8Array, control: Control, index: VobSubIndex): BitmapRect | null {
  const width = control.x2 - control.x1 + 1, height = control.y2 - control.y1 + 1;
  const pixels = new Uint8Array(width * height);
  decodeField(data, control.top, pixels, width, height, 0);
  decodeField(data, control.bottom, pixels, width, height, 1);
  const tableOf = (colours: readonly number[], alphas: readonly number[]) => {
    const table = new Uint8ClampedArray(16);
    for (let value = 0; value < 4; value++) {
      const custom = index.custom;
      const [r, g, b] = custom ? custom.colours[value] ?? [0, 0, 0] : index.palette[colours[value]!] ?? [0, 0, 0];
      const alpha = custom && custom.transparent[value] ? 0 : Math.round(alphas[value]! * 17 * index.alpha);
      table.set([r, g, b, alpha], value * 4);
    }
    return table;
  };
  const plain = tableOf(control.colours, control.alphas);
  // Lines with colour changes switch table at each change point (columns in picture coordinates).
  const changed = control.changes.map(change => ({...change, tables: change.points.map(point => ({x: point.x - control.x1, table: tableOf(point.colours, point.alphas)}))}));
  const rgba = new Uint8ClampedArray(width * height * 4);
  let seen = false;
  for (let y = 0; y < height; y++) {
    const line = control.y1 + y, change = changed.find(c => line >= c.first && line <= c.last);
    let table = plain, next = 0;
    for (let x = 0, i = y * width, target = i * 4; x < width; x++, i++, target += 4) {
      if (change) while (next < change.tables.length && change.tables[next]!.x <= x) table = change.tables[next++]!.table;
      const entry = pixels[i]! * 4;
      if (table[entry + 3] === 0) continue;
      seen = true;
      rgba[target] = table[entry]!; rgba[target + 1] = table[entry + 1]!; rgba[target + 2] = table[entry + 2]!; rgba[target + 3] = table[entry + 3]!;
    }
  }
  return seen ? {x: control.x1 + index.originX, y: control.y1 + index.originY, width, height, rgba, forced: control.forced} : null;
}
