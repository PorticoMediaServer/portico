import type {PacketDecoder, Planar} from './decoder';
import type {Packet, TrackConfig} from './types';

/**
 * A FLAC frame decoder in JavaScript, for browsers whose WebCodecs has no working FLAC decoder
 * (Safari 27 fails every FLAC stream). Lossless and exact: the output is the stream's own samples,
 * so the §18.8 lossless fixtures match bit for bit. Handles CONSTANT, VERBATIM, FIXED and LPC
 * subframes, wasted bits, Rice (4- and 5-bit parameter) residuals with escapes, and all stereo
 * decorrelations.
 */
class Bits {
  private readonly b: Uint8Array;
  private pos = 0; // bit position
  constructor(b: Uint8Array, byte: number) { this.b = b; this.pos = byte * 8; }
  get byte() { return this.pos >> 3; }
  read(n: number): number {
    // Up to 32 bits, as an unsigned number.
    let v = 0;
    while (n > 0) {
      const byte = this.b[this.pos >> 3];
      if (byte === undefined) throw new Error('FLAC frame ended early.');
      const offset = this.pos & 7, take = Math.min(8 - offset, n);
      v = v * (1 << take) + ((byte >> (8 - offset - take)) & ((1 << take) - 1));
      this.pos += take; n -= take;
    }
    return v;
  }
  signed(n: number): number { if (n === 0) return 0; const v = this.read(n); return v >= 2 ** (n - 1) ? v - 2 ** n : v; }
  unary(): number {
    let n = 0;
    for (;;) {
      const byte = this.b[this.pos >> 3];
      if (byte === undefined) throw new Error('FLAC frame ended early.');
      const offset = this.pos & 7, rest = (byte << offset) & 0xff;
      if (rest === 0) { n += 8 - offset; this.pos += 8 - offset; continue; }
      const zeros = Math.clz32(rest) - 24;
      n += zeros; this.pos += zeros + 1;
      return n;
    }
  }
  rice(k: number): number {
    const q = this.unary();
    const u = k ? q * 2 ** k + this.read(k) : q;
    return u % 2 === 1 ? -(u + 1) / 2 : u / 2;
  }
}

const FIXED: readonly (readonly number[])[] = [[], [1], [2, -1], [3, -3, 1], [4, -6, 4, -1]];

function residual(r: Bits, out: Int32Array | Float64Array, order: number, blockSize: number) {
  const method = r.read(2);
  if (method > 1) throw new Error('Unknown FLAC residual coding.');
  const paramBits = method === 0 ? 4 : 5, escape = method === 0 ? 15 : 31;
  const partitionOrder = r.read(4), partitions = 1 << partitionOrder;
  let i = order;
  for (let p = 0; p < partitions; p++) {
    const count = partitionOrder === 0 ? blockSize - order : p === 0 ? (blockSize >> partitionOrder) - order : blockSize >> partitionOrder;
    const k = r.read(paramBits);
    if (k === escape) { const n = r.read(5); for (let j = 0; j < count; j++) out[i++] = r.signed(n); }
    else for (let j = 0; j < count; j++) out[i++] = r.rice(k);
  }
}

function subframe(r: Bits, blockSize: number, bps: number): Float64Array {
  if (r.read(1) !== 0) throw new Error('Bad FLAC subframe.');
  const type = r.read(6);
  let wasted = 0;
  if (r.read(1)) wasted = r.unary() + 1;
  const bits = bps - wasted;
  const s = new Float64Array(blockSize);
  if (type === 0) { const v = r.signed(bits); s.fill(v); }
  else if (type === 1) { for (let i = 0; i < blockSize; i++) s[i] = r.signed(bits); }
  else if (type >= 8 && type <= 12) {
    const order = type - 8;
    for (let i = 0; i < order; i++) s[i] = r.signed(bits);
    residual(r, s, order, blockSize);
    const c = FIXED[order]!;
    for (let i = order; i < blockSize; i++) { let sum = 0; for (let j = 0; j < order; j++) sum += c[j]! * s[i - 1 - j]!; s[i] = s[i]! + sum; }
  } else if (type >= 32) {
    const order = type - 31;
    for (let i = 0; i < order; i++) s[i] = r.signed(bits);
    const precision = r.read(4) + 1;
    if (precision === 16) throw new Error('Bad FLAC LPC precision.');
    const shift = r.signed(5);
    const coefs = new Float64Array(order);
    for (let i = 0; i < order; i++) coefs[i] = r.signed(precision);
    residual(r, s, order, blockSize);
    const div = 2 ** shift;
    for (let i = order; i < blockSize; i++) {
      let sum = 0;
      for (let j = 0; j < order; j++) sum += coefs[j]! * s[i - 1 - j]!;
      s[i] = s[i]! + Math.floor(sum / div);
    }
  } else throw new Error('Reserved FLAC subframe type.');
  if (wasted) for (let i = 0; i < blockSize; i++) s[i] = s[i]! * 2 ** wasted;
  return s;
}

const SAMPLE_BITS = [0, 8, 12, 0, 16, 20, 24, 32];

export function decodeFlacFrame(frame: Uint8Array, streamBits: number): {channels: Float32Array[]; frames: number} {
  const sizeCode = frame[2]! >> 4, rateCode = frame[2]! & 0xf, channelCode = frame[3]! >> 4, bitsCode = (frame[3]! >> 1) & 7;
  let p = 4;
  const first = frame[p]!;
  p += first < 0x80 ? 1 : first < 0xe0 ? 2 : first < 0xf0 ? 3 : first < 0xf8 ? 4 : first < 0xfc ? 5 : first < 0xfe ? 6 : 7;
  let blockSize: number;
  if (sizeCode === 1) blockSize = 192;
  else if (sizeCode <= 5) blockSize = 576 << (sizeCode - 2);
  else if (sizeCode === 6) blockSize = frame[p++]! + 1;
  else if (sizeCode === 7) { blockSize = ((frame[p]! << 8) | frame[p + 1]!) + 1; p += 2; }
  else blockSize = 256 << (sizeCode - 8);
  if (rateCode === 12) p += 1; else if (rateCode === 13 || rateCode === 14) p += 2;
  p += 1; // CRC-8
  const bps = bitsCode === 0 ? streamBits : SAMPLE_BITS[bitsCode]!;
  const channels = channelCode < 8 ? channelCode + 1 : 2;
  const r = new Bits(frame, p);
  const raw: Float64Array[] = [];
  for (let ch = 0; ch < channels; ch++) {
    const side = (channelCode === 8 && ch === 1) || (channelCode === 9 && ch === 0) || (channelCode === 10 && ch === 1);
    raw.push(subframe(r, blockSize, bps + (side ? 1 : 0)));
  }
  if (channelCode === 8) { const [l, d] = raw as [Float64Array, Float64Array]; for (let i = 0; i < blockSize; i++) d[i] = l[i]! - d[i]!; }
  else if (channelCode === 9) { const [d, rr] = raw as [Float64Array, Float64Array]; for (let i = 0; i < blockSize; i++) d[i] = d[i]! + rr[i]!; }
  else if (channelCode === 10) {
    const [m, d] = raw as [Float64Array, Float64Array];
    for (let i = 0; i < blockSize; i++) {
      const side = d[i]!, mid = m[i]! * 2 + (side & 1);
      m[i] = (mid + side) / 2; d[i] = (mid - side) / 2;
    }
  }
  const scale = 1 / 2 ** (bps - 1);
  return {channels: raw.map(x => { const f = new Float32Array(blockSize); for (let i = 0; i < blockSize; i++) f[i] = x[i]! * scale; return f; }), frames: blockSize};
}

export class JsFlacDecoder implements PacketDecoder {
  readonly via = 'js-flac';
  private out: Planar[] = [];
  private readonly bits: number;
  constructor(config: TrackConfig) {
    // STREAMINFO's bits per sample (description: "fLaC" + block header + STREAMINFO).
    const d = config.description;
    const si = d && d.length >= 42 ? d.subarray(8) : d && d.length >= 34 ? d.subarray(d.length - 34) : undefined;
    this.bits = si ? (((si[12]! & 1) << 4) | (si[13]! >> 4)) + 1 : 16;
  }
  decode(p: Packet) { this.out.push(decodeFlacFrame(p.data, this.bits)); }
  take() { const o = this.out; this.out = []; return o; }
  async drain() { return this.take(); }
  async flush() { return this.take(); }
  close() { this.out = []; }
}
