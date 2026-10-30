/* Small image-subtitle files built in memory: the encoders the decoders' tests read back. */

export const bytes = (...parts: (number | number[] | Uint8Array)[]) => Uint8Array.from(parts.flatMap(p => typeof p === 'number' ? [p] : [...p]));
export const u16 = (n: number) => [(n >> 8) & 255, n & 255];
export const u32 = (n: number) => [(n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255];

/** Rows of palette indexes ("0012" …, one character per pixel, base 36). */
export const picture = (...rows: string[]) => ({width: rows[0]!.length, height: rows.length, pixels: rows.flatMap(row => [...row].map(c => parseInt(c, 36)))});
function runs(row: number[]): [number, number][] {
  const out: [number, number][] = [];
  for (const value of row) { const last = out[out.length - 1]; if (last && last[0] === value) last[1]++; else out.push([value, 1]); }
  return out;
}
const rowsOf = (p: {width: number; height: number; pixels: number[]}) => Array.from({length: p.height}, (_, y) => p.pixels.slice(y * p.width, (y + 1) * p.width));

// ── PGS ────────────────────────────────────────────────────────────────
export function pgsRuns(p: ReturnType<typeof picture>): number[] {
  const out: number[] = [];
  for (const row of rowsOf(p)) {
    for (const [colour, run] of runs(row)) {
      if (colour && run < 3) for (let i = 0; i < run; i++) out.push(colour);
      else if (run < 64) out.push(0, (colour ? 0x80 : 0) | run, ...(colour ? [colour] : []));
      else out.push(0, (colour ? 0xc0 : 0x40) | (run >> 8), run & 255, ...(colour ? [colour] : []));
    }
    out.push(0, 0);
  }
  return out;
}
export const pgsSegment = (type: number, ptsMs: number, payload: number[]) => [0x50, 0x47, ...u32(ptsMs * 90), ...u32(0), type, ...u16(payload.length), ...payload];
export type PgsPlaced = {id: number; window?: number; x: number; y: number; forced?: boolean; crop?: [number, number, number, number]};
export const pgsPresentation = (ptsMs: number, size: [number, number], state: 'epoch' | 'acquire' | 'normal', palette: number, objects: PgsPlaced[], paletteOnly = false) =>
  pgsSegment(0x16, ptsMs, [...u16(size[0]), ...u16(size[1]), 0x10, 0, 0, state === 'epoch' ? 0x80 : state === 'acquire' ? 0x40 : 0, paletteOnly ? 0x80 : 0, palette, objects.length,
    ...objects.flatMap(o => [...u16(o.id), o.window ?? 0, (o.crop ? 0x80 : 0) | (o.forced ? 0x40 : 0), ...u16(o.x), ...u16(o.y), ...(o.crop ? o.crop.flatMap(u16) : [])])]);
export const pgsWindows = (ptsMs: number, windows: [number, number, number, number, number][]) => pgsSegment(0x17, ptsMs, [windows.length, ...windows.flatMap(([id, x, y, w, h]) => [id, ...u16(x), ...u16(y), ...u16(w), ...u16(h)])]);
/** Entries are [index, Y, Cr, Cb, alpha]. */
export const pgsPalette = (ptsMs: number, id: number, entries: number[][]) => pgsSegment(0x14, ptsMs, [id, 0, ...entries.flat()]);
export function pgsObject(ptsMs: number, id: number, p: ReturnType<typeof picture>, pieces = 1): number[] {
  const data = pgsRuns(p), out: number[] = [], step = Math.ceil(data.length / pieces);
  for (let i = 0; i < pieces; i++) {
    const part = data.slice(i * step, (i + 1) * step), first = i === 0, last = i === pieces - 1;
    out.push(...pgsSegment(0x15, ptsMs, [...u16(id), 0, (first ? 0x80 : 0) | (last ? 0x40 : 0), ...(first ? [((data.length + 4) >> 16) & 255, ((data.length + 4) >> 8) & 255, (data.length + 4) & 255, ...u16(p.width), ...u16(p.height)] : []), ...part]));
  }
  return out;
}
export const pgsEnd = (ptsMs: number) => pgsSegment(0x80, ptsMs, []);

// ── VobSub ─────────────────────────────────────────────────────────────
function vobSubField(rows: number[][]): number[] {
  const out: number[] = [];
  for (const row of rows) {
    const nibbles: number[] = [];
    const all = runs(row);
    all.forEach(([colour, run], i) => {
      if (i === all.length - 1 && run > 3) { nibbles.push(0, 0, 0, colour); return; }
      let left = run;
      while (left > 0) {
        const n = Math.min(left, 255), code = (n << 2) | colour;
        if (n < 4) nibbles.push(code); else if (n < 16) nibbles.push(code >> 4, code & 15); else if (n < 64) nibbles.push(code >> 8, (code >> 4) & 15, code & 15); else nibbles.push(code >> 12, (code >> 8) & 15, (code >> 4) & 15, code & 15);
        left -= n;
      }
    });
    if (nibbles.length & 1) nibbles.push(0);
    for (let i = 0; i < nibbles.length; i += 2) out.push((nibbles[i]! << 4) | nibbles[i + 1]!);
  }
  return out;
}
export type SubpictureOptions = {x: number; y: number; colours: [number, number, number, number]; alphas: [number, number, number, number]; stopAfterMs?: number; forced?: boolean; /** Further commands in the first sequence (a CHG_COLCON). */ extra?: number[]};
/** One subpicture unit. `colours` and `alphas` are for pixel values 0 to 3. */
export function subpicture(p: ReturnType<typeof picture>, o: SubpictureOptions): number[] {
  const rows = rowsOf(p), top = vobSubField(rows.filter((_, y) => y % 2 === 0)), bottom = vobSubField(rows.filter((_, y) => y % 2 === 1));
  const control = 4 + top.length + bottom.length, x2 = o.x + p.width - 1, y2 = o.y + p.height - 1;
  const nib = (v: number[]) => [(v[3]! << 4) | v[2]!, (v[1]! << 4) | v[0]!];
  const first = [0x03, ...nib(o.colours), 0x04, ...nib(o.alphas), 0x05, o.x >> 4, ((o.x & 15) << 4) | (x2 >> 8), x2 & 255, o.y >> 4, ((o.y & 15) << 4) | (y2 >> 8), y2 & 255, 0x06, ...u16(4), ...u16(4 + top.length), ...(o.extra ?? []), o.forced ? 0x00 : 0x01, 0xff];
  const second = control + 4 + first.length;
  const stop = o.stopAfterMs === undefined ? [] : [...u16(Math.round(o.stopAfterMs * 90 / 1024)), ...u16(second), 0x02, 0xff];
  const body = [...top, ...bottom, ...u16(0), ...u16(stop.length ? second : control), ...first, ...stop];
  return [...u16(body.length + 4), ...u16(control), ...body];
}
/** A unit as program-stream packs of at most `chunk` payload bytes, for substream `stream`. */
export function programStream(unit: number[], ptsMs: number, stream = 0, chunk = 2000): number[] {
  const out: number[] = [];
  for (let at = 0, first = true; at < unit.length; at += chunk, first = false) {
    const part = unit.slice(at, at + chunk), pts = Math.round(ptsMs * 90);
    const stamp = first ? [0x21 | ((Math.floor(pts / 2 ** 30) & 7) << 1), (pts >> 22) & 255, ((pts >> 14) & 0xfe) | 1, (pts >> 7) & 255, ((pts << 1) & 0xfe) | 1] : [];
    const pes = [0x81, first ? 0x80 : 0x00, stamp.length, ...stamp, 0x20 + stream, ...part];
    out.push(0, 0, 1, 0xba, 0x44, 0, 4, 0, 4, 1, 1, 0x89, 0xc3, 0xf8, 0, 0, 1, 0xbd, ...u16(pes.length), ...pes);
  }
  return out;
}
export const stamp = (ms: number) => { const p = (n: number, w = 2) => String(n).padStart(w, '0'); return `${p(Math.floor(ms / 3600000))}:${p(Math.floor(ms / 60000) % 60)}:${p(Math.floor(ms / 1000) % 60)}:${p(ms % 1000, 3)}`; };

// ── DVB and its Matroska file ──────────────────────────────────────────
export class BitWriter {
  private bits: number[] = [];
  put(value: number, count: number) { for (let i = count - 1; i >= 0; i--) this.bits.push((value >> i) & 1); return this; }
  bytes(): number[] { const out: number[] = []; for (let i = 0; i < this.bits.length; i += 8) out.push(parseInt(this.bits.slice(i, i + 8).join('').padEnd(8, '0'), 2)); return out; }
}
export const dvbSegment = (type: number, page: number, payload: number[]) => [0x0f, type, ...u16(page), ...u16(payload.length), ...payload];
export const dvbPage = (page: number, timeout: number, state: number, regions: [number, number, number][]) => dvbSegment(0x10, page, [timeout, state << 2, ...regions.flatMap(([id, x, y]) => [id, 0, ...u16(x), ...u16(y)])]);
export const dvbRegion = (page: number, id: number, size: [number, number], depth: 2 | 4 | 8, clut: number, background: number, objects: [number, number, number][], fill = true) =>
  dvbSegment(0x11, page, [id, fill ? 8 : 0, ...u16(size[0]), ...u16(size[1]), (depth === 2 ? 1 : depth === 4 ? 2 : 3) << 2, clut, depth === 8 ? background : 0, depth === 4 ? background << 4 : depth === 2 ? background << 2 : 0, ...objects.flatMap(([object, x, y]) => [...u16(object), ...u16(x), ...u16(y)])]);
/** Full-range entries are [index, Y, Cr, Cb, transparency] for the table of `depth`. */
export const dvbClut = (page: number, id: number, depth: 2 | 4 | 8, entries: number[][]) => dvbSegment(0x12, page, [id, 0, ...entries.flatMap(([entry, ...colour]) => [entry!, (depth === 2 ? 0x80 : depth === 4 ? 0x40 : 0x20) | 1, ...colour])]);
/** An object from already coded lines (each a data type byte and its string), one list per field. */
export const dvbObject = (page: number, id: number, top: number[], bottom: number[], keepOne = false) => dvbSegment(0x13, page, [...u16(id), keepOne ? 2 : 0, ...u16(top.length), ...u16(bottom.length), ...top, ...bottom]);
/** A line of 8-bit codes: each pixel on its own, then end of string and end of line. */
export const dvbLine8 = (row: number[]) => [0x12, ...runs(row).flatMap(([colour, run]) => colour && run < 3 ? Array(run).fill(colour) : colour ? [0, 0x80 | run, colour] : [0, run]), 0, 0, 0xf0];

const size = (n: number) => n < 127 ? [0x80 | n] : [0x40 | (n >> 8), n & 255];
const element = (id: number[], payload: number[]) => [...id, ...size(payload.length), ...payload];
/** A Matroska file with one subtitle track; packets are [time in ms, bytes, duration or null]. The segment's size is left open, as when written to a pipe. */
export function matroska(codec: string, codecPrivate: number[], packets: [number, number[], number | null][]): Uint8Array {
  const info = element([0x15, 0x49, 0xa9, 0x66], element([0x2a, 0xd7, 0xb1], [0x0f, 0x42, 0x40]));
  const tracks = element([0x16, 0x54, 0xae, 0x6b], element([0xae], [...element([0xd7], [1]), ...element([0x83], [0x11]), ...element([0x86], [...codec].map(c => c.charCodeAt(0))), ...element([0x63, 0xa2], codecPrivate)]));
  const clusters = packets.flatMap(([time, data, duration]) => {
    const block = [0x81, 0, 0, 0, ...data];
    return element([0x1f, 0x43, 0xb6, 0x75], [...element([0xe7], u32(time)), ...(duration === null ? element([0xa3], block) : element([0xa0], [...element([0xa1], block), ...element([0x9b], u16(duration))]))]);
  });
  return bytes(element([0x1a, 0x45, 0xdf, 0xa3], element([0x42, 0x82], [...'matroska'].map(c => c.charCodeAt(0)))), [0x18, 0x53, 0x80, 0x67, 0x01, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff], info, tracks, clusters);
}
