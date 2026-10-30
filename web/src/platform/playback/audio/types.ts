/**
 * The on-device music decoder (spec §18.1 version 2; Plan §9.6): the web engine direct-plays the
 * original file and decodes it here, with the server's exact trim and gains.
 *
 * A container is read through a `ByteReader` (Range requests behind a bounded cache, or a file in
 * tests), split into codec packets by a demuxer, and decoded by WebCodecs (or, for PCM, here).
 */

/** Random access to the file's bytes. `read` may return fewer bytes only at the end of the file. */
export interface ByteReader {
  /** The file's size when known (direct plans carry `bytes`). */
  readonly size?: number;
  read(offset: number, length: number, signal?: AbortSignal): Promise<Uint8Array>;
}

/** What a decoder needs, as WebCodecs `AudioDecoderConfig` spells it, plus what the engine checks. */
export type TrackConfig = Readonly<{
  /** WebCodecs codec string (`mp3`, `mp4a.40.2`, `flac`, `opus`, `vorbis`, `alac`), or `pcm` (decoded here). */
  codec: string;
  /** The plan's codec family (`mp3`, `aac`, `flac`, `opus`, `vorbis`, `alac`, `pcm`). */
  family: string;
  sampleRate: number;
  numberOfChannels: number;
  description?: Uint8Array;
  /** Raw decoded packet length, independent of a trimmed container timestamp. */
  framesPerPacket?: number;
  /** PCM only: how samples are stored. */
  pcm?: Readonly<{bits: number; float: boolean; bigEndian: boolean}>;
}>;

/** One codec packet. `frames` is its decoded length when the container or bitstream says. */
export type Packet = Readonly<{data: Uint8Array; frames?: number; offset: number}>;

export interface Demuxer {
  readonly config: TrackConfig;
  /** Packets in order from the start. */
  packets(signal?: AbortSignal): AsyncGenerator<Packet>;
  /** Packets from the one holding raw frame `target`, `preroll` packets earlier, found without
   * reading what comes before (a container with a sample index, or frames that carry their own
   * position). `rawStart` is the raw index of the first packet's first frame, and is exact.
   * Absent, or answering undefined: the caller walks `packets` from the start. */
  seek?(target: number, preroll: number, signal?: AbortSignal): SeekResult | undefined | Promise<SeekResult | undefined>;
  /** A bounded search for the same start in a stream with no usable index, each candidate
   * confirmed by its checksum. Throws when no start near the position is confirmed, so the
   * caller hands the position to the ordinary player instead of reading everything before it. */
  locate?(target: number, preroll: number, signal?: AbortSignal): Promise<SeekResult>;
  /** Packets from `offset`, a packet (MP3, ADTS) or page (Ogg) boundary the server's seek index
   * names, read from there without what comes before. Each packet says its length in frames
   * where the bitstream does (not Vorbis). */
  packetsFrom?(offset: number, signal?: AbortSignal): AsyncGenerator<Packet>;
}

export type SeekResult = {packets: AsyncGenerator<Packet>; rawStart: number};

export class DemuxError extends Error {
  readonly code = 'invalid_media';
}

export const u32be = (b: Uint8Array, o: number) => ((b[o]! << 24) >>> 0) + (b[o + 1]! << 16) + (b[o + 2]! << 8) + b[o + 3]!;
export const u16be = (b: Uint8Array, o: number) => (b[o]! << 8) | b[o + 1]!;
export const u32le = (b: Uint8Array, o: number) => (b[o]! | (b[o + 1]! << 8) | (b[o + 2]! << 16)) + ((b[o + 3]! << 24) >>> 0);
export const u16le = (b: Uint8Array, o: number) => b[o]! | (b[o + 1]! << 8);
export const ascii = (b: Uint8Array, o: number, n: number) => String.fromCharCode(...b.subarray(o, o + n));

/**
 * Sequential reading over a `ByteReader` in blocks, for the streaming containers (MP3, ADTS, FLAC,
 * Ogg): `need(n)` makes n bytes available at `pos` (fewer only at the end of the file).
 */
export class Cursor {
  private buffer = new Uint8Array(0);
  private start = 0;
  pos: number;
  eof = false;
  private readonly reader: ByteReader;
  private readonly block: number;
  private readonly signal?: AbortSignal;
  constructor(reader: ByteReader, pos = 0, block = 1 << 16, signal?: AbortSignal) { this.reader = reader; this.pos = pos; this.start = pos; this.block = block; this.signal = signal; }
  /** Bytes available at `pos`. */
  get view(): Uint8Array { return this.buffer.subarray(this.pos - this.start); }
  async need(n: number): Promise<boolean> {
    while (this.start + this.buffer.length - this.pos < n && !this.eof) {
      const end = this.start + this.buffer.length;
      const size = this.reader.size;
      const want = Math.max(this.block, n);
      const chunk = size !== undefined && end >= size ? new Uint8Array(0) : await this.reader.read(end, size !== undefined ? Math.min(want, size - end) : want, this.signal);
      if (chunk.length === 0) { this.eof = true; break; }
      // Keep only what is at or after `pos`.
      const keep = this.buffer.subarray(Math.max(0, this.pos - this.start));
      const next = new Uint8Array(keep.length + chunk.length);
      next.set(keep); next.set(chunk, keep.length);
      this.start = Math.max(this.start, this.pos);
      this.buffer = next;
      if (size === undefined && chunk.length < want) this.eof = true;
    }
    return this.start + this.buffer.length - this.pos >= n;
  }
  skip(n: number) { this.pos += n; }
  /** Moves to an absolute offset (drops the buffer when it's outside). */
  seek(pos: number) {
    if (pos < this.start || pos > this.start + this.buffer.length) { this.buffer = new Uint8Array(0); this.start = pos; this.eof = false; }
    this.pos = pos;
  }
}
