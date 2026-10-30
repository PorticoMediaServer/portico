/**
 * DVB subtitles (ETSI EN 300 743), as broadcast and as kept in recordings.
 *
 * The server hands them over as a Matroska file holding the track's packets. A packet is a
 * display set: a page that lists regions and where they sit, the regions (small indexed-colour
 * canvases that list objects), colour tables, and the objects' run-length pixel data. Regions,
 * objects and colour tables stay until a page says to start afresh, so the state is carried from
 * packet to packet and what each page shows is kept, compactly, with its cue.
 *
 * An object is usually pixels. It can also be a string of characters (character-coded objects),
 * which the receiver writes in its own font in the colours the region gives it; here that is the
 * browser's sans-serif, drawn into the region's pixels like any other object. The newer progressive
 * objects (EN 300 743 V1.6.1, for UHD services) are an 8-bit indexed bitmap as PNG keeps its
 * pixels: filtered scanlines in a zlib stream, read with `inflate.ts`.
 */
import {bitmapDecodeLimits, type BitmapDecodeLimits, bitmapLimits, checkedSize, finishCues, yuvToRgb, type BitmapCue, type BitmapRect, type BitmapTrack} from './bitmap.ts';
import {readMatroskaSubtitles, type MatroskaPacket} from './matroska.ts';
import {inflateZlib, unfilterScanlines} from './inflate.ts';

/** Where an object sits in a region; a character object also names its letters' and their background's pixel codes. */
type Placement = {id: number; x: number; y: number; fg?: number; bg?: number};

/**
 * Letters drawn for a character-coded object: a mask (1 where there is ink) no larger than
 * `width`×`height`, or null when this place cannot draw text.
 */
export type TextRasterizer = (text: string, width: number, height: number) => Readonly<{width: number; height: number; mask: Uint8Array}> | null;

/** Draws with an offscreen canvas in the browser's sans-serif, a line high (at most 48 pixels). */
export const canvasText: TextRasterizer = (text, maxWidth, maxHeight) => {
  if (typeof OffscreenCanvas !== 'function' || !text.trim() || maxWidth < 1 || maxHeight < 4) return null;
  const height = Math.min(48, Math.floor(maxHeight)), size = Math.max(8, Math.round(height * 0.8));
  const measure = new OffscreenCanvas(1, 1).getContext('2d');
  if (!measure) return null;
  measure.font = `${size}px sans-serif`;
  const width = Math.min(Math.floor(maxWidth), Math.ceil(measure.measureText(text).width) + 2);
  if (width < 1) return null;
  const context = new OffscreenCanvas(width, height).getContext('2d');
  if (!context) return null;
  context.font = `${size}px sans-serif`;
  // Only the ink's coverage is read (the default fill is opaque), so no colour is chosen here.
  context.textBaseline = 'middle';
  context.fillText(text, 1, height / 2);
  const alpha = context.getImageData(0, 0, width, height).data, mask = new Uint8Array(width * height);
  for (let i = 0; i < mask.length; i++) mask[i] = alpha[i * 4 + 3]! >= 96 ? 1 : 0;
  return {width, height, mask};
};

/** A character object's codes as text: Unicode code units, with the C0 and C1 controls left out. */
export function characterText(data: Uint8Array, from: number, count: number, to: number): string {
  let text = '';
  for (let i = 0, p = from; i < count && p + 2 <= to; i++, p += 2) {
    const code = (data[p]! << 8) | data[p + 1]!;
    if (code >= 0x20 && !(code >= 0x7f && code < 0xa0) && !(code >= 0xd800 && code < 0xe000)) text += String.fromCharCode(code);
  }
  return text;
}
type Region = {width: number; height: number; depth: 2 | 4 | 8; clut: number; pixels: Uint8Array; objects: Placement[]};
type Clut = {2: Uint8ClampedArray; 4: Uint8ClampedArray; 8: Uint8ClampedArray};
type Shown = {x: number; y: number; width: number; height: number; packed: Uint8Array; table: Uint8ClampedArray};

function defaultClut(): Clut {
  const two = new Uint8ClampedArray([0, 0, 0, 0, 255, 255, 255, 255, 0, 0, 0, 255, 127, 127, 127, 255]);
  const four = new Uint8ClampedArray(16 * 4), eight = new Uint8ClampedArray(256 * 4);
  for (let i = 1; i < 16; i++) { const level = i < 8 ? 255 : 127; four.set([i & 1 ? level : 0, i & 2 ? level : 0, i & 4 ? level : 0, 255], i * 4); }
  for (let i = 1; i < 256; i++) {
    const bit = (mask: number, value: number) => (i & mask ? value : 0);
    let r: number, g: number, b: number, a = 255;
    if (i < 8) { r = bit(1, 255); g = bit(2, 255); b = bit(4, 255); a = 63; }
    else if ((i & 0x88) === 0x80) { r = 127 + bit(1, 43) + bit(0x10, 85); g = 127 + bit(2, 43) + bit(0x20, 85); b = 127 + bit(4, 43) + bit(0x40, 85); }
    else if ((i & 0x88) === 0x88) { r = bit(1, 43) + bit(0x10, 85); g = bit(2, 43) + bit(0x20, 85); b = bit(4, 43) + bit(0x40, 85); }
    else { r = bit(1, 85) + bit(0x10, 170); g = bit(2, 85) + bit(0x20, 170); b = bit(4, 85) + bit(0x40, 170); if ((i & 0x88) === 0x08) a = 127; }
    eight.set([r, g, b, a], i * 4);
  }
  return {2: two, 4: four, 8: eight};
}

/**
 * A region as kept with its cue. Most of a region is one colour, so it is stored as runs
 * (value, length low, length high) behind a 1; a region that would not shrink is stored as it is
 * behind a 0, so the kept copy is never larger than the region.
 */
export function packRuns(pixels: Uint8Array): Uint8Array {
  const out = new Uint8Array(pixels.length + 1);
  out[0] = 1;
  let size = 1;
  for (let i = 0; i < pixels.length;) {
    const value = pixels[i]!;
    let run = 1;
    while (i + run < pixels.length && run < 65535 && pixels[i + run] === value) run++;
    if (size + 3 > out.length) { out[0] = 0; out.set(pixels, 1); return out; }
    out[size++] = value; out[size++] = run & 255; out[size++] = run >> 8;
    i += run;
  }
  return out.slice(0, size);
}
export function unpackRuns(packed: Uint8Array, length: number): Uint8Array {
  const out = new Uint8Array(length);
  if (packed[0] === 0) { out.set(packed.subarray(1, 1 + length)); return out; }
  for (let i = 1, at = 0; i + 2 < packed.length && at < length; i += 3) {
    const run = packed[i + 1]! | (packed[i + 2]! << 8);
    if (packed[i]) out.fill(packed[i]!, at, Math.min(length, at + run));
    at += run;
  }
  return out;
}

class Bits {
  private bit = 0;
  private readonly data: Uint8Array;
  private readonly from: number;
  private readonly to: number;
  constructor(data: Uint8Array, from: number, to: number) { this.data = data; this.from = from; this.to = to; }
  get ended() { return this.from + (this.bit >> 3) >= this.to; }
  read(count: number): number {
    let value = 0;
    for (let i = 0; i < count; i++, this.bit++) {
      const at = this.from + (this.bit >> 3);
      value = (value << 1) | (at < this.to ? (this.data[at]! >> (7 - (this.bit & 7))) & 1 : 0);
    }
    return value;
  }
  /** The first byte after what has been read. */
  get next() { return Math.min(this.to, this.from + ((this.bit + 7) >> 3)); }
}

/**
 * One coded line of pixels (`bitsPerPixel` 2, 4 or 8) written into `row` from `x`. Returns where
 * the data continues and the new x. Pixel value 1 is left alone when `keepOne` is set, and
 * `map` widens a narrow code to the region's depth.
 */
export function readDvbString(data: Uint8Array, from: number, to: number, bitsPerPixel: 2 | 4 | 8, row: Uint8Array, x: number, keepOne: boolean, map: readonly number[] | null): {next: number; x: number} {
  const bits = new Bits(data, from, to);
  const put = (code: number, run: number) => {
    if (!(keepOne && code === 1)) { const value = map ? map[code] ?? 0 : code, end = Math.min(row.length, x + run); if (x < end) row.fill(value, x, end); }
    x += run;
  };
  while (!bits.ended) {
    if (bitsPerPixel === 8) {
      const code = bits.read(8);
      if (code) { put(code, 1); continue; }
      const flagged = bits.read(1), run = bits.read(7);
      if (!flagged) { if (!run) break; put(0, run); } else put(bits.read(8), run);
    } else if (bitsPerPixel === 4) {
      const code = bits.read(4);
      if (code) { put(code, 1); continue; }
      if (!bits.read(1)) { const run = bits.read(3); if (!run) break; put(0, run + 2); }
      else if (!bits.read(1)) { const run = bits.read(2) + 4; put(bits.read(4), run); }
      else {
        const kind = bits.read(2);
        if (kind === 0) put(0, 1); else if (kind === 1) put(0, 2);
        else if (kind === 2) { const run = bits.read(4) + 9; put(bits.read(4), run); }
        else { const run = bits.read(8) + 25; put(bits.read(4), run); }
      }
    } else {
      const code = bits.read(2);
      if (code) { put(code, 1); continue; }
      if (bits.read(1)) { const run = bits.read(3) + 3; put(bits.read(2), run); }
      else if (bits.read(1)) put(0, 1);
      else {
        const kind = bits.read(2);
        if (kind === 0) break;
        if (kind === 1) put(0, 2);
        else if (kind === 2) { const run = bits.read(4) + 12; put(bits.read(2), run); }
        else { const run = bits.read(8) + 29; put(bits.read(2), run); }
      }
    }
  }
  return {next: bits.next, x};
}

/** Draws one field of an object into a region: lines `y`, `y + 2`, … from `x`. */
function drawField(data: Uint8Array, from: number, to: number, region: Region, x0: number, y0: number, keepOne: boolean) {
  let p = from, x = x0, y = y0;
  let map2to4 = [0x0, 0x7, 0x8, 0xf], map2to8 = [0x00, 0x77, 0x88, 0xff], map4to8 = Array.from({length: 16}, (_, i) => i * 0x11);
  while (p < to) {
    const type = data[p++]!;
    if (type === 0xf0) { x = x0; y += 2; continue; }
    if (type === 0x20) { if (p + 2 > to) return; map2to4 = [data[p]! >> 4, data[p]! & 15, data[p + 1]! >> 4, data[p + 1]! & 15]; p += 2; continue; }
    if (type === 0x21) { if (p + 4 > to) return; map2to8 = [...data.subarray(p, p + 4)]; p += 4; continue; }
    if (type === 0x22) { if (p + 16 > to) return; map4to8 = [...data.subarray(p, p + 16)]; p += 16; continue; }
    if (type !== 0x10 && type !== 0x11 && type !== 0x12) return;
    const coded = type === 0x10 ? 2 : type === 0x11 ? 4 : 8;
    if (coded > region.depth) return;
    const map = coded === region.depth ? null : coded === 2 ? (region.depth === 4 ? map2to4 : map2to8) : map4to8;
    // A line outside the region is still read, so the data stays in step; it is written nowhere.
    const row = y >= 0 && y < region.height ? region.pixels.subarray(y * region.width, (y + 1) * region.width) : new Uint8Array(0);
    const result = readDvbString(data, p, to, coded, row, x, keepOne, map);
    p = result.next; x = result.x;
  }
}

export function parseDvbPackets(packets: readonly MatroskaPacket[], pages: readonly number[] | null, lower: BitmapDecodeLimits = {}, text: TextRasterizer = canvasText): BitmapTrack {
  const limits = bitmapDecodeLimits(lower);
  let statePixels = 0, retainedBytes = 0, workPixels = 0;
  const work = (count: number) => { workPixels += count; return workPixels <= limits.maxWorkPixels; };
  const removeRegion = (id: number) => { statePixels -= regions.get(id)?.pixels.length ?? 0; regions.delete(id); };
  let regions = new Map<number, Region>(), cluts = new Map<number, Clut>();
  let display = {width: 720, height: 576, x: 0, y: 0};
  let canvasWidth = 0, canvasHeight = 0, skipped = 0;
  const cues: (BitmapCue & {startMs: number; endMs: number})[] = [];
  let open = null as {startMs: number; endMs: number} | null;
  const clutOf = (id: number) => { let c = cluts.get(id); if (!c && cluts.size < 256) { c = defaultClut(); cluts.set(id, c); } return c ?? defaultClut(); };

  packetsLoop: for (const packet of packets) {
    if (cues.length >= bitmapLimits.maxCues) { skipped++; break; }
    const data = packet.data, view = new DataView(data.buffer, data.byteOffset, data.byteLength);
    let p = data.length >= 2 && data[0] === 0x20 && data[1] === 0x00 ? 2 : 0;
    let page = null as {timeout: number; list: Placement[]} | null;
    while (p + 6 <= data.length && data[p] === 0x0f) {
      const type = data[p + 1]!, pageId = view.getUint16(p + 2), length = view.getUint16(p + 4), body = p + 6, end = body + length;
      if (end > data.length) { skipped++; break; }
      p = end;
      if (pages && pages.length && !pages.includes(pageId)) continue;
      if (type === 0x10 && length >= 2) {
        const state = (data[body + 1]! >> 2) & 3;
        if (state === 1 || state === 2) { regions = new Map(); cluts = new Map(); statePixels = 0; }
        page = {timeout: data[body]!, list: []};
        for (let q = body + 2; q + 6 <= end && page.list.length < bitmapLimits.maxPictures; q += 6) page.list.push({id: data[q]!, x: view.getUint16(q + 2), y: view.getUint16(q + 4)});
      } else if (type === 0x11 && length >= 10) {
        const id = data[body]!, fill = (data[body + 1]! >> 3) & 1, width = view.getUint16(body + 2), height = view.getUint16(body + 4);
        const depthCode = (data[body + 6]! >> 2) & 7, depth = depthCode === 1 ? 2 : depthCode === 2 ? 4 : depthCode === 3 ? 8 : 0;
        if (!depth || !checkedSize(width, height) || (!regions.has(id) && regions.size >= 256)) { removeRegion(id); skipped++; continue; }
        let region = regions.get(id);
        const fresh = !region || region.width !== width || region.height !== height;
        if (fresh) {
          const count = width * height;
          if (statePixels - (region?.pixels.length ?? 0) + count > limits.maxStatePixels || !work(count)) { removeRegion(id); skipped++; continue; }
          statePixels += count - (region?.pixels.length ?? 0);
        }
        if (!region || fresh) region = {width, height, depth, clut: 0, pixels: new Uint8Array(width * height), objects: []};
        region.depth = depth;
        region.clut = data[body + 7]!;
        const background = depth === 8 ? data[body + 8]! : depth === 4 ? data[body + 9]! >> 4 : (data[body + 9]! >> 2) & 3;
        if (fill || fresh) { if (!fresh && !work(region.pixels.length)) { removeRegion(id); skipped++; continue; } region.pixels.fill(background); }
        region.objects = [];
        for (let q = body + 10; q + 6 <= end && region.objects.length < 256;) {
          const kind = data[q + 2]! >> 6, letters = (kind === 1 || kind === 2) && q + 8 <= end;
          region.objects.push({id: view.getUint16(q), x: view.getUint16(q + 2) & 0xfff, y: view.getUint16(q + 4) & 0xfff, ...(letters ? {fg: data[q + 6]!, bg: data[q + 7]!} : {})});
          q += kind === 1 || kind === 2 ? 8 : 6;
        }
        regions.set(id, region);
      } else if (type === 0x12 && length >= 2) {
        const clut = clutOf(data[body]!);
        for (let q = body + 2; q + 4 <= end;) {
          const entry = data[q]!, flags = data[q + 1]!;
          let y: number, cr: number, cb: number, t: number;
          if (flags & 1) { if (q + 6 > end) break; y = data[q + 2]!; cr = data[q + 3]!; cb = data[q + 4]!; t = data[q + 5]!; q += 6; }
          else { const a = data[q + 2]!, b = data[q + 3]!; y = a & 0xfc; cr = (((a & 3) << 2) | (b >> 6)) << 4; cb = (b << 2) & 0xf0; t = (b << 6) & 0xc0; q += 4; }
          const [r, g, bl] = yuvToRgb(y, cb, cr, false), colour = [r, g, bl, y === 0 ? 0 : 255 - t];
          if (flags & 0x80 && entry < 4) clut[2].set(colour, entry * 4);
          if (flags & 0x40 && entry < 16) clut[4].set(colour, entry * 4);
          if (flags & 0x20) clut[8].set(colour, entry * 4);
        }
      } else if (type === 0x13 && length >= 7) {
        const id = view.getUint16(body), method = (data[body + 2]! >> 2) & 3, keepOne = ((data[body + 2]! >> 1) & 1) === 1;
        if (method === 1 && length >= 4) {
          // A string of characters, written where the region places it in the region's own colours.
          const letters = characterText(data, body + 4, data[body + 3]!, end);
          for (const region of regions.values()) for (const placed of region.objects) {
            if (placed.id !== id || !letters) continue;
            const mask = placed.x < region.width && placed.y < region.height ? text(letters, region.width - placed.x, region.height - placed.y) : null;
            if (!mask || !work(mask.width * mask.height)) { skipped++; continue; }
            const top = region.depth === 2 ? 3 : region.depth === 4 ? 15 : 255, fg = (placed.fg ?? 1) & top, bg = (placed.bg ?? 0) & top;
            for (let y = 0; y < mask.height && placed.y + y < region.height; y++)
              for (let x = 0; x < mask.width && placed.x + x < region.width; x++) region.pixels[(placed.y + y) * region.width + placed.x + x] = mask.mask[y * mask.width + x] ? fg : bg;
          }
          continue;
        }
        if (method === 2 && length >= 9) {
          // A progressive bitmap: width, height, then that many bytes of zlib-compressed scanlines.
          const width = view.getUint16(body + 3), height = view.getUint16(body + 5), size = view.getUint16(body + 7);
          if (!checkedSize(width, height) || body + 9 + size > end || !work(width * height * 2)) { skipped++; continue; }
          const raw = inflateZlib(data.subarray(body + 9, body + 9 + size), height * (width + 1));
          const pixels = raw ? unfilterScanlines(raw, width, height) : null;
          if (!pixels) { skipped++; continue; }
          for (const region of regions.values()) for (const placed of region.objects) {
            if (placed.id !== id || placed.x >= region.width || placed.y >= region.height) continue;
            if (!work(width * height)) { skipped++; break packetsLoop; }
            const top = region.depth === 2 ? 3 : region.depth === 4 ? 15 : 255;
            for (let y = 0; y < height && placed.y + y < region.height; y++)
              for (let x = 0; x < width && placed.x + x < region.width; x++) region.pixels[(placed.y + y) * region.width + placed.x + x] = pixels[y * width + x]! & top;
          }
          continue;
        }
        if (method !== 0) { skipped++; continue; }
        const top = view.getUint16(body + 3), bottom = view.getUint16(body + 5), first = body + 7;
        if (first + top + bottom > end) { skipped++; continue; }
        for (const region of regions.values()) for (const placed of region.objects) {
          if (!work(1)) { skipped++; break packetsLoop; }
          if (placed.id !== id) continue;
          if (!work((top + bottom) * 8 + region.pixels.length * 2)) { skipped++; break packetsLoop; }
          drawField(data, first, first + top, region, placed.x, placed.y, keepOne);
          // An object with no second field repeats its first on the lines between.
          if (bottom) drawField(data, first + top, first + top + bottom, region, placed.x, placed.y + 1, keepOne);
          else drawField(data, first, first + top, region, placed.x, placed.y + 1, keepOne);
        }
      } else if (type === 0x14 && length >= 5) {
        const width = view.getUint16(body + 1) + 1, height = view.getUint16(body + 3) + 1;
        if (checkedSize(width, height)) display = {width, height, x: data[body]! & 8 && length >= 13 ? view.getUint16(body + 5) : 0, y: data[body]! & 8 && length >= 13 ? view.getUint16(body + 9) : 0};
      }
    }
    if (!page) continue;
    const at = packet.timeMs;
    if (open && open.endMs > at) open.endMs = at;
    open = null;
    const shown: Shown[] = [];
    let framePixels = 0;
    for (const entry of page.list) {
      const region = regions.get(entry.id);
      if (!region) continue;
      const count = region.pixels.length, table = clutOf(region.clut)[region.depth];
      if (framePixels + count > limits.maxFramePixels || retainedBytes + count + 1 + table.byteLength > limits.maxRetainedBytes || !work(count)) { skipped++; continue; }
      const packed = packRuns(region.pixels);
      framePixels += count;
      retainedBytes += packed.byteLength + table.byteLength;
      shown.push({x: entry.x + display.x, y: entry.y + display.y, width: region.width, height: region.height, packed, table: table.slice()});
    }
    if (!shown.length) continue;
    if (cues.length >= bitmapLimits.maxCues) { skipped++; continue; }
    if (display.width * display.height > canvasWidth * canvasHeight) { canvasWidth = display.width; canvasHeight = display.height; }
    const cue = {startMs: at, endMs: at + (page.timeout ? page.timeout * 1000 : bitmapLimits.orphanMs), forced: false, paint: () => shown.map(paintRegion).filter((r): r is BitmapRect => r !== null)};
    cues.push(cue);
    open = cue;
  }
  return {kind: 'dvb', width: canvasWidth, height: canvasHeight, cues: finishCues(cues), skipped};
}

function paintRegion(shown: Shown): BitmapRect | null {
  const pixels = unpackRuns(shown.packed, shown.width * shown.height), table = shown.table;
  const rgba = new Uint8ClampedArray(pixels.length * 4);
  let seen = false;
  for (let i = 0, target = 0; i < pixels.length; i++, target += 4) {
    const entry = pixels[i]! * 4;
    if (!table[entry + 3]) continue;
    seen = true;
    rgba[target] = table[entry]!; rgba[target + 1] = table[entry + 1]!; rgba[target + 2] = table[entry + 2]!; rgba[target + 3] = table[entry + 3]!;
  }
  return seen ? {x: shown.x, y: shown.y, width: shown.width, height: shown.height, rgba, forced: false} : null;
}

/** DVB subtitles from the Matroska file the server delivers. */
export function parseDvb(bytes: Uint8Array): BitmapTrack {
  const file = readMatroskaSubtitles(bytes);
  const extra = file.codecPrivate;
  const pages = extra && extra.length >= 4 ? [(extra[0]! << 8) | extra[1]!, (extra[2]! << 8) | extra[3]!] : null;
  return parseDvbPackets(file.packets, pages);
}
