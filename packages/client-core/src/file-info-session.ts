import type {Item, ItemFiles, PlaybackOptions, Stream} from '../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';
import {unreadableServerResponse} from './server-messages.ts';
import type {FileInfoDiscTitle} from './detail.ts';
import type {FileInfoRows, FileInfoFileRow, FileInfoGroupRow, FileInfoHeadTrackRow, FileInfoSurface, FileInfoTrackLane, FileInfoTrackRow} from './file-info-pages.ts';

export type FileInfoLane<Row> = Readonly<{status: 'loading' | 'preparing' | 'ready' | 'failed'; rows?: FileInfoRows<Row>; total?: number; error?: unknown}>;
export type {FileInfoTrackLane};
export type FileInfoSnapshot = Readonly<{
  groups: FileInfoLane<FileInfoGroupRow>; files?: FileInfoLane<FileInfoFileRow>; surface?: FileInfoSurface;
  tracks: ReadonlyMap<string, FileInfoLane<FileInfoTrackRow>>;
  headTracks: ReadonlyMap<string, FileInfoLane<FileInfoHeadTrackRow>>;
  members: ReadonlyMap<string, FileInfoLane<FileInfoFileRow>>;
  disc?: FileInfoLane<FileInfoDiscTitle> & Readonly<{occurrence?: string; discKind?: 'bluray' | 'dvd'}>;
}>;
export const fileInfoTrackKey = (id: string, lane: FileInfoTrackLane) => `${lane}:${id}`;
/** App card kinds that have their own versions, including a multipart audiobook. */
export const fileInfoUsesGroups = (kind: string) => ['movie', 'episode', 'extra', 'song', 'audiobook_file', 'book', 'ebook', 'comic'].includes(kind);
export type FileInfoSessionOptions = Readonly<{
  api: ServerRequester; itemId: string; role: string; isCurrent(): boolean; signal?: AbortSignal;
}>;
const ready = <Row>(rows: readonly Row[]): FileInfoLane<Row> => Object.freeze({status: 'ready', total: rows.length,
  rows: Object.freeze({rows: Object.freeze(rows), more: false, earlier: false})});
const invalid = (): never => {throw new Error(unreadableServerResponse);};
const text = (value: unknown): value is string => typeof value === 'string' && !value.includes('\0');
const number = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value) && value >= 0;
const video = (streams: readonly Stream[]) => {
  const s = streams.find(s => s.kind === 'video');
  return s ? {codec: s.codec ?? '', profile: s.profile, width: s.width ?? 0, height: s.height ?? 0,
    dynamicRange: s.hdr, frameRate: s.frameRate, bitDepth: s.bitDepth} : undefined;
};
function tracks(streams: readonly Stream[], lane: FileInfoTrackLane): FileInfoTrackRow[] {
  return streams.filter(s => s.kind === (lane === 'audio' ? 'audio' : 'subtitle')).map((s, index) => ({
    ordinal: index + 1, codec: s.codec ?? '', language: s.language, title: s.title, channels: s.channels, channelLayout: s.channelLayout,
    bitRate: s.bitrate, default: s.default, forced: s.forced, hearingImpaired: s.hearingImpaired, profile: s.profile,
    sampleRate: s.sampleRate, bitDepth: s.bitDepth,
  }));
}
function checkedStreams(value: unknown): Stream[] {
  if (!Array.isArray(value) || value.some(s => !s || typeof s !== 'object' || !text(s.kind) || !Number.isInteger(s.index))) invalid();
  return value as Stream[];
}
type Version = Readonly<{id: string; files: readonly FileInfoFileRow[]; streams: readonly Stream[][]; group: FileInfoGroupRow}>;

/** One panel reads the existing title and, only for a manager, its recorded file locations.
 * No opening token, preparation handle, cursor or background poll. Closing/authority change
 * aborts the read and prevents late answers from reaching either a screen or a command. */
export class FileInfoSession {
  private snapshot: FileInfoSnapshot = Object.freeze({groups: {status: 'loading'}, tracks: new Map(), headTracks: new Map(), members: new Map()});
  private readonly listeners = new Set<() => void>();
  private readonly controller = new AbortController();
  private versions?: Promise<readonly Version[]>;
  private offered?: Promise<PlaybackOptions>;
  private readonly abort = () => this.retire();
  private readonly options: FileInfoSessionOptions;
  constructor(options: FileInfoSessionOptions) {
    this.options = options;
    options.signal?.addEventListener('abort', this.abort, {once: true});
    if (options.signal?.aborted) this.retire();
  }
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.snapshot;
  private live = () => !this.controller.signal.aborted && this.options.isCurrent();
  private publish(next: Partial<FileInfoSnapshot>) {
    if (!this.live()) return;
    this.snapshot = Object.freeze({...this.snapshot, ...next});
    this.listeners.forEach(listener => listener());
  }
  retire() {this.controller.abort(); this.options.signal?.removeEventListener('abort', this.abort); this.listeners.clear();}
  private load() {
    return this.versions ??= this.read().catch(error => {this.versions = undefined; throw error;});
  }
  private async read(): Promise<readonly Version[]> {
    if (!this.live()) throw new DOMException('File info retired', 'AbortError');
    const {api, itemId, role} = this.options, signal = this.controller.signal, manage = role === 'owner' || role === 'admin';
    const [item, locations] = await Promise.all([
      callServer(api, 'GET /v1/items/{id}', {id: itemId, signal}),
      manage ? callServer(api, 'GET /v1/items/{id}/files', {id: itemId, signal}) : undefined,
    ]);
    if (!this.live()) throw new DOMException('File info retired', 'AbortError');
    if (item.id !== itemId || !Array.isArray(item.versions ?? []) || locations && !Array.isArray(locations.versions)) invalid();
    const versions = this.mapVersions(item, locations);
    const fileCount = versions.reduce((sum, v) => sum + v.files.length, 0);
    // Deleting a version is the owner's alone (03-scanning rule 64; the server's GET/DELETE /v1/admin/versions/{id}
    // are owner-only), with the exact version preview first. Administrators see paths, split and merge (rule 25).
    this.publish({surface: Object.freeze({fileCount, eligibleFileCount: fileCount, groupCount: versions.length, canSplit: versions.length > 1,
      hasDiscColumns: false, ...(manage ? {manage: {delete: role === 'owner', split: versions.length > 1, merge: true}} : {})})});
    return versions;
  }
  private mapVersions(item: Item, locations?: ItemFiles): Version[] {
    const media = item.versions ?? [], recorded = new Map(locations?.versions.map(v => [v.id, v]));
    const ids = [...media.map(v => v.id), ...[...recorded.keys()].filter(id => !media.some(v => v.id === id))];
    if (new Set(ids).size !== ids.length || ids.some(id => !text(id) || !id)) invalid();
    return ids.map((id, index) => {
      const m = media.find(v => v.id === id), held = recorded.get(id);
      if (m && !Array.isArray(m.parts) || held && !Array.isArray(held.files)) invalid();
      const parts = held?.files ?? m!.parts;
      const streams = parts.map(p => checkedStreams(p.streams));
      const files: FileInfoFileRow[] = parts.map((p, part) => {
        const place = held?.files[part], durationMs = 'durationMs' in p ? p.durationMs : undefined;
        if (!number(p.size) || durationMs !== undefined && !number(durationMs) || place && !text(place.path)) invalid();
        return Object.freeze({occurrence: `${id}:${part}`, itemId: item.id, id, size: {value: p.size}, container: place?.container ?? m?.container ?? '',
          available: !place?.missingSinceAt, partIndex: part, ...(place ? {path: place.path, missingSinceAt: place.missingSinceAt} : {}), edition: m?.edition,
          ...(durationMs ? {duration: durationMs / 1000} : {}), video: video(streams[part]),
          audioCount: tracks(streams[part], 'audio').length, subtitleCount: tracks(streams[part], 'subtitles').length});
      });
      const head = streams[0] ?? [], size = m?.size ?? files.reduce((sum, f) => sum + (f.size.value ?? 0), 0);
      const group: FileInfoGroupRow = Object.freeze({id, headOccurrence: id, ordinal: index + 1, memberCount: files.length, edition: m?.edition,
        container: m?.container ?? held?.files[0]?.container ?? '', size: {value: size}, durationMs: m?.durationMs,
        video: video(head), audioCount: tracks(head, 'audio').length, subtitleCount: tracks(head, 'subtitles').length});
      return {id, files, streams, group};
    });
  }
  private async run<Row>(read: (versions: readonly Version[]) => readonly Row[] | Promise<readonly Row[]>, store: (value: FileInfoLane<Row>) => void) {
    if (!this.live()) return;
    store({status: 'loading'});
    try {
      const versions = await this.load();
      if (!this.live()) return;
      const rows = await read(versions);
      if (this.live()) store(ready(rows));
    }
    catch (error) {if (this.live()) store({status: 'failed', error});}
  }
  groups(_mode?: 'open' | 'next' | 'restart') {return this.run(versions => versions.map(v => v.group), groups => this.publish({groups}));}
  files(_mode?: 'open' | 'next' | 'restart') {return this.run(versions => versions.flatMap(v => v.files), files => this.publish({files}));}
  members(id: string, _mode?: 'open' | 'next' | 'restart') {
    if (!this.snapshot.surface?.manage) return Promise.resolve();
    return this.run(versions => versions.find(v => v.id === id)?.files ?? [], value => this.publish({members: new Map([...this.snapshot.members, [id, value]])}));
  }
  tracks(at: string, lane: FileInfoTrackLane, _mode?: 'open' | 'next' | 'restart') {
    const key = fileInfoTrackKey(at, lane);
    return this.run(versions => {
      const version = versions.find(v => v.id === at || v.files.some(f => f.occurrence === at));
      const part = version?.files.findIndex(f => f.occurrence === at) ?? -1;
      return tracks(version?.streams[Math.max(0, part)] ?? [], lane);
    }, value => this.publish({tracks: new Map([...this.snapshot.tracks, [key, value]])}));
  }
  headTracks(id: string, lane: FileInfoTrackLane, _mode?: 'open' | 'next' | 'restart') {
    const key = fileInfoTrackKey(id, lane);
    return this.run(async () => {
      const offered = await (this.offered ??= callServer(this.options.api, 'GET /v1/items/{id}/playback-options',
        {id: this.options.itemId, signal: this.controller.signal}).catch(error => {this.offered = undefined; throw error;}));
      if (offered.itemId !== this.options.itemId || !Array.isArray(offered.versions)) invalid();
      const version = offered.versions.find(v => v.id === id);
      if (!version) return invalid();
      const rows = lane === 'audio' ? version.audio : version.subtitles;
      if (!Array.isArray(rows)) invalid();
      return rows.map((t, index): FileInfoHeadTrackRow => {
        if (!text(t.id) || !t.id) invalid();
        return {id: t.id, ordinal: index + 1, codec: 'codec' in t ? t.codec : t.format,
          language: t.language, title: t.title, default: t.default,
          ...('codec' in t ? {channels: t.channels, channelLayout: t.layout, commentary: t.commentary} : {}),
          ...('source' in t ? {external: t.source !== 'embedded', forced: t.forced, hearingImpaired: t.sdh} : {})};
      });
    }, value => this.publish({headTracks: new Map([...this.snapshot.headTracks, [key, value]])}));
  }

  /** The rebuilt server does not yet publish a disc-title inventory. */
  disc(_mode?: 'open' | 'next' | 'restart') {return Promise.resolve();}
}
