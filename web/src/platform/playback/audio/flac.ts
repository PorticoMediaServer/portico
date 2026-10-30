import {ascii, Cursor, DemuxError, u16be, u32be, type ByteReader, type Demuxer, type Packet, type SeekResult, type TrackConfig} from './types';

/**
 * Native FLAC (`fLaC` stream). Frames are found by their sync code and confirmed by the header's
 * CRC-8 and the frame's CRC-16, so a sync-like byte pair inside audio data is never a boundary.
 */
const CRC8 = new Uint8Array(256), CRC16 = new Uint16Array(256);
for (let i = 0; i < 256; i++) {
  let c = i;
  for (let k = 0; k < 8; k++) c = (c & 0x80 ? (c << 1) ^ 0x07 : c << 1) & 0xff;
  CRC8[i] = c;
  let d = i << 8;
  for (let k = 0; k < 8; k++) d = (d & 0x8000 ? (d << 1) ^ 0x8005 : d << 1) & 0xffff;
  CRC16[i] = d;
}
const crc8 = (b: Uint8Array, from: number, to: number) => { let c = 0; for (let i = from; i < to; i++) c = CRC8[c ^ b[i]!]!; return c; };
export const crc16 = (b: Uint8Array, from: number, to: number) => { let c = 0; for (let i = from; i < to; i++) c = ((c << 8) & 0xffff) ^ CRC16[(c >> 8) ^ b[i]!]!; return c; };

export type StreamInfo = Readonly<{minBlock: number; maxBlock: number; maxFrame: number; sampleRate: number; channels: number; bits: number; totalFrames: number; body: Uint8Array}>;
export type SeekPoint = Readonly<{sample: number; offset: number; frames: number}>;
export type FrameHeader = Readonly<{variable: boolean; blockSize: number; number: number; channels: number; length: number}>;

/** A frame header at `o` with a valid CRC-8, or undefined. */
export function flacFrameHeader(b: Uint8Array, o: number, info: StreamInfo): FrameHeader | undefined {
  if (o + 6 > b.length || b[o] !== 0xff || (b[o + 1]! & 0xfe) !== 0xf8) return undefined;
  const variable = (b[o + 1]! & 1) === 1;
  const sizeCode = b[o + 2]! >> 4, rateCode = b[o + 2]! & 0xf;
  const channelCode = b[o + 3]! >> 4, bitsCode = (b[o + 3]! >> 1) & 7;
  if (sizeCode === 0 || rateCode === 15 || channelCode > 10 || bitsCode === 3 || (b[o + 3]! & 1)) return undefined;
  // UTF-8-style coded frame or sample number.
  let p = o + 4, number = b[p]!, extra = 0;
  if (number < 0x80) extra = 0;
  else if ((number & 0xe0) === 0xc0) { extra = 1; number &= 0x1f; }
  else if ((number & 0xf0) === 0xe0) { extra = 2; number &= 0x0f; }
  else if ((number & 0xf8) === 0xf0) { extra = 3; number &= 0x07; }
  else if ((number & 0xfc) === 0xf8) { extra = 4; number &= 0x03; }
  else if ((number & 0xfe) === 0xfc) { extra = 5; number &= 0x01; }
  else if (number === 0xfe) { extra = 6; number = 0; }
  else return undefined;
  p++;
  if (p + extra + 3 > b.length) return undefined;
  for (let i = 0; i < extra; i++) { const x = b[p++]!; if ((x & 0xc0) !== 0x80) return undefined; number = number * 64 + (x & 0x3f); }
  let blockSize = 0;
  if (sizeCode === 1) blockSize = 192;
  else if (sizeCode <= 5) blockSize = 576 << (sizeCode - 2);
  else if (sizeCode === 6) blockSize = b[p++]! + 1;
  else if (sizeCode === 7) { blockSize = u16be(b, p) + 1; p += 2; }
  else blockSize = 256 << (sizeCode - 8);
  if (rateCode === 12) p += 1; else if (rateCode === 13 || rateCode === 14) p += 2;
  if (p >= b.length || crc8(b, o, p) !== b[p]) return undefined;
  const channels = channelCode < 8 ? channelCode + 1 : 2;
  if (channels !== info.channels || blockSize > info.maxBlock && info.maxBlock > 0) return undefined;
  return {variable, blockSize, number, channels, length: p + 1 - o};
}

export async function openFlac(reader: ByteReader): Promise<Demuxer & {info: StreamInfo; seekTable: readonly SeekPoint[]; firstFrame: number}> {
  const head = await reader.read(0, 1 << 16);
  let o = 0;
  if (ascii(head, 0, 3) === 'ID3') o = 10 + (((head[6]! & 0x7f) << 21) | ((head[7]! & 0x7f) << 14) | ((head[8]! & 0x7f) << 7) | (head[9]! & 0x7f));
  const c = new Cursor(reader, o);
  if (!(await c.need(4)) || ascii(c.view, 0, 4) !== 'fLaC') throw new DemuxError('Not a FLAC stream.');
  c.skip(4);
  let info: StreamInfo | undefined;
  const seekTable: SeekPoint[] = [];
  for (;;) {
    if (!(await c.need(4))) throw new DemuxError('Truncated FLAC metadata.');
    const v = c.view, last = (v[0]! & 0x80) !== 0, type = v[0]! & 0x7f, length = (v[1]! << 16) | (v[2]! << 8) | v[3]!;
    c.skip(4);
    if (!(await c.need(length))) throw new DemuxError('Truncated FLAC metadata.');
    const body = c.view.slice(0, length);
    if (type === 0 && length >= 34) {
      const rate = (body[10]! << 12) | (body[11]! << 4) | (body[12]! >> 4);
      const channels = ((body[12]! >> 1) & 7) + 1, bits = (((body[12]! & 1) << 4) | (body[13]! >> 4)) + 1;
      const total = (body[13]! & 0xf) * 2 ** 32 + ((body[14]! << 24) >>> 0) + (body[15]! << 16) + (body[16]! << 8) + body[17]!;
      info = {minBlock: u16be(body, 0), maxBlock: u16be(body, 2), maxFrame: (body[7]! << 16) | (body[8]! << 8) | body[9]!, sampleRate: rate, channels, bits, totalFrames: total, body: body.slice(0, 34)};
    } else if (type === 3) {
      for (let i = 0; i + 18 <= length; i += 18) {
        const hi = u32be(body, i) * 2 ** 32 + u32be(body, i + 4);
        if (body[i] === 0xff && body[i + 1] === 0xff) continue; // placeholder point
        const off = u32be(body, i + 8) * 2 ** 32 + u32be(body, i + 12);
        if (Number.isSafeInteger(hi) && Number.isSafeInteger(off) && hi < (info?.totalFrames ?? Number.MAX_SAFE_INTEGER) && (reader.size === undefined || off < reader.size) && seekTable.length < 65536) seekTable.push({sample: hi, offset: off, frames: u16be(body, i + 16)});
      }
    }
    c.skip(length);
    if (last) break;
  }
  if (!info || !info.sampleRate || !info.channels) throw new DemuxError('FLAC STREAMINFO is missing.');
  const firstFrame = c.pos;
  // WebCodecs wants "fLaC" and a STREAMINFO block (header included) as the description.
  const description = new Uint8Array(42);
  description.set([0x66, 0x4c, 0x61, 0x43, 0x80, 0, 0, 34]);
  description.set(info.body, 8);
  const config: TrackConfig = Object.freeze({codec: 'flac', family: 'flac', sampleRate: info.sampleRate, numberOfChannels: info.channels, description});
  const streamInfo = info;
  const locate = async (target: number, preroll: number, signal?: AbortSignal): Promise<SeekResult> => {
    const wanted = Math.max(0, target - preroll * streamInfo.maxBlock);
    let point = [...seekTable].sort((a,b) => a.sample-b.sample).filter(p => p.sample<=wanted).at(-1);
    if (point) {
      const verified=await flacPoint(reader,firstFrame+point.offset,streamInfo,signal);
      point=verified&&verified.sample<=wanted?{...verified,offset:verified.offset-firstFrame}:undefined;
    }
    if (!point && wanted>streamInfo.maxBlock && reader.size!==undefined) {
      let low=firstFrame, high=reader.size;
      point={sample:0,offset:0,frames:streamInfo.maxBlock};
      for(let count=0;count<32 && low<high;count++){
        const mid=Math.floor((low+high)/2);
        const found=await flacPoint(reader,mid,streamInfo,signal);
        if(!found){high=mid;continue;}
        if(found.sample>wanted){high=mid;continue;}
        point={...found,offset:found.offset-firstFrame};
        if(wanted<found.sample+found.frames)break;
        low=Math.max(mid+1,found.offset+1);
      }
    }
    if(wanted-(point?.sample??0)>streamInfo.sampleRate*30)throw new DemuxError('No bounded FLAC seek point was found.');
    return {packets: flacFrames(reader,firstFrame+(point?.offset??0),streamInfo,signal),rawStart:point?.sample??0};
  };
  return {
    config, info, seekTable, firstFrame, locate,
    /** The seek table or a few proportional reads usually find the start. When they confirm
     * nothing and the position is deep, the bounded CRC-confirmed search stands in for reading
     * everything before it; near the start the caller simply walks. */
    async seek(target: number, preroll: number, signal?: AbortSignal) {
      const found = await flacSeek(reader, firstFrame, streamInfo, seekTable, target, preroll, signal);
      if (found || target - preroll * (streamInfo.maxBlock || 4096) <= streamInfo.sampleRate * 30) return found;
      return locate(target, preroll, signal);
    },
    packets: (signal?: AbortSignal) => flacFrames(reader, firstFrame, streamInfo, signal),
  };
}

/** The raw index of a frame's first sample: its own number, or its frame number in a fixed-block stream. */
const frameSample = (h: FrameHeader, info: StreamInfo) => (h.variable ? h.number : h.number * info.minBlock);

/**
 * Starts part-way without reading what comes before. A FLAC frame carries its own position, so
 * wherever reading begins, the first whole frame found says exactly which sample it is: the seek
 * table gives a place to begin when the file has one, and otherwise (or when the table is sparse)
 * the place is found by proportion and corrected from the frames met there, in a bounded number
 * of small reads. Resuming hours into a single-file audiobook then costs a few reads, not the
 * whole file before the position. Undefined when no start can be confirmed: the caller walks.
 */
export async function flacSeek(reader: ByteReader, firstFrame: number, info: StreamInfo, seekTable: readonly SeekPoint[], target: number, preroll: number, signal?: AbortSignal, /** How close is close enough (tests shrink it for fixtures a few seconds long). */ tuning: {near?: number} = {}): Promise<{packets: AsyncGenerator<Packet>; rawStart: number} | undefined> {
  type Place = {offset: number; sample: number};
  const wanted = Math.max(0, target - preroll * (info.maxBlock || 4096));
  const near = tuning.near ?? info.sampleRate * 15;
  const probe = async (at: number): Promise<Place | undefined> => {
    const frames = flacFrames(reader, at, info, signal);
    try {
      const first = await frames.next();
      return first.done ? undefined : {offset: first.value.offset, sample: frameSample(first.value.header, info)};
    } finally { await frames.return(undefined).catch(() => {}); }
  };
  let low: Place = {offset: firstFrame, sample: 0};
  let high: Place | undefined;
  // The seek table's last point at or before the position, confirmed by the frame found there.
  let point: SeekPoint | undefined;
  for (const p of seekTable) if (p.sample <= wanted && (!point || p.sample > point.sample)) point = p;
  if (point && point.sample > 0) {
    const found = await probe(firstFrame + point.offset);
    if (found && found.sample <= wanted) low = found;
  }
  const size = reader.size, total = info.totalFrames, block = info.maxBlock || 4096;
  if (size && total) {
    // Bytes to stand back from the proportional place; it grows while reads land past the position.
    let back = 0;
    for (let i = 0; i < 10 && wanted - low.sample > near; i++) {
      const upper = high ?? {offset: size, sample: total};
      if (upper.sample <= low.sample || upper.offset <= low.offset) break;
      const perSample = (upper.offset - low.offset) / (upper.sample - low.sample);
      const frameBytes = Math.max(64, perSample * block);
      // Two frames short of the proportional place, so that landing before the position is the usual case.
      const aim = Math.floor(low.offset + (wanted - low.sample) * perSample - 2 * frameBytes - back);
      if (aim <= low.offset) break;
      const found = await probe(aim);
      if (!found) break;
      if (found.sample <= wanted) { if (found.offset <= low.offset) break; low = found; back = 0; }
      else { high = found; back = back ? back * 2 : 2 * frameBytes; }
    }
  }
  if (low.offset === firstFrame) return undefined; // nothing better than the start was confirmed
  const from = low;
  return {packets: flacFrames(reader, from.offset, info, signal), rawStart: from.sample};
}

/** A CRC-confirmed frame near a byte offset. Search and allocations stay bounded. */
async function flacPoint(reader: ByteReader, offset: number, info: StreamInfo, signal?: AbortSignal): Promise<SeekPoint | undefined> {
  const reach=Math.min(1<<20,Math.max(1<<17,2*info.maxFrame+64));
  const data=await reader.read(offset,Math.min(reach,(reader.size??offset+reach)-offset),signal);
  let candidates=0;
  for(let at=0;at+8<data.length;at++){
    const h=flacFrameHeader(data,at,info);if(!h)continue;
    if(++candidates>64)return undefined;
    const sample=h.variable?h.number:h.number*info.maxBlock;
    if(!Number.isSafeInteger(sample)||sample>=info.totalFrames)continue;
    for(let end=at+h.length+2;end+8<data.length;end++){
      if(data[end]!==0xff)continue;
      const next=flacFrameHeader(data,end,info);
      if(next&&next.variable===h.variable&&next.number===h.number+(h.variable?h.blockSize:1)
        &&crc16(data,at,end-2)===u16be(data,end-2))return {sample,offset:offset+at,frames:h.blockSize};
    }
    if(offset+data.length===reader.size && crc16(data,at,data.length-2)===u16be(data,data.length-2))return {sample,offset:offset+at,frames:h.blockSize};
  }
  return undefined;
}

/** Frames from `from` (which must be a frame start). */
export async function* flacFrames(reader: ByteReader, from: number, info: StreamInfo, signal?: AbortSignal): AsyncGenerator<Packet & {header: FrameHeader}> {
  const c = new Cursor(reader, from, 1 << 17, signal);
  const reach = Math.max(info.maxFrame || 0, 1 << 16) + 32;
  while (await c.need(6)) {
    const h = flacFrameHeader(c.view, 0, info);
    if (!h) { c.skip(1); continue; }
    await c.need(reach);
    const v = c.view;
    // The frame ends where the next valid frame begins and the CRC-16 of what lies between matches.
    let end = -1;
    for (let p = h.length + 2; p + 1 < v.length; p++) {
      if (v[p] !== 0xff || (v[p + 1]! & 0xfe) !== 0xf8) continue;
      const next = flacFrameHeader(v, p, info);
      if (next && next.variable === h.variable && crc16(v, 0, p - 2) === u16be(v, p - 2)) { end = p; break; }
    }
    if (end < 0) {
      if (!c.eof) {
        // A frame longer than the window (a stream without max frame size): widen and look again.
        if (!(await c.need(c.view.length * 2))) { /* at the end */ }
        if (!c.eof) continue;
      }
      // The last frame runs to the end of the file (less any trailing junk the CRC rejects).
      const tail = c.view;
      for (let len = tail.length; len >= h.length + 2; len--) {
        if (crc16(tail, 0, len - 2) === u16be(tail, len - 2)) { end = len; break; }
        if (tail.length - len > 256) break;
      }
      if (end < 0) return;
    }
    const offset = c.pos;
    const data = c.view.slice(0, end);
    c.skip(end);
    yield {data, frames: h.blockSize, offset, header: h};
  }
}
