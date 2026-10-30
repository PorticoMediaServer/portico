import {Cursor, DemuxError, type ByteReader, type Demuxer, type Packet, type TrackConfig} from './types';
import {afterId3} from './mp3';

/** AAC in ADTS (`.aac`): each frame's header gives the AudioSpecificConfig; packets are raw AAC. */
const RATES = [96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350];

type Header = {profile: number; rateIndex: number; channels: number; length: number; headerLength: number; blocks: number};
function header(b: Uint8Array, o: number): Header | undefined {
  if (o + 7 > b.length || b[o] !== 0xff || (b[o + 1]! & 0xf6) !== 0xf0) return undefined;
  const headerLength = (b[o + 1]! & 1) ? 7 : 9;
  const profile = b[o + 2]! >> 6, rateIndex = (b[o + 2]! >> 2) & 0xf;
  const channels = ((b[o + 2]! & 1) << 2) | (b[o + 3]! >> 6);
  const length = ((b[o + 3]! & 3) << 11) | (b[o + 4]! << 3) | (b[o + 5]! >> 5);
  const blocks = (b[o + 6]! & 3) + 1;
  if (rateIndex >= RATES.length || length < headerLength || channels === 0) return undefined;
  return {profile, rateIndex, channels, length, headerLength, blocks};
}

export async function openAdts(reader: ByteReader): Promise<Demuxer> {
  const head = await reader.read(0, 1 << 16);
  const base = afterId3(head, 0);
  const probe = base + 8192 > head.length ? await reader.read(base, 8192) : head.subarray(base);
  let first: Header | undefined, at = 0;
  for (; at + 7 <= probe.length; at++) {
    const h = header(probe, at);
    if (h && header(probe, at + h.length)?.rateIndex === h.rateIndex) { first = h; break; }
  }
  if (!first) throw new DemuxError('No ADTS frames were found.');
  const objectType = first.profile + 1;
  const asc = new Uint8Array([(objectType << 3) | (first.rateIndex >> 1), ((first.rateIndex & 1) << 7) | (first.channels << 3)]);
  const config: TrackConfig = Object.freeze({codec: `mp4a.40.${objectType}`, family: 'aac', sampleRate: RATES[first.rateIndex]!, numberOfChannels: first.channels, description: asc});
  const start = base + at;
  async function* frames(from: number, signal?: AbortSignal): AsyncGenerator<Packet> {
    const c = new Cursor(reader, from, 1 << 16, signal);
    while (await c.need(7)) {
      const h = header(c.view, 0);
      if (!h || h.rateIndex !== first!.rateIndex) { c.skip(1); continue; }
      if (!(await c.need(h.length))) return;
      const offset = c.pos;
      const data = c.view.slice(h.headerLength, h.length);
      c.skip(h.length);
      yield {data, frames: 1024 * h.blocks, offset};
    }
  }
  return {
    config,
    packets: signal => frames(start, signal),
    packetsFrom: (offset, signal) => frames(Math.max(start, offset), signal),
  };
}
