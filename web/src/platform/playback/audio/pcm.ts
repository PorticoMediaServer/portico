import {ascii, DemuxError, u16be, u16le, u32be, u32le, type ByteReader, type Demuxer, type Packet, type TrackConfig} from './types';

/** WAV (RIFF, including WAVE_FORMAT_EXTENSIBLE) and AIFF/AIFC (uncompressed): no codec, samples are
 * converted here. Packets are whole-frame slices of the data chunk. */
export async function openPcm(reader: ByteReader): Promise<Demuxer & {dataOffset: number; dataBytes: number}> {
  const head = await reader.read(0, 1 << 16);
  let sampleRate = 0, channels = 0, bits = 0, float = false, bigEndian = false, dataOffset = -1, dataBytes = 0;
  if (ascii(head, 0, 4) === 'RIFF' && ascii(head, 8, 4) === 'WAVE') {
    for (let o = 12; o + 8 <= head.length;) {
      const id = ascii(head, o, 4), size = u32le(head, o + 4);
      if (id === 'fmt ') {
        let format = u16le(head, o + 8);
        channels = u16le(head, o + 10); sampleRate = u32le(head, o + 12); bits = u16le(head, o + 22);
        if (format === 0xfffe && size >= 40) format = u16le(head, o + 32);
        if (format !== 1 && format !== 3) throw new DemuxError('Compressed WAV is not supported.');
        float = format === 3;
      } else if (id === 'data') { dataOffset = o + 8; dataBytes = size; break; }
      o += 8 + size + (size & 1);
    }
  } else if (ascii(head, 0, 4) === 'FORM' && (ascii(head, 8, 4) === 'AIFF' || ascii(head, 8, 4) === 'AIFC')) {
    bigEndian = true;
    for (let o = 12; o + 8 <= head.length;) {
      const id = ascii(head, o, 4), size = u32be(head, o + 4);
      if (id === 'COMM') {
        channels = u16be(head, o + 8); bits = u16be(head, o + 14);
        // 80-bit extended float sample rate.
        const exponent = (u16be(head, o + 16) & 0x7fff) - 16383, mantissa = u32be(head, o + 18);
        sampleRate = Math.round(mantissa * 2 ** (exponent - 31));
        if (ascii(head, 8, 4) === 'AIFC') {
          const kind = ascii(head, o + 26, 4);
          if (kind === 'sowt') bigEndian = false;
          else if (kind === 'fl32' || kind === 'FL32') float = true;
          else if (kind !== 'NONE') throw new DemuxError('Compressed AIFF is not supported.');
        }
      } else if (id === 'SSND') { dataOffset = o + 16 + u32be(head, o + 8); dataBytes = size - 8; break; }
      o += 8 + size + (size & 1);
    }
  } else throw new DemuxError('Not a WAV or AIFF file.');
  if (dataOffset < 0 || !sampleRate || !channels || ![8, 16, 24, 32].includes(bits)) throw new DemuxError('Unreadable PCM header.');
  if (reader.size !== undefined) dataBytes = Math.min(dataBytes || Infinity, reader.size - dataOffset);
  const frameBytes = channels * bits / 8;
  const config: TrackConfig = Object.freeze({codec: 'pcm', family: 'pcm', sampleRate, numberOfChannels: channels, pcm: Object.freeze({bits, float, bigEndian})});
  const block = frameBytes * 16384;
  return {
    config, dataOffset, dataBytes,
    async *packets(signal?: AbortSignal): AsyncGenerator<Packet> {
      const end = dataOffset + dataBytes - (dataBytes % frameBytes);
      for (let o = dataOffset; o < end;) {
        const n = Math.min(block, end - o);
        const data = await reader.read(o, n, signal);
        const whole = data.length - (data.length % frameBytes);
        if (!whole) return;
        yield {data: data.subarray(0, whole), frames: whole / frameBytes, offset: o};
        o += whole;
      }
    },
  };
}

/** PCM bytes → planar float. */
export function pcmToPlanar(data: Uint8Array, channels: number, pcm: {bits: number; float: boolean; bigEndian: boolean}): Float32Array[] {
  const bytes = pcm.bits / 8, frames = Math.floor(data.length / (bytes * channels));
  const out = Array.from({length: channels}, () => new Float32Array(frames));
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength), le = !pcm.bigEndian;
  for (let i = 0, o = 0; i < frames; i++) {
    for (let ch = 0; ch < channels; ch++, o += bytes) {
      let v: number;
      if (pcm.float) v = pcm.bits === 32 ? view.getFloat32(o, le) : 0;
      else if (bytes === 1) v = pcm.bigEndian ? ((data[o]! << 24) >> 24) / 128 : (data[o]! - 128) / 128;
      else if (bytes === 2) v = view.getInt16(o, le) / 32768;
      else if (bytes === 3) { const x = le ? data[o]! | (data[o + 1]! << 8) | (data[o + 2]! << 16) : (data[o]! << 16) | (data[o + 1]! << 8) | data[o + 2]!; v = ((x << 8) >> 8) / 8388608; }
      else v = view.getInt32(o, le) / 2147483648;
      out[ch]![i] = v;
    }
  }
  return out;
}
