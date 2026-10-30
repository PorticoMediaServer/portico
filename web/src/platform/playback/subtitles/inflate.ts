/**
 * A zlib stream (RFC 1950) holding DEFLATE data (RFC 1951), decompressed at once and bounded:
 * what a progressively coded DVB subtitle object carries (its PNG-style filtered scanlines). The
 * browser's own decompressor is asynchronous and the subtitle decoder is not, and the streams are
 * small, so this is the plain textbook decoder. Anything malformed, or more than `limit` bytes
 * out, answers null.
 */
export function inflateZlib(data: Uint8Array, limit: number): Uint8Array | null {
  if (data.length < 6) return null;
  const cmf = data[0]!, flg = data[1]!;
  // DEFLATE, a window of at most 32 KiB, no preset dictionary, and a valid check.
  if ((cmf & 0x0f) !== 8 || cmf >> 4 > 7 || flg & 0x20 || ((cmf << 8) | flg) % 31 !== 0) return null;
  try {
    return inflate(data, 2, limit);
  } catch {
    return null;
  }
}

class Bits {
  bit = 0;
  readonly data: Uint8Array;
  pos: number;
  constructor(data: Uint8Array, pos: number) { this.data = data; this.pos = pos; }
  need(n: number): number {
    let out = 0;
    for (let i = 0; i < n; i++) {
      if (this.pos >= this.data.length) throw new Error('truncated');
      out |= ((this.data[this.pos]! >> this.bit) & 1) << i;
      if (++this.bit === 8) { this.bit = 0; this.pos++; }
    }
    return out;
  }
  align() { if (this.bit) { this.bit = 0; this.pos++; } }
}

/** A canonical Huffman code as counts per length and symbols in code order. */
type Code = {counts: Uint16Array; symbols: Uint16Array};

function code(lengths: ArrayLike<number>, n: number): Code {
  const counts = new Uint16Array(16), offsets = new Uint16Array(16), symbols = new Uint16Array(n);
  for (let i = 0; i < n; i++) counts[lengths[i]!]!++;
  counts[0] = 0;
  let left = 1;
  for (let len = 1; len < 16; len++) { left = (left << 1) - counts[len]!; if (left < 0) throw new Error('over-subscribed'); }
  for (let len = 1; len < 15; len++) offsets[len + 1] = offsets[len]! + counts[len]!;
  for (let i = 0; i < n; i++) if (lengths[i]) symbols[offsets[lengths[i]!]!++] = i;
  return {counts, symbols};
}

function decode(bits: Bits, c: Code): number {
  let value = 0, first = 0, index = 0;
  for (let len = 1; len < 16; len++) {
    value |= bits.need(1);
    const count = c.counts[len]!;
    if (value - count < first) return c.symbols[index + (value - first)]!;
    index += count; first += count; first <<= 1; value <<= 1;
  }
  throw new Error('bad code');
}

const LENGTH_BASE = [3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258];
const LENGTH_EXTRA = [0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0];
const DIST_BASE = [1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577];
const DIST_EXTRA = [0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13];
const CLEN_ORDER = [16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15];

let fixed: {lit: Code; dist: Code} | undefined;
function fixedCodes() {
  if (!fixed) {
    const lengths = new Uint8Array(288);
    lengths.fill(8, 0, 144); lengths.fill(9, 144, 256); lengths.fill(7, 256, 280); lengths.fill(8, 280, 288);
    fixed = {lit: code(lengths, 288), dist: code(new Uint8Array(30).fill(5), 30)};
  }
  return fixed;
}

function inflate(data: Uint8Array, start: number, limit: number): Uint8Array {
  const bits = new Bits(data, start);
  let out = new Uint8Array(Math.min(limit, Math.max(1024, data.length * 4)));
  let size = 0;
  const put = (byte: number) => {
    if (size >= limit) throw new Error('too large');
    if (size === out.length) { const grown = new Uint8Array(Math.min(limit, out.length * 2)); grown.set(out); out = grown; }
    out[size++] = byte;
  };
  for (let last = 0; !last;) {
    last = bits.need(1);
    const type = bits.need(2);
    if (type === 0) {
      bits.align();
      if (bits.pos + 4 > data.length) throw new Error('truncated');
      const len = data[bits.pos]! | (data[bits.pos + 1]! << 8), nlen = data[bits.pos + 2]! | (data[bits.pos + 3]! << 8);
      if ((len ^ 0xffff) !== nlen) throw new Error('stored length');
      bits.pos += 4;
      if (bits.pos + len > data.length) throw new Error('truncated');
      for (let i = 0; i < len; i++) put(data[bits.pos + i]!);
      bits.pos += len;
      continue;
    }
    let lit: Code, dist: Code;
    if (type === 1) ({lit, dist} = fixedCodes());
    else if (type === 2) {
      const nlen = bits.need(5) + 257, ndist = bits.need(5) + 1, ncode = bits.need(4) + 4;
      if (nlen > 286 || ndist > 30) throw new Error('counts');
      const clen = new Uint8Array(19);
      for (let i = 0; i < ncode; i++) clen[CLEN_ORDER[i]!] = bits.need(3);
      const lencode = code(clen, 19);
      const lengths = new Uint8Array(nlen + ndist);
      for (let i = 0; i < nlen + ndist;) {
        const sym = decode(bits, lencode);
        if (sym < 16) { lengths[i++] = sym; continue; }
        let repeat: number, value = 0;
        if (sym === 16) { if (!i) throw new Error('repeat'); value = lengths[i - 1]!; repeat = 3 + bits.need(2); }
        else if (sym === 17) repeat = 3 + bits.need(3);
        else repeat = 11 + bits.need(7);
        if (i + repeat > nlen + ndist) throw new Error('repeat');
        while (repeat--) lengths[i++] = value;
      }
      if (!lengths[256]) throw new Error('no end');
      lit = code(lengths.subarray(0, nlen), nlen);
      dist = code(lengths.subarray(nlen), ndist);
    } else throw new Error('block type');
    for (;;) {
      const sym = decode(bits, lit);
      if (sym < 256) { put(sym); continue; }
      if (sym === 256) break;
      const l = sym - 257;
      if (l >= 29) throw new Error('length');
      const length = LENGTH_BASE[l]! + bits.need(LENGTH_EXTRA[l]!);
      const d = decode(bits, dist);
      if (d >= 30) throw new Error('distance');
      const back = DIST_BASE[d]! + bits.need(DIST_EXTRA[d]!);
      if (back > size) throw new Error('distance');
      for (let i = 0; i < length; i++) put(out[size - back]!);
    }
  }
  return out.subarray(0, size);
}

/**
 * PNG's scanline filters undone for one byte per pixel (an 8-bit indexed bitmap): each row is a
 * filter type byte and `width` bytes. Null when the rows are not all there or a filter is unknown.
 */
export function unfilterScanlines(raw: Uint8Array, width: number, height: number): Uint8Array | null {
  if (raw.length < height * (width + 1)) return null;
  const out = new Uint8Array(width * height);
  for (let y = 0; y < height; y++) {
    const filter = raw[y * (width + 1)]!, from = y * (width + 1) + 1, row = y * width, up = row - width;
    for (let x = 0; x < width; x++) {
      const v = raw[from + x]!, a = x ? out[row + x - 1]! : 0, b = y ? out[up + x]! : 0, c = x && y ? out[up + x - 1]! : 0;
      let p: number;
      switch (filter) {
        case 0: p = v; break;
        case 1: p = v + a; break;
        case 2: p = v + b; break;
        case 3: p = v + ((a + b) >> 1); break;
        case 4: { const e = a + b - c, pa = Math.abs(e - a), pb = Math.abs(e - b), pc = Math.abs(e - c); p = v + (pa <= pb && pa <= pc ? a : pb <= pc ? b : c); break; }
        default: return null;
      }
      out[row + x] = p & 0xff;
    }
  }
  return out;
}
