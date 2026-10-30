import {routePath} from '../../contracts/src/route-paths.ts';
import {FileInfoWindowSession, fileInfoRows, type FileInfoRows, type FileInfoWindowFacts} from './file-info-windows.ts';
import {
  FILE_INFO_DEFAULT_LIMIT, fileInfoReadyQueryId, parseFileInfoDiscPage, parseFileInfoFilesPage, parseFileInfoGroupsPage, parseFileInfoHeadTracksPage, parseFileInfoMembersPage, parseFileInfoPreparing, parseFileInfoTracksPage,
  type FileInfoDiscPage, type FileInfoFileRow, type FileInfoGroupRow, type FileInfoHeadTrackRow, type FileInfoOpening, type FileInfoSurface, type FileInfoTrackLane, type FileInfoTrackRow,
} from './file-info-pages.ts';
import type {PreparationRequestPort, PreparationResponse, PreparationRuntime} from './read-preparation.ts';
import {browsePreparationError} from './browse-preparation.ts';

/** Every FileInfo request is built here, from the generated route builders (P2-29's contract). */
export const fileInfoPaths = Object.freeze({
  files: (itemId: string) => routePath('get_items_id_file_info', {id: itemId}),
  groups: (itemId: string) => routePath('get_item_file_info_groups', {id: itemId}),
  members: (itemId: string) => routePath('get_item_file_info_members', {id: itemId}),
  tracks: (itemId: string) => routePath('get_item_file_info_tracks', {id: itemId}),
  headTracks: (itemId: string) => routePath('get_item_file_info_head_tracks', {id: itemId}),
  sources: (itemId: string) => routePath('get_item_file_info_sources', {id: itemId}),
  disc: (itemId: string) => routePath('get_item_file_info_disc_titles', {id: itemId}),
  query: (queryId: string) => routePath('get_file_info_query', {queryId}),
});

export type FileInfoLane<Row> = Readonly<{
  status: 'loading' | 'preparing' | 'ready' | 'failed';
  rows?: FileInfoRows<Row>;
  /** Total of the whole lane where the server publishes one. */
  total?: number;
  error?: unknown;
}>;
export type {FileInfoTrackLane};
export type FileInfoSnapshot = Readonly<{
  /** Versions of a playable item. Absent on a flat-file surface. */
  groups: FileInfoLane<FileInfoGroupRow>;
  /** The flat files of any other kind (an album, a book…), in the server's file order. */
  files?: FileInfoLane<FileInfoFileRow>;
  surface?: FileInfoSurface;
  /** Raw tracks, keyed by `fileInfoTrackKey(occurrence, lane)`. Absent until a renderer asks. */
  tracks: ReadonlyMap<string, FileInfoLane<FileInfoTrackRow>>;
  /** Offered head tracks, keyed by `fileInfoTrackKey(groupId, lane)`. */
  headTracks: ReadonlyMap<string, FileInfoLane<FileInfoHeadTrackRow>>;
  /** Owner only, keyed by group id. */
  members: ReadonlyMap<string, FileInfoLane<FileInfoFileRow>>;
  disc?: FileInfoLane<FileInfoDiscPage['titles'][number]> & Readonly<{occurrence?: string; discKind?: FileInfoDiscPage['discKind']}>;
}>;
export const fileInfoTrackKey = (occurrence: string, lane: FileInfoTrackLane) => `${lane}:${occurrence}`;
/** Playable single items (catalog `playable`) read versions as groups; every other kind (albums,
 * books, shows…) reads its flat files. Groups on another kind are refused by the server. */
const playable: readonly string[] = ['movie', 'episode', 'extra', 'song', 'audiobook_file'];
export const fileInfoUsesGroups = (kind: string) => playable.includes(kind);

export type FileInfoSessionOptions = Readonly<{
  api: PreparationRequestPort;
  opening: FileInfoOpening;
  /** Defaults to the shared typed preparation error (code, retry hint, response). */
  errorFromResponse?(response: PreparationResponse): Error;
  isCurrent(): boolean;
  signal?: AbortSignal;
  runtime?: Partial<PreparationRuntime>;
  limit?: number;
}>;

type Lane<Read, Page, Row> = {
  session: FileInfoWindowSession<Read, Page>;
  read: Read;
  rows(page: Page): readonly Row[];
  total?(page: Page): number | undefined;
};

/** One File info opening: the accepted Detail it was made from, its lanes and their resident
 * rows. Lanes are read only when a renderer asks, one window at a time, and a lane holds its
 * server lease only while it has more to read. Retire it with the panel. */
export class FileInfoSession {
  private readonly options: FileInfoSessionOptions;
  private readonly listeners = new Set<() => void>();
  private snapshot: FileInfoSnapshot = Object.freeze({groups: Object.freeze({status: 'loading'}), tracks: new Map(), headTracks: new Map(), members: new Map()});
  private readonly lanes = new Map<string, Lane<unknown, unknown, unknown>>();
  private readonly controller = new AbortController();
  private retired = false;
  /** A window of this opening has been read: from then on every preparation must name its Detail. */
  private accepted = false;

  constructor(options: FileInfoSessionOptions) {
    this.options = options;
    options.signal?.addEventListener('abort', () => this.retire(), {once: true});
  }
  /** The opening this session reads; commands carry its token with an exact occurrence. */
  get opening(): FileInfoOpening { return this.options.opening; }
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => this.listeners.delete(listener); };
  getSnapshot = (): FileInfoSnapshot => this.snapshot;
  private live = () => !this.retired && !this.controller.signal.aborted && this.options.isCurrent();
  private publish(next: Partial<FileInfoSnapshot>): void {
    if (!this.live()) return;
    this.snapshot = Object.freeze({...this.snapshot, ...next});
    for (const listener of [...this.listeners]) listener();
  }
  retire(): void {
    if (this.retired) return;
    this.retired = true;
    this.controller.abort();
    for (const lane of this.lanes.values()) lane.session.retire();
    this.lanes.clear();
    this.listeners.clear();
  }
  private limit(): number { return this.options.limit ?? FILE_INFO_DEFAULT_LIMIT; }
  private window<Read extends Readonly<{cursor?: string}>, Page extends Readonly<{window: FileInfoWindowFacts}>>(path: (read: Read, cursor?: string) => string, parse: (body: unknown, response: PreparationResponse) => Page, onPreparing: () => void) {
    return new FileInfoWindowSession<Read, Page>({
      api: this.options.api, path, poll: fileInfoPaths.query, cancel: fileInfoPaths.query,
      parse: (body, _read, response) => {const page = parse(body, response); this.accepted = true; return page;}, window: page => page.window,
      parsePreparing: (body, response) => parseFileInfoPreparing(body, this.options.opening, response, !this.accepted),
      readyQueryId: fileInfoReadyQueryId, errorFromResponse: this.options.errorFromResponse ?? browsePreparationError,
      isCurrent: this.live, signal: this.controller.signal, runtime: this.options.runtime, onPreparing,
    });
  }
  /** Drive one lane: open or continue it, keep its resident rows, and give up its lease when it
   * has nothing more to read. A failure keeps the rows already shown. */
  private async drive<Page extends Readonly<{window: FileInfoWindowFacts}>, Row>(
    lane: Lane<Readonly<{cursor?: string}>, Page, Row>, mode: 'open' | 'next' | 'restart',
    current: () => FileInfoLane<Row> | undefined, store: (value: FileInfoLane<Row>) => void, after?: (page: Page) => void,
  ): Promise<void> {
    const held = current();
    store(Object.freeze({...held, status: 'loading', error: undefined}));
    try {
      const result = mode === 'next' ? await lane.session.next() : await lane.session.open(lane.read);
      if (!this.live()) return;
      const rows = fileInfoRows(mode === 'next' && !result.reopened ? held?.rows : undefined, lane.rows(result.page), result.page.window, mode === 'next' && !result.reopened ? 'append' : 'replace');
      const total = lane.total?.(result.page);
      store(Object.freeze({status: 'ready', rows, ...(total !== undefined ? {total} : {})}));
      after?.(result.page);
      if (!result.page.window.next) lane.session.cancel();
    } catch (error) {
      if (!this.live() || (error as {name?: string})?.name === 'AbortError') return;
      store(Object.freeze({...current(), status: 'failed', error}));
    }
  }
  private lane<Page extends Readonly<{window: FileInfoWindowFacts}>, Row>(key: string, make: () => Lane<Readonly<{cursor?: string}>, Page, Row>): Lane<Readonly<{cursor?: string}>, Page, Row> {
    let lane = this.lanes.get(key) as Lane<Readonly<{cursor?: string}>, Page, Row> | undefined;
    if (!lane) { lane = make(); this.lanes.set(key, lane as Lane<unknown, unknown, unknown>); }
    return lane;
  }

  /** A flat-file surface's files, with the same surface facts as groups. */
  files(mode: 'open' | 'next' | 'restart' = 'open'): Promise<void> {
    const {opening} = this.options, item = opening.target.itemId;
    const lane = this.lane('files', () => ({
      read: {},
      session: this.window((_read, cursor) => lanePath(fileInfoPaths.files(item), opening, this.limit(), cursor),
        (body, response) => parseFileInfoFilesPage(body, opening, response), () => this.publish({files: Object.freeze({...this.snapshot.files, status: 'preparing'})})),
      rows: page => page.files,
    }));
    return this.drive(lane, mode, () => this.snapshot.files, value => this.publish({files: value}), page => this.publish({surface: page.surface}));
  }
  /** The versions. `more` reads the next window; `restart` reads the lane from its start. */
  groups(mode: 'open' | 'next' | 'restart' = 'open'): Promise<void> {
    const {opening} = this.options, item = opening.target.itemId;
    const lane = this.lane('groups', () => ({
      read: {},
      session: this.window((_read, cursor) => lanePath(fileInfoPaths.groups(item), opening, this.limit(), cursor),
        (body, response) => parseFileInfoGroupsPage(body, opening, response), () => this.publish({groups: Object.freeze({...this.snapshot.groups, status: 'preparing'})})),
      rows: page => page.groups,
    }));
    return this.drive(lane, mode, () => this.snapshot.groups, value => this.publish({groups: value}), page => this.publish({surface: page.surface}));
  }
  /** One version's raw audio or subtitle tracks, read from its head file. */
  tracks(occurrence: string, kind: FileInfoTrackLane, mode: 'open' | 'next' | 'restart' = 'open'): Promise<void> {
    const {opening} = this.options, item = opening.target.itemId, key = fileInfoTrackKey(occurrence, kind);
    const set = (value: FileInfoLane<FileInfoTrackRow>) => this.publish({tracks: new Map([...this.snapshot.tracks, [key, value]])});
    const lane = this.lane(key, () => ({
      read: {},
      session: this.window((_read, cursor) => lanePath(fileInfoPaths.tracks(item), opening, this.limit(), cursor, {occurrence, lane: kind}),
        (body, response) => parseFileInfoTracksPage(body, opening, kind, occurrence, response), () => set(Object.freeze({...this.snapshot.tracks.get(key), status: 'preparing'}))),
      rows: page => page.tracks, total: page => page.total,
    }));
    return this.drive(lane, mode, () => this.snapshot.tracks.get(key), set);
  }
  /** Owner only: the files of one version, with their paths. */
  members(groupId: string, mode: 'open' | 'next' | 'restart' = 'open'): Promise<void> {
    const {opening} = this.options, item = opening.target.itemId, key = `members:${groupId}`, owner = !!this.snapshot.surface?.manage;
    const set = (value: FileInfoLane<FileInfoFileRow>) => this.publish({members: new Map([...this.snapshot.members, [groupId, value]])});
    const lane = this.lane(key, () => ({
      read: {},
      session: this.window((_read, cursor) => lanePath(fileInfoPaths.members(item), opening, this.limit(), cursor, {group: groupId}),
        (body, response) => parseFileInfoMembersPage(body, opening, groupId, owner, response), () => set(Object.freeze({...this.snapshot.members.get(groupId), status: 'preparing'}))),
      rows: page => page.members, total: page => page.total,
    }));
    return this.drive(lane, mode, () => this.snapshot.members.get(groupId), set);
  }
  /** Native: one version's offered head tracks. */
  headTracks(groupId: string, kind: FileInfoTrackLane, mode: 'open' | 'next' | 'restart' = 'open'): Promise<void> {
    const {opening} = this.options, item = opening.target.itemId, key = fileInfoTrackKey(groupId, kind);
    const set = (value: FileInfoLane<FileInfoHeadTrackRow>) => this.publish({headTracks: new Map([...this.snapshot.headTracks, [key, value]])});
    const lane = this.lane(`head:${key}`, () => ({
      read: {},
      session: this.window((_read, cursor) => lanePath(fileInfoPaths.headTracks(item), opening, this.limit(), cursor, {group: groupId, lane: kind}),
        (body, response) => parseFileInfoHeadTracksPage(body, opening, kind, groupId, response), () => set(Object.freeze({...this.snapshot.headTracks.get(key), status: 'preparing'}))),
      rows: page => page.tracks, total: page => page.total,
    }));
    return this.drive(lane, mode, () => this.snapshot.headTracks.get(key), set);
  }
  /** Owner only: the titles of the surface's owner-disc file, once the surface names it. */
  disc(mode: 'open' | 'next' | 'restart' = 'open'): Promise<void> {
    const {opening} = this.options, item = opening.target.itemId, at = this.snapshot.surface?.ownerDiscOccurrence;
    if (!at) return Promise.resolve();
    type Disc = NonNullable<FileInfoSnapshot['disc']>;
    const set = (value: Disc) => this.publish({disc: Object.freeze({...this.snapshot.disc, ...value})});
    const lane = this.lane(`disc:${at}`, () => ({
      read: {},
      session: this.window((_read, cursor) => lanePath(fileInfoPaths.disc(item), opening, this.limit(), cursor, {occurrence: at}),
        (body, response) => parseFileInfoDiscPage(body, opening, at, response), () => set(Object.freeze({status: 'preparing'}) as Disc)),
      rows: page => page.titles, total: page => page.total,
    }));
    return this.drive(lane, mode, () => this.snapshot.disc, value => set(value as Disc), page => set(Object.freeze({...this.snapshot.disc!, occurrence: page.occurrence, discKind: page.discKind})));
  }
}

/** Every lane names the opening. A continuation inherits its limit; only a first window sets it. */
function lanePath(path: string, opening: FileInfoOpening, limit: number, cursor: string | undefined, query: Readonly<Record<string, string | undefined>> = {}): string {
  return withQuery(path, {opening: opening.token, ...query, ...(cursor ? {cursor} : {limit})});
}
function withQuery(path: string, query: Readonly<Record<string, string | number | undefined>>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) if (value !== undefined) search.set(key, String(value));
  const text = search.toString();
  return text ? `${path}?${text}` : path;
}
