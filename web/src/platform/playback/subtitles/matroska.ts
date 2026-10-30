/**
 * The little of Matroska needed to take subtitle packets out of a `.mks` file: the track's codec
 * and private data, and each block's time and bytes. Everything else is stepped over. Sizes are
 * checked against the file; an element that runs past its parent ends the reading.
 */
export type MatroskaPacket = Readonly<{timeMs: number; durationMs: number | null; data: Uint8Array}>;
export type MatroskaSubtitles = Readonly<{codec: string; codecPrivate: Uint8Array | null; packets: readonly MatroskaPacket[]}>;

const SEGMENT = 0x18538067, INFO = 0x1549a966, TIMESCALE = 0x2ad7b1, TRACKS = 0x1654ae6b, TRACK = 0xae, TRACK_NUMBER = 0xd7, TRACK_TYPE = 0x83,
  CODEC = 0x86, CODEC_PRIVATE = 0x63a2, CLUSTER = 0x1f43b675, CLUSTER_TIME = 0xe7, SIMPLE_BLOCK = 0xa3, BLOCK_GROUP = 0xa0, BLOCK = 0xa1, BLOCK_DURATION = 0x9b;
/** Elements read through in place: their children are handled as if they stood where the parent does,
 * which is also what makes a parent of unstated size (a file written to a pipe) readable. */
const transparent = new Set([SEGMENT, INFO, TRACKS, CLUSTER]);

export function readMatroskaSubtitles(bytes: Uint8Array, maxPackets = 100_000): MatroskaSubtitles {
  type Block = {track: number; timeTicks: number; durationTicks: number | null; data: Uint8Array};
  type Track = {number: number; type: number; codec: string; codecPrivate: Uint8Array | null};
  let scale = 1_000_000, clusterTime = 0;
  const tracks: Track[] = [], blocks: Block[] = [];
  const unsigned = (from: number, to: number) => { let n = 0; for (let i = from; i < to && i < from + 8; i++) n = n * 256 + bytes[i]!; return n; };
  /** A variable-length number at `p`. An ID keeps its marker bit; a size drops it, and all ones means "not stated". */
  const vint = (p: number, id: boolean): {value: number; length: number; unknown: boolean} | null => {
    const first = bytes[p];
    if (first === undefined || first === 0) return null;
    const length = Math.clz32(first) - 23;
    if (length > (id ? 4 : 8) || p + length > bytes.length) return null;
    const mask = 0xff >> length;
    let value = id ? first : first & mask, ones = (first & mask) === mask;
    for (let i = 1; i < length; i++) { const next = bytes[p + i]!; value = value * 256 + next; if (next !== 0xff) ones = false; }
    return {value, length, unknown: !id && ones};
  };
  const block = (from: number, to: number): Block | null => {
    const track = vint(from, false);
    if (!track || from + track.length + 3 > to) return null;
    const at = from + track.length, relative = ((bytes[at]! << 8) | bytes[at + 1]!) << 16 >> 16, flags = bytes[at + 2]!;
    if (flags & 0x06) return null; // laced blocks are not used for subtitles
    return {track: track.value, timeTicks: clusterTime + relative, durationTicks: null, data: bytes.subarray(at + 3, to)};
  };
  const walk = (from: number, to: number, group: {block: Block | null; duration: number | null} | null, track: Track | null) => {
    let p = from;
    while (p < to && blocks.length < maxPackets) {
      const id = vint(p, true);
      if (!id) return;
      const size = vint(p + id.length, false);
      if (!size) return;
      const body = p + id.length + size.length;
      if (!group && !track && transparent.has(id.value)) { p = body; continue; }
      if (size.unknown || body + size.value > to) return;
      const end = body + size.value;
      if (id.value === TRACK && !group && !track) { const entry: Track = {number: 0, type: 0, codec: '', codecPrivate: null}; walk(body, end, null, entry); if (tracks.length < 64) tracks.push(entry); }
      else if (id.value === BLOCK_GROUP && !group && !track) {
        const holder: {block: Block | null; duration: number | null} = {block: null, duration: null};
        walk(body, end, holder, null);
        if (holder.block) { holder.block.durationTicks = holder.duration; blocks.push(holder.block); }
      }
      else if (id.value === TIMESCALE) scale = unsigned(body, end) || scale;
      else if (id.value === CLUSTER_TIME) clusterTime = unsigned(body, end);
      else if (id.value === SIMPLE_BLOCK) { const b = block(body, end); if (b) blocks.push(b); }
      else if (group && id.value === BLOCK) group.block = block(body, end);
      else if (group && id.value === BLOCK_DURATION) group.duration = unsigned(body, end);
      else if (track && id.value === TRACK_NUMBER) track.number = unsigned(body, end);
      else if (track && id.value === TRACK_TYPE) track.type = unsigned(body, end);
      else if (track && id.value === CODEC) track.codec = String.fromCharCode(...bytes.subarray(body, Math.min(end, body + 64))).replace(/\0+$/, '');
      else if (track && id.value === CODEC_PRIVATE) track.codecPrivate = bytes.subarray(body, end);
      p = end;
    }
  };
  walk(0, bytes.length, null, null);
  const chosen = tracks.find(t => t.type === 0x11) ?? tracks[0];
  const ms = scale / 1_000_000;
  const packets = blocks.filter(b => !chosen || b.track === chosen.number).map(b => ({timeMs: b.timeTicks * ms, durationMs: b.durationTicks === null ? null : b.durationTicks * ms, data: b.data}));
  return {codec: chosen?.codec ?? '', codecPrivate: chosen?.codecPrivate ?? null, packets};
}
