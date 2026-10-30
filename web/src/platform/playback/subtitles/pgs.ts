/**
 * Blu-ray subtitles (PGS, a `.sup` file): presentation, window, palette and object segments.
 *
 * A display set is a presentation segment, the definitions that changed, and an end segment. An
 * epoch start forgets everything; otherwise objects and palettes stay until redefined, so a set
 * may show an object defined earlier, or only swap the palette (a fade). What is on screen at a
 * moment is the last presentation segment's object list, each object cropped as the segment
 * says, clipped to its window, coloured through the palette the segment names.
 */
import {bitmapDecodeLimits, type BitmapDecodeLimits, SubtitleDataError, bitmapLimits, checkedSize, finishCues, yuvToRgb, type BitmapCue, type BitmapRect, type BitmapTrack} from './bitmap.ts';

/** `parts` are views of the file itself until the object is complete: nothing is allocated for a size a file merely claims. */
type PgsObject = {width: number; height: number; expected: number; parts: Uint8Array[]; data: Uint8Array; length: number; complete: boolean};
type Window = {x: number; y: number; width: number; height: number};
type Composition = {id: number; window: number; x: number; y: number; forced: boolean; crop?: Window};
type DisplaySet = {pts: number; width: number; height: number; palette: number; list: Composition[]};
type Placed = {object: PgsObject; x: number; y: number; forced: boolean; crop?: Window; window?: Window};

const PCS = 0x16, WDS = 0x17, PDS = 0x14, ODS = 0x15, END = 0x80;

/** Run-length data to palette indexes, never writing outside `width × height`. */
export function decodePgsRuns(data: Uint8Array, width: number, height: number): Uint8Array {
  if (!checkedSize(width, height)) throw new SubtitleDataError('Subtitle picture is too large.');
  const out = new Uint8Array(width * height);
  let p = 0, x = 0, y = 0;
  while (p < data.length && y < height) {
    const first = data[p++]!;
    let run = 1, colour = first;
    if (first === 0) {
      const flags = data[p++] ?? 0;
      run = flags & 0x3f;
      if (flags & 0x40) run = (run << 8) | (data[p++] ?? 0);
      colour = flags & 0x80 ? (data[p++] ?? 0) : 0;
      if (run === 0) { x = 0; y++; continue; }
    }
    const room = width - x, n = run < room ? run : room;
    if (colour !== 0 && n > 0) out.fill(colour, y * width + x, y * width + x + n);
    x += run;
  }
  return out;
}

export function parsePgs(bytes: Uint8Array, lower: BitmapDecodeLimits = {}): BitmapTrack {
  if (bytes.byteLength > bitmapLimits.maxBytes) throw new SubtitleDataError('Subtitle file is too large.');
  const limits = bitmapDecodeLimits(lower);
  let retainedBytes = bytes.byteLength;
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const objects = new Map<number, PgsObject>(), palettes = new Map<number, Uint8ClampedArray>(), windows = new Map<number, Window>();
  let canvasWidth = 0, canvasHeight = 0, skipped = 0;
  let pending = null as DisplaySet | null;
  const cues: (BitmapCue & {startMs: number; endMs: number})[] = [];
  let open = null as {startMs: number; endMs: number} | null;

  const present = () => {
    const set = pending;
    pending = null;
    if (!set) return;
    const at = set.pts / 90;
    if (open && !(open.endMs > open.startMs)) open.endMs = at;
    open = null;
    if (!checkedSize(set.width, set.height)) { skipped++; return; }
    if (set.width * set.height > canvasWidth * canvasHeight) { canvasWidth = set.width; canvasHeight = set.height; }
    const table = palettes.get(set.palette);
    const placed: Placed[] = [];
    let framePixels = 0;
    for (const entry of set.list) {
      const object = objects.get(entry.id);
      if (!object || !object.complete) { skipped++; continue; }
      if (framePixels + object.width * object.height > limits.maxFramePixels) { skipped++; continue; }
      framePixels += object.width * object.height;
      placed.push({object, x: entry.x, y: entry.y, forced: entry.forced, crop: entry.crop, window: windows.get(entry.window)});
    }
    if (!placed.length || !table) return;
    if (cues.length >= bitmapLimits.maxCues) { skipped++; return; }
    // The palette is copied: a later set may redefine it while this cue is still waiting to be painted.
    if (retainedBytes + table.byteLength > limits.maxRetainedBytes) { skipped++; return; }
    retainedBytes += table.byteLength;
    const palette = table.slice(), frame = {width: set.width, height: set.height};
    const cue = {startMs: at, endMs: 0, forced: placed.every(p => p.forced), paint: () => placed.map(p => paintObject(p, palette, frame)).filter((r): r is BitmapRect => r !== null)};
    cues.push(cue);
    open = cue;
  };

  let p = 0;
  while (p + 13 <= bytes.length) {
    if (bytes[p] !== 0x50 || bytes[p + 1] !== 0x47) {
      // Not a segment: look for the next one rather than giving the file up.
      skipped++;
      let next = p + 1;
      while (next + 1 < bytes.length && !(bytes[next] === 0x50 && bytes[next + 1] === 0x47)) next++;
      p = next;
      continue;
    }
    const pts = view.getUint32(p + 2), type = bytes[p + 10]!, size = view.getUint16(p + 11), body = p + 13;
    if (body + size > bytes.length) { skipped++; break; }
    p = body + size;
    if (type === PCS) {
      if (size < 11) { skipped++; continue; }
      const state = bytes[body + 7]! & 0xc0, count = bytes[body + 10]!;
      if (state === 0x80) { objects.clear(); palettes.clear(); windows.clear(); }
      const list: Composition[] = [];
      let q = body + 11;
      for (let i = 0; i < count && list.length < bitmapLimits.maxPictures; i++) {
        if (q + 8 > body + size) break;
        const flags = bytes[q + 3]!, cropped = (flags & 0x80) !== 0;
        const entry: Composition = {id: view.getUint16(q), window: bytes[q + 2]!, x: view.getUint16(q + 4), y: view.getUint16(q + 6), forced: (flags & 0x40) !== 0};
        q += 8;
        if (cropped) {
          if (q + 8 > body + size) break;
          entry.crop = {x: view.getUint16(q), y: view.getUint16(q + 2), width: view.getUint16(q + 4), height: view.getUint16(q + 6)};
          q += 8;
        }
        list.push(entry);
      }
      pending = {pts, width: view.getUint16(body), height: view.getUint16(body + 2), palette: bytes[body + 9]!, list};
    } else if (type === WDS) {
      const count = size ? bytes[body]! : 0;
      for (let i = 0, q = body + 1; i < count && q + 9 <= body + size; i++, q += 9)
        windows.set(bytes[q]!, {x: view.getUint16(q + 1), y: view.getUint16(q + 3), width: view.getUint16(q + 5), height: view.getUint16(q + 7)});
    } else if (type === PDS) {
      if (size < 2) { skipped++; continue; }
      const id = bytes[body]!;
      // A palette update changes entries of the palette that is there; an epoch start cleared it.
      const table = palettes.get(id) ?? new Uint8ClampedArray(256 * 4);
      const hd = (pending?.height ?? canvasHeight) >= 600;
      for (let q = body + 2; q + 5 <= body + size; q += 5) {
        const [r, g, b] = yuvToRgb(bytes[q + 1]!, bytes[q + 3]!, bytes[q + 2]!, hd);
        table.set([r, g, b, bytes[q + 4]!], bytes[q]! * 4);
      }
      palettes.set(id, table);
    } else if (type === ODS) {
      if (size < 4) { skipped++; continue; }
      const id = view.getUint16(body), sequence = bytes[body + 3]!;
      let from = body + 4;
      if (sequence & 0x80) {
        if (size < 11) { skipped++; continue; }
        const declared = (bytes[body + 4]! << 16) | (bytes[body + 5]! << 8) | bytes[body + 6]!;
        const width = view.getUint16(body + 7), height = view.getUint16(body + 9);
        from = body + 11;
        if (!checkedSize(width, height) || declared < 4 || (objects.size >= 256 && !objects.has(id))) { objects.delete(id); skipped++; continue; }
        // The run-length data cannot usefully be longer than a few bytes per pixel.
        const expected = Math.min(declared - 4, width * height * 4 + height * 2 + 16);
        objects.set(id, {width, height, expected, parts: [], data: new Uint8Array(0), length: 0, complete: false});
      }
      const object = objects.get(id);
      if (!object || object.complete) continue;
      const part = bytes.subarray(from, Math.min(body + size, from + (object.expected - object.length)));
      if (part.length) object.parts.push(part);
      object.length += part.length;
      if (sequence & 0x40) {
        if (object.parts.length === 1) object.data = object.parts[0]!;
        else { if (retainedBytes + object.length > limits.maxRetainedBytes) { objects.delete(id); skipped++; continue; } retainedBytes += object.length; object.data = new Uint8Array(object.length); let at = 0; for (const piece of object.parts) { object.data.set(piece, at); at += piece.length; } }
        object.parts = [];
        object.complete = true;
      }
    } else if (type === END) present();
  }
  present();
  return {kind: 'pgs', width: canvasWidth, height: canvasHeight, cues: finishCues(cues), skipped};
}

function paintObject(placed: Placed, palette: Uint8ClampedArray, frame: {width: number; height: number}): BitmapRect | null {
  const {object} = placed;
  // What part of the object is shown, in the object's own coordinates.
  let left = 0, top = 0, right = object.width, bottom = object.height;
  if (placed.crop) { left = Math.max(left, placed.crop.x); top = Math.max(top, placed.crop.y); right = Math.min(right, placed.crop.x + placed.crop.width); bottom = Math.min(bottom, placed.crop.y + placed.crop.height); }
  // Where that part lands on the canvas: a cropped object's shown corner is the stated position.
  const originX = placed.x - left, originY = placed.y - top;
  const clip = (box: Window) => { left = Math.max(left, box.x - originX); top = Math.max(top, box.y - originY); right = Math.min(right, box.x + box.width - originX); bottom = Math.min(bottom, box.y + box.height - originY); };
  if (placed.window) clip(placed.window);
  clip({x: 0, y: 0, width: frame.width, height: frame.height});
  const width = right - left, height = bottom - top;
  if (width <= 0 || height <= 0) return null;
  const indexes = decodePgsRuns(object.data, object.width, object.height);
  const rgba = new Uint8ClampedArray(width * height * 4);
  for (let y = 0; y < height; y++) {
    let source = (top + y) * object.width + left, target = y * width * 4;
    for (let x = 0; x < width; x++, target += 4) {
      const entry = indexes[source++]! * 4;
      if (palette[entry + 3] === 0) continue;
      rgba[target] = palette[entry]!; rgba[target + 1] = palette[entry + 1]!; rgba[target + 2] = palette[entry + 2]!; rgba[target + 3] = palette[entry + 3]!;
    }
  }
  return {x: originX + left, y: originY + top, width, height, rgba, forced: placed.forced};
}
