import {ascii, Cursor, DemuxError, u16le, u32le, type ByteReader, type Demuxer, type Packet, type TrackConfig} from './types';

/**
 * Ogg with Opus or Vorbis: pages are reassembled into packets for the first logical stream. Opus's
 * `OpusHead` is the decoder description, and each packet's length comes from its TOC byte (48 kHz).
 * Vorbis's three header packets become the Xiph-laced description WebCodecs expects.
 */
type Page = {granule: number; serial: number; flags: number; segments: Uint8Array; body: Uint8Array; offset: number};

async function page(c: Cursor): Promise<Page | undefined> {
  for (;;) {
    if (!(await c.need(27))) return undefined;
    const v = c.view;
    if (ascii(v, 0, 4) !== 'OggS' || v[4] !== 0) { c.skip(1); continue; }
    const count = v[26]!;
    if (!(await c.need(27 + count))) return undefined;
    const segments = c.view.slice(27, 27 + count);
    let size = 0;
    for (const s of segments) size += s;
    if (!(await c.need(27 + count + size))) return undefined;
    const w = c.view;
    const granule = u32le(w, 6) + u32le(w, 10) * 2 ** 32;
    const p: Page = {granule: u32le(w, 10) === 0xffffffff ? -1 : granule, serial: u32le(w, 14), flags: w[5]!, segments, body: w.slice(27 + count, 27 + count + size), offset: c.pos};
    c.skip(27 + count + size);
    return p;
  }
}

/** Packets of one serial, reassembled across pages. Started part-way (`midStream`), a packet
 * continued from a page before the start is incomplete and is dropped. */
async function* oggPackets(reader: ByteReader, from: number, serial: number | undefined, signal?: AbortSignal, midStream = false): AsyncGenerator<{data: Uint8Array; serial: number; offset: number; granule: number; lastOnPage: boolean}> {
  const c = new Cursor(reader, from, 1 << 16, signal);
  let partial: Uint8Array[] = [], stream = serial, first = midStream;
  for (;;) {
    const p = await page(c);
    if (!p) return;
    if (stream === undefined) stream = p.serial;
    if (p.serial !== stream) continue;
    let dropFirst = first && !!(p.flags & 1);
    first = false;
    if (!(p.flags & 1)) partial = []; // not a continuation: drop any dangling piece
    let at = 0;
    const done: Uint8Array[] = [];
    for (let i = 0; i < p.segments.length; i++) {
      const n = p.segments[i]!;
      partial.push(p.body.subarray(at, at + n));
      at += n;
      if (n < 255) {
        const size = partial.reduce((s, x) => s + x.length, 0), data = new Uint8Array(size);
        let o = 0;
        for (const x of partial) { data.set(x, o); o += x.length; }
        if (!dropFirst) done.push(data);
        dropFirst = false;
        partial = [];
      }
    }
    partial = partial.map(x => x.slice());
    for (let i = 0; i < done.length; i++) yield {data: done[i]!, serial: stream, offset: p.offset, granule: p.granule, lastOnPage: i === done.length - 1};
  }
}

/** Samples in one Opus packet at 48 kHz (RFC 6716 §3.1). */
export function opusPacketFrames(packet: Uint8Array): number {
  if (!packet.length) return 0;
  const toc = packet[0]!, config = toc >> 3;
  const size = config < 12 ? [480, 960, 1920, 2880][config & 3]! : config < 16 ? [480, 960][config & 1]! : [120, 240, 480, 960][config & 3]!;
  const code = toc & 3;
  const count = code === 0 ? 1 : code < 3 ? 2 : (packet[1] ?? 0) & 0x3f;
  return size * count;
}

function xiphLace(headers: Uint8Array[]): Uint8Array {
  const lace: number[] = [headers.length - 1];
  for (const h of headers.slice(0, -1)) { let n = h.length; while (n >= 255) { lace.push(255); n -= 255; } lace.push(n); }
  const total = lace.length + headers.reduce((s, h) => s + h.length, 0), out = new Uint8Array(total);
  out.set(lace);
  let o = lace.length;
  for (const h of headers) { out.set(h, o); o += h.length; }
  return out;
}

export async function openOgg(reader: ByteReader): Promise<Demuxer & {preSkip: number}> {
  const headers: {data: Uint8Array; serial: number; offset: number}[] = [];
  let kind: 'opus' | 'vorbis' | undefined, serial: number | undefined, after = 0;
  const need = () => (kind === 'opus' ? 2 : 3);
  for await (const p of oggPackets(reader, 0, undefined)) {
    if (!kind) {
      if (ascii(p.data, 0, 8) === 'OpusHead') kind = 'opus';
      else if (p.data[0] === 1 && ascii(p.data, 1, 6) === 'vorbis') kind = 'vorbis';
      else continue;
      serial = p.serial;
    }
    headers.push(p);
    if (headers.length === need()) { after = p.offset; break; }
  }
  if (!kind || headers.length < need()) throw new DemuxError('No Opus or Vorbis stream was found.');
  const id = headers[0]!.data;
  let config: TrackConfig, preSkip = 0;
  if (kind === 'opus') {
    preSkip = u16le(id, 10);
    config = Object.freeze({codec: 'opus', family: 'opus', sampleRate: 48000, numberOfChannels: id[9]!, description: id});
  } else {
    config = Object.freeze({codec: 'vorbis', family: 'vorbis', sampleRate: u32le(id, 12), numberOfChannels: id[11]!, description: xiphLace(headers.map(h => h.data))});
  }
  const stream = serial!, headerCount = need(), opus = kind === 'opus';
  const packet = (p: {data: Uint8Array; offset: number}): Packet => (opus ? {data: p.data, frames: opusPacketFrames(p.data), offset: p.offset} : {data: p.data, offset: p.offset});
  async function* packets(signal?: AbortSignal): AsyncGenerator<Packet> {
    let seen = 0;
    // From the page holding the first header: skip the header packets.
    for await (const p of oggPackets(reader, headers[0]!.offset, stream, signal)) {
      if (seen++ < headerCount) continue;
      yield packet(p);
    }
    void after;
  }
  return {
    config, preSkip, packets,
    async *packetsFrom(offset: number, signal?: AbortSignal): AsyncGenerator<Packet> {
      // An index entry is a page past the headers that starts a packet.
      if (offset <= headers[headers.length - 1]!.offset) { yield* packets(signal); return; }
      for await (const p of oggPackets(reader, offset, stream, signal, true)) yield packet(p);
    },
  };
}
