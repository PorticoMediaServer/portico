/**
 * The server's HLS playlists, read. Not HLS in general: what the Portico server writes.
 *
 * - A title: a master playlist naming one video variant (fragmented MP4 with an `EXT-X-MAP`, or
 *   MPEG-TS) and its audio renditions, or a media playlist alone (one muxed track). The media
 *   playlists are complete (`VOD`) or still growing from their first segment.
 * - A live channel: one media playlist of MPEG-TS segments that slides. Its first segment's
 *   number is `EXT-X-MEDIA-SEQUENCE`, each segment says when it was broadcast
 *   (`EXT-X-PROGRAM-DATE-TIME`), a change of programme is an `EXT-X-DISCONTINUITY`, and
 *   `EXT-X-START` says how far behind the newest segment to begin.
 *
 * Nothing here touches the device, so it runs under test as it is.
 */
export type Rendition = Readonly<{name: string; language: string; url: string; isDefault: boolean; codecs?: readonly string[]}>;
export type Segment = Readonly<{
  /** The segment's number in the stream: the playlist's media sequence plus its place in it. */
  number: number;
  url: string;
  /** Seconds from the first segment listed. Stable only in a playlist that does not slide. */
  start: number;
  duration: number;
  /** When the segment was broadcast, in milliseconds since 1970, when the playlist says. */
  dateMs?: number;
  /** The segment's clock does not follow the one before it. */
  discontinuity: boolean;
}>;
export type Playlist = {init: string | null; segments: Segment[]; ended: boolean; sequence: number; targetDuration: number; /** `EXT-X-START:TIME-OFFSET`: seconds from the start, or from the end when negative. */ startOffset?: number};
export type Master = {video: string; codecs: string[]; audio: Rendition[]};

export function attributes(line: string): Record<string, string> {
  const found: Record<string, string> = {};
  const pattern = /([A-Z0-9-]+)=("[^"]*"|[^,]*)/g;
  for (let match = pattern.exec(line); match; match = pattern.exec(line)) found[match[1]] = match[2].replace(/^"|"$/g, '');
  return found;
}

export const lines = (text: string): string[] => text.split(/\r?\n/).map(line => line.trim()).filter(Boolean);

export function parseMaster(text: string, base: string): Master | null {
  const all = lines(text);
  let best: {url: string; codecs: string[]; bandwidth: number; audioGroup?: string} | undefined;
  const audio: (Rendition & {group?: string})[] = [];
  all.forEach((line, index) => {
    if (line.startsWith('#EXT-X-MEDIA:')) {
      const a = attributes(line);
      if (a.TYPE === 'AUDIO' && a.URI) audio.push({name: a.NAME ?? '', language: a.LANGUAGE ?? '', url: new URL(a.URI, base).href, isDefault: a.DEFAULT === 'YES', group: a['GROUP-ID']});
    } else if (line.startsWith('#EXT-X-STREAM-INF:')) {
      const a = attributes(line), next = all[index + 1];
      const bandwidth = Number(a.BANDWIDTH) || 0;
      if (next && !next.startsWith('#') && (!best || bandwidth > best.bandwidth)) best = {url: new URL(next, base).href, codecs: (a.CODECS ?? '').split(',').map(codec => codec.trim()).filter(Boolean), bandwidth, audioGroup: a.AUDIO};
    }
  });
  if (!best) return null;
  const chosen = best, sound = chosen.codecs.filter(codec => !isVideoCodec(codec));
  return {video: chosen.url, codecs: chosen.codecs, audio: audio.filter(rendition => !chosen.audioGroup || rendition.group === chosen.audioGroup).map(({group: _group, ...rendition}) => ({...rendition, ...(sound.length === 1 ? {codecs: sound} : {})}))};
}

export function parseMedia(text: string, base: string): Playlist {
  const playlist: Playlist = {init: null, segments: [], ended: false, sequence: 0, targetDuration: 0};
  let at = 0, duration = 0, date: number | undefined, discontinuity = false;
  for (const line of lines(text)) {
    if (line.startsWith('#EXT-X-MAP:')) { const uri = attributes(line).URI; if (uri) playlist.init = new URL(uri, base).href; }
    else if (line.startsWith('#EXTINF:')) duration = Number(line.slice(8).split(',')[0]) || 0;
    else if (line.startsWith('#EXT-X-MEDIA-SEQUENCE:')) { const n = Number(line.slice(22)); if (Number.isSafeInteger(n) && n >= 0) playlist.sequence = n; }
    else if (line.startsWith('#EXT-X-TARGETDURATION:')) playlist.targetDuration = Number(line.slice(22)) || 0;
    else if (line.startsWith('#EXT-X-PROGRAM-DATE-TIME:')) { const ms = Date.parse(line.slice(25)); date = Number.isFinite(ms) ? ms : undefined; }
    else if (line === '#EXT-X-DISCONTINUITY') discontinuity = true;
    else if (line.startsWith('#EXT-X-START:')) { const offset = Number(attributes(line)['TIME-OFFSET']); if (Number.isFinite(offset)) playlist.startOffset = offset; }
    else if (line === '#EXT-X-ENDLIST') playlist.ended = true;
    else if (!line.startsWith('#')) {
      playlist.segments.push({number: playlist.sequence + playlist.segments.length, url: new URL(line, base).href, start: at, duration, ...(date !== undefined ? {dateMs: date} : {}), discontinuity});
      at += duration;
      // A date carries on through the segments that follow it until the playlist gives another.
      if (date !== undefined) date += duration * 1000;
      duration = 0;
      discontinuity = false;
    }
  }
  return playlist;
}

export const isVideoCodec = (codec: string): boolean => /^(avc|hvc|hev|dvh|av01|vp0)/i.test(codec);

/** The number of the segment that holds `position` seconds of a playlist that does not slide. At
 * or past the end of a finished stream: the last segment, so the player has something to arrive
 * at and can end; of one still growing: the segment that will come next. */
export function numberAt(playlist: Playlist, position: number): number {
  const segments = playlist.segments;
  const index = segments.findIndex(segment => segment.start + segment.duration > position);
  return playlist.sequence + (index >= 0 ? index : playlist.ended ? Math.max(0, segments.length - 1) : segments.length);
}

/** The segment that was on air at `dateMs`: the first one if that moment has left the playlist,
 * and none (undefined) when it has not been broadcast yet or the playlist carries no dates. */
export function segmentAtDate(playlist: Playlist, dateMs: number): Segment | undefined {
  const dated = playlist.segments.filter(segment => segment.dateMs !== undefined);
  if (!dated.length) return undefined;
  if (dateMs < dated[0].dateMs!) return dated[0];
  return dated.find(segment => dateMs < segment.dateMs! + segment.duration * 1000);
}

/** Where a live channel begins when nothing says otherwise: where `EXT-X-START` points, else
 * three segments behind the newest, so there is something to play while the next one is made. */
export function liveStart(playlist: Playlist): Segment | undefined {
  const segments = playlist.segments;
  if (!segments.length) return undefined;
  const offset = playlist.startOffset;
  if (offset === undefined) return segments[Math.max(0, segments.length - 3)];
  const last = segments[segments.length - 1];
  const at = offset < 0 ? last.start + last.duration + offset : offset;
  return segments.find(segment => segment.start + segment.duration > at) ?? last;
}

/** The segment numbered `number`, or the first one listed when that number has already left a
 * sliding playlist. Undefined when it is not listed yet. */
export function segmentNumbered(playlist: Playlist, number: number): Segment | undefined {
  return playlist.segments[Math.max(0, number - playlist.sequence)];
}
