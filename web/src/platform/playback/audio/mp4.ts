import {aacConfig} from './aac-config';
import {ascii, DemuxError, u16be, u32be, type ByteReader, type Demuxer, type Packet, type TrackConfig} from './types';

/**
 * MP4/M4A with AAC, ALAC, FLAC or Opus: the first sound track's sample table gives each packet's
 * offset, size and duration, so packets are read directly and a seek is exact. `moov` may follow
 * the media (a prepared presentation's `prefetchBytes` covers a trailing index, §18.1).
 */
type Box = {type: string; start: number; size: number; header: number};

function boxes(b: Uint8Array, from: number, to: number): Box[] {
  const out: Box[] = [];
  for (let o = from; o + 8 <= to;) {
    let size = u32be(b, o), header = 8;
    const type = ascii(b, o + 4, 4);
    if (size === 1) { size = u32be(b, o + 8) * 2 ** 32 + u32be(b, o + 12); header = 16; }
    else if (size === 0) size = to - o;
    if (size < header || o + size > to) break;
    out.push({type, start: o, size, header});
    o += size;
  }
  return out;
}
const child = (b: Uint8Array, box: Box, type: string, skip = 0) => boxes(b, box.start + box.header + skip, box.start + box.size).find(x => x.type === type);

/** The top-level `moov`, read wherever it is. */
async function readMoov(reader: ByteReader, signal?: AbortSignal): Promise<Uint8Array> {
  let o = 0;
  for (let guard = 0; guard < 64; guard++) {
    const h = await reader.read(o, 16, signal);
    if (h.length < 8) break;
    let size = u32be(h, 0);
    const type = ascii(h, 4, 4);
    if (size === 1) size = u32be(h, 8) * 2 ** 32 + u32be(h, 12);
    else if (size === 0) size = (reader.size ?? o + 8) - o;
    if (type === 'moov') {
      if (size > 64 << 20) throw new DemuxError('The MP4 index is too large.');
      return reader.read(o, size, signal);
    }
    if (size < 8) break;
    o += size;
  }
  throw new DemuxError('No MP4 index (moov) was found.');
}

/** The descriptor tree in `esds` → DecoderSpecificInfo (the AudioSpecificConfig). */
function esdsConfig(b: Uint8Array, from: number, to: number): Uint8Array | undefined {
  let o = from;
  const len = () => { let n = 0; for (let i = 0; i < 4; i++) { const x = b[o++]!; n = (n << 7) | (x & 0x7f); if (!(x & 0x80)) break; } return n; };
  while (o < to) {
    const tag = b[o++]!, size = len();
    if (tag === 0x03) { o += 2; const flags = b[o++]!; if (flags & 0x80) o += 2; if (flags & 0x40) o += 1 + b[o]!; if (flags & 0x20) o += 2; continue; }
    if (tag === 0x04) { o += 13; continue; }
    if (tag === 0x05) return b.slice(o, o + size);
    o += size;
  }
  return undefined;
}

export type SampleTable = Readonly<{offsets: Float64Array; sizes: Uint32Array; durations: Uint32Array; timescale: number}>;

export async function openMp4(reader: ByteReader): Promise<Demuxer & {table: SampleTable}> {
  const moov = await readMoov(reader);
  const root: Box = {type: 'moov', start: 0, size: moov.length, header: 8};
  const trak = boxes(moov, 8, moov.length).filter(x => x.type === 'trak').find(t => {
    const mdia = child(moov, t, 'mdia'), hdlr = mdia && child(moov, mdia, 'hdlr');
    return hdlr && ascii(moov, hdlr.start + hdlr.header + 8, 4) === 'soun';
  });
  void root;
  if (!trak) throw new DemuxError('No audio track in this MP4.');
  const mdia = child(moov, trak, 'mdia')!, mdhd = child(moov, mdia, 'mdhd'), minf = child(moov, mdia, 'minf'), stbl = minf && child(moov, minf, 'stbl');
  if (!mdhd || !stbl) throw new DemuxError('Incomplete MP4 audio track.');
  const timescale = moov[mdhd.start + mdhd.header] === 1 ? u32be(moov, mdhd.start + mdhd.header + 20) : u32be(moov, mdhd.start + mdhd.header + 12);
  const stsd = child(moov, stbl, 'stsd');
  if (!stsd) throw new DemuxError('No MP4 sample description.');
  const entry = boxes(moov, stsd.start + stsd.header + 8, stsd.start + stsd.size)[0];
  if (!entry) throw new DemuxError('No MP4 sample entry.');
  // AudioSampleEntry: 8 reserved/index, 8 reserved (version, revision, vendor), channels, bits, 4, rate (16.16).
  const e = entry.start + entry.header;
  const version = u16be(moov, e + 8);
  let channels = u16be(moov, e + 16), sampleRate = u32be(moov, e + 24) >>> 16;
  const inner = e + 28 + (version === 1 ? 16 : version === 2 ? 36 : 0);
  const sub = boxes(moov, inner, entry.start + entry.size);
  let config: TrackConfig;
  const fourcc = entry.type;
  if (fourcc === 'mp4a') {
    const esds = sub.find(x => x.type === 'esds');
    const asc = esds && esdsConfig(moov, esds.start + esds.header + 4, esds.start + esds.size);
    if (!asc || asc.length < 2) throw new DemuxError('AAC configuration is missing.');
    const aac = aacConfig(asc), objectType = aac.objectType;
    sampleRate = aac.sampleRate;
    if (aac.channels) channels = aac.channels;
    config = {codec: `mp4a.40.${objectType}`, family: 'aac', sampleRate, numberOfChannels: channels, description: asc, framesPerPacket: aac.framesPerPacket};
  } else if (fourcc === 'alac') {
    const alac = sub.find(x => x.type === 'alac');
    if (!alac) throw new DemuxError('ALAC configuration is missing.');
    const cookie = moov.slice(alac.start + alac.header + 4, alac.start + alac.size);
    if (cookie.length >= 24) { channels = cookie[9]!; sampleRate = u32be(cookie, 20); }
    config = {codec: 'alac', family: 'alac', sampleRate, numberOfChannels: channels, description: cookie};
  } else if (fourcc === 'fLaC') {
    const dfla = sub.find(x => x.type === 'dfLa');
    if (!dfla) throw new DemuxError('FLAC configuration is missing.');
    const blocks = moov.slice(dfla.start + dfla.header + 4, dfla.start + dfla.size);
    const description = new Uint8Array(4 + blocks.length);
    description.set([0x66, 0x4c, 0x61, 0x43]); description.set(blocks, 4);
    config = {codec: 'flac', family: 'flac', sampleRate: (blocks[14]! << 12) | (blocks[15]! << 4) | (blocks[16]! >> 4), numberOfChannels: ((blocks[16]! >> 1) & 7) + 1, description};
  } else if (fourcc === 'Opus') {
    const dops = sub.find(x => x.type === 'dOps');
    if (!dops) throw new DemuxError('Opus configuration is missing.');
    const d = moov.subarray(dops.start + dops.header, dops.start + dops.size);
    // dOps (big-endian) → OpusHead (little-endian).
    const head = new Uint8Array(19 + (d[10] ? 2 + d[1]! : 0));
    head.set([0x4f, 0x70, 0x75, 0x73, 0x48, 0x65, 0x61, 0x64, 1, d[1]!, d[3]!, d[2]!, d[7]!, d[6]!, d[5]!, d[4]!, d[9]!, d[8]!, d[10]!]);
    if (d[10]) head.set(d.subarray(11, 13 + d[1]!), 19);
    config = {codec: 'opus', family: 'opus', sampleRate: 48000, numberOfChannels: d[1]!, description: head};
  } else throw new DemuxError(`Unsupported MP4 audio: ${fourcc}.`);

  const table = sampleTable(moov, stbl, timescale);
  // Durations are in the track's timescale; packets are counted in frames at the codec's rate.
  const scale = config.sampleRate / timescale;
  const frozen = Object.freeze(config);
  return {
    config: frozen, table,
    async *packets(signal?: AbortSignal): AsyncGenerator<Packet> {
      yield* mp4Packets(reader, table, 0, scale, signal, config.framesPerPacket);
    },
    // The sample table is in memory, so a seek reads only from the target on:
    // resuming hour eight of an audiobook doesn't download hours one to seven.
    seek(target: number, preroll: number, signal?: AbortSignal) {
      let raw = 0, i = 0;
      const starts: number[] = [];
      for (; i < table.durations.length; i++) {
        const frames = config.framesPerPacket ?? Math.round(table.durations[i]! * scale);
        starts.push(raw);
        if (raw + frames > target) break;
        raw += frames;
      }
      const first = Math.max(0, Math.min(i, table.durations.length) - preroll);
      return {packets: mp4Packets(reader, table, first, scale, signal, config.framesPerPacket), rawStart: starts[first] ?? raw};
    },
  };
}

export async function* mp4Packets(reader: ByteReader, t: SampleTable, from: number, scale: number, signal?: AbortSignal, framesPerPacket?: number): AsyncGenerator<Packet> {
  // Read runs of contiguous samples in one request.
  for (let i = from; i < t.sizes.length;) {
    let j = i, bytes = 0;
    while (j < t.sizes.length && bytes < (1 << 18) && (j === i || t.offsets[j] === t.offsets[j - 1]! + t.sizes[j - 1]!)) { bytes += t.sizes[j]!; j++; }
    const run = await reader.read(t.offsets[i]!, bytes, signal);
    let o = 0;
    for (let k = i; k < j; k++) {
      if (o + t.sizes[k]! > run.length) return;
      yield {data: run.slice(o, o + t.sizes[k]!), frames: framesPerPacket ?? Math.round(t.durations[k]! * scale), offset: t.offsets[k]!};
      o += t.sizes[k]!;
    }
    i = j;
  }
}

function sampleTable(b: Uint8Array, stbl: Box, timescale: number): SampleTable {
  const find = (type: string) => child(b, stbl, type);
  const stsz = find('stsz'), stsc = find('stsc'), stco = find('stco') ?? find('co64'), stts = find('stts');
  if (!stsz || !stsc || !stco || !stts) throw new DemuxError('Incomplete MP4 sample table.');
  // Every count must fit inside its own box before anything is allocated from it: a
  // damaged file in a real library must fail cleanly, not allocate or loop on garbage.
  const fits = (box: Box, from: number, entries: number, width: number) => from + entries * width <= box.start + box.size;
  const zb = stsz.start + stsz.header, fixed = u32be(b, zb + 4), count = u32be(b, zb + 8);
  if (count > 50_000_000 || (!fixed && !fits(stsz, zb + 12, count, 4))) throw new DemuxError('Damaged MP4 sample sizes.');
  const sizes = new Uint32Array(count);
  for (let i = 0; i < count; i++) sizes[i] = fixed || u32be(b, zb + 12 + i * 4);
  const cb = stco.start + stco.header, chunks = u32be(b, cb + 4), wide = stco.type === 'co64';
  if (!fits(stco, cb + 8, chunks, wide ? 8 : 4)) throw new DemuxError('Damaged MP4 chunk offsets.');
  const chunkOffsets = new Float64Array(chunks);
  for (let i = 0; i < chunks; i++) chunkOffsets[i] = wide ? u32be(b, cb + 8 + i * 8) * 2 ** 32 + u32be(b, cb + 12 + i * 8) : u32be(b, cb + 8 + i * 4);
  const sb = stsc.start + stsc.header, runs = u32be(b, sb + 4);
  if (!fits(stsc, sb + 8, runs, 12)) throw new DemuxError('Damaged MP4 chunk map.');
  const offsets = new Float64Array(count);
  let sample = 0;
  for (let r = 0; r < runs; r++) {
    const firstChunk = u32be(b, sb + 8 + r * 12) - 1, perChunk = u32be(b, sb + 12 + r * 12);
    const lastChunk = Math.min(chunks, r + 1 < runs ? u32be(b, sb + 8 + (r + 1) * 12) - 1 : chunks);
    for (let ch = Math.max(0, firstChunk); ch < lastChunk && sample < count; ch++) {
      let o = chunkOffsets[ch]!;
      for (let k = 0; k < perChunk && sample < count; k++) { offsets[sample] = o; o += sizes[sample]!; sample++; }
    }
  }
  const tb = stts.start + stts.header, entries = u32be(b, tb + 4);
  if (!fits(stts, tb + 8, entries, 8)) throw new DemuxError('Damaged MP4 sample durations.');
  const durations = new Uint32Array(count);
  let s = 0;
  for (let i = 0; i < entries && s < count; i++) {
    const n = u32be(b, tb + 8 + i * 8), d = u32be(b, tb + 12 + i * 8);
    for (let k = 0; k < n && s < count; k++) durations[s++] = d;
  }
  return Object.freeze({offsets, sizes, durations, timescale});
}
