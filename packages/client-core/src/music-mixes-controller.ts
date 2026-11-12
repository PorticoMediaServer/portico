import {routePath,
  type GetMusicMixesResponse, type PostMusicMixesIdPlaylistResponse} from '../../contracts/src/index.ts';
import type {MusicMix, MusicMixPage} from '../../contracts/src/next/api.ts';
import {musicMixDisplayName} from './music-mixes.ts';
import {callServer, notOnThisServer} from './query/server-api.ts';
import type {V1QueuePlayer} from './playback-v1/v1-queue-player.ts';
import type {QueueHeader} from './playback-v1/types.ts';
import type {DetailApi} from './detail.ts';

type Mix = GetMusicMixesResponse['data']['mixes'][number];
type Immutable<T> = T extends object ? {readonly [K in keyof T]: Immutable<T[K]>} : T;
export type MusicMixRow = Readonly<{mix: Immutable<Mix>; name: string}>;
export type MusicMixLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
export type MusicMixesSnapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error' | 'retired'; rows: readonly MusicMixRow[]; error?: unknown}>;
export type MusicMixPrincipal = Readonly<{serverId: string; authority: 'local' | 'hosted'; accountId: string; profileId: string}>;
/** The app's transport. Its `requestRaw` sends Save's Idempotency-Key, so a Retry after a lost answer makes one playlist. */
type MixesApi = DetailApi & Partial<Readonly<{requestRaw(path: string, method: 'POST', options?: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}>): Promise<{status: number; headers: Record<string, string>; body?: unknown}>}>>;
export type MusicMixesOptions = Readonly<{api: MixesApi; principal: MusicMixPrincipal; requestId: () => Promise<string>;
  signal: AbortSignal; isCurrent: () => boolean; t: Parameters<typeof musicMixDisplayName>[2]}>;
const cancelled = () => Object.assign(new Error('Music Mixes changed'), {name: 'AbortError'});
const invalid = () => Object.assign(new Error('invalid_music_mixes'), {code: 'invalid_music_mixes'});
const status = (error: unknown, value: number) => typeof error === 'object' && error !== null && 'status' in error && error.status === value;
/** Native AbortSignal lacks the static any helper. Link only for the request's
 * lifetime, and remove every upstream listener on cancellation or completion. */
function linkedMusicMixSignal(signals: readonly AbortSignal[]) {
  const controller = new AbortController();
  const detach = () => {for (const source of signals) source.removeEventListener('abort', abort);};
  const abort = () => {controller.abort();detach();};
  for (const source of signals) {
    if (source.aborted) {abort();break;}
    source.addEventListener('abort', abort, {once: true});
  }
  return {signal: controller.signal, dispose: detach};
}
function freeze<T>(value: T): T {
  if (value && typeof value === 'object') {for (const child of Object.values(value)) freeze(child);Object.freeze(value);}
  return value;
}

/** One portable profile-scoped row for Home and Music Discover. Only the generated
 * read/copy contracts and canonical naming formatter define its contents. */
/** Whether the server has Mixes. The rebuilt server does not yet: see the stopgap in `read`. */
/** Save as playlist is in the row's menu: `POST /v1/music/mixes/{id}/playlist` saves the mix's songs as the profile sees
 * them now, as a new playlist, without touching the device's queue (#3027). */
export const mixSaveOnServer = true;
/** A stable tint for a mix, from its first cover's album: the rebuilt server sends none. */
function tintOf(albumId: string): string {
  let hash = 0;
  for (const char of albumId) hash = (hash * 31 + char.codePointAt(0)!) >>> 0;
  const hue = hash % 360, s = 0.35, l = 0.32, k = (n: number) => (n + hue / 30) % 12, a = s * Math.min(l, 1 - l);
  const channel = (n: number) => Math.round(255 * (l - a * Math.max(-1, Math.min(k(n) - 3, 9 - k(n), 1)))).toString(16).padStart(2, '0');
  return '#' + channel(0) + channel(8) + channel(4);
}
/** One mix of `GET /v1/music/mixes` in the shape the rows draw: named from its style, or else its first artist (area 6
 * music notes), "and more" when it has more artists, up to three covers. */
function mixOf(mix: MusicMix): Mix {
  const nameSource = mix.style ? {kind: 'style', model: 'musicnn-mtt', tag: mix.style, version: 'server'} : {kind: 'artist', artistId: mix.artists[0]?.id ?? ''};
  return {id: mix.id, artists: mix.artists.map(artist => ({id: artist.id, name: artist.name})), more: mix.moreArtists === true, songCount: mix.songs,
    covers: mix.covers.slice(0, 3).map(cover => ({album: {id: cover.albumId, name: ''}, posterUrl: cover.image ?? ''})),
    tint: tintOf(mix.covers[0]?.albumId ?? mix.id), nameSource} as Mix;
}
export function createMusicMixesController(options: MusicMixesOptions) {
  const api = {request: options.api.request.bind(options.api)}, sendRaw = options.api.requestRaw?.bind(options.api), principal = Object.freeze({serverId: options.principal.serverId,
    authority: options.principal.authority, accountId: options.principal.accountId, profileId: options.principal.profileId});
  const {requestId, signal, isCurrent, t} = options, life = new AbortController();
  const listeners = new Set<() => void>(), commands = new Set<AbortController>();
  const saves = new Map<MusicMixRow, Promise<PostMusicMixesIdPlaylistResponse | undefined>>();
  const receipts = new Map<string, Promise<string>>();
  let closed = false, generation = 0, raw: readonly Immutable<Mix>[] = [], read: AbortController | undefined;
  let pending: Promise<void> | undefined, playing: AbortController | undefined;
  let state: MusicMixesSnapshot = Object.freeze({phase: 'idle', rows: Object.freeze([])});
  const publish = (next: MusicMixesSnapshot) => {state = Object.freeze(next);for (const listener of [...listeners]) listener();};
  const abortCommands = () => {for (const controller of commands) controller.abort();commands.clear();saves.clear();playing = undefined;};
  const retire = (silent = false) => {
    if (closed) return;
    closed = true;generation++;signal.removeEventListener('abort', dispose);life.abort();read?.abort();read = undefined;pending = undefined;
    abortCommands();receipts.clear();raw = [];
    state = Object.freeze({phase: 'retired', rows: Object.freeze([])});
    try {if (!silent) for (const listener of [...listeners]) listener();} finally {listeners.clear();}
  };
  const dispose = () => retire();
  const current = () => {
    if (closed) return false;
    if (signal.aborted || !isCurrent()) {dispose();return false;}
    return true;
  };
  const names = (): readonly MusicMixRow[] => Object.freeze(raw.flatMap(mix => {
    const name = musicMixDisplayName(mix.nameSource, mix.artists, t);
    return name === undefined ? [] : [Object.freeze({mix, name})];
  }));
  const contains = (row: MusicMixRow) => current() && state.phase === 'ready' && state.rows.includes(row);
  const relabel = () => {
    if (!current() || state.phase !== 'ready') return;
    const rows = names();
    if (rows.length === state.rows.length && rows.every((row, index) => row.mix === state.rows[index].mix && row.name === state.rows[index].name)) return;
    abortCommands();publish({phase: 'ready', rows});
  };
  const load = (): Promise<void> => {
    if (!current()) return Promise.reject(cancelled());
    if (pending) return pending;
    const controller = new AbortController(), ticket = ++generation;read = controller;
    const linked = linkedMusicMixSignal([signal, life.signal, controller.signal]), active = linked.signal;
    abortCommands();raw = [];
    const task = Promise.resolve().then(async () => {
      try {
        if (!current() || ticket !== generation || active.aborted) throw cancelled();
        // The profile's mixes, at most six; none means the row is absent (not enough listening analysed yet).
        const page: MusicMixPage = await callServer(api, 'GET /v1/music/mixes', {signal: active});
        if (!current() || ticket !== generation || active.aborted) throw cancelled();
        if (!page || !Array.isArray(page.items) || new Set(page.items.map(mix => mix.id)).size !== page.items.length) throw invalid();
        raw = freeze(page.items.map(mixOf));publish({phase: 'ready', rows: names()});
      } catch (error) {
        // Mixes are not on the rebuilt server yet: the row is simply absent, with no message.
        if (notOnThisServer(error)) {if (current() && ticket === generation && !active.aborted) publish({phase: 'ready', rows: Object.freeze([])});return;}
        if (status(error, 401) || status(error, 403)) dispose();
        if (current() && ticket === generation && !active.aborted) publish({phase: 'error', rows: Object.freeze([]), error});
        throw error;
      } finally {linked.dispose();if (read === controller) {read = undefined;pending = undefined;}}
    });
    pending = task;publish({phase: 'loading', rows: Object.freeze([])});return task;
  };
  const capture = (row: MusicMixRow, caller: MusicMixLifetime) => {
    if (!contains(row) || caller.signal.aborted || !caller.isCurrent()) throw cancelled();
    const ticket = generation, controller = new AbortController();commands.add(controller);
    const linked = linkedMusicMixSignal([signal, life.signal, controller.signal, caller.signal]), active = linked.signal;
    const valid = () => !active.aborted && ticket === generation && contains(row) && caller.isCurrent();
    const assert = () => {if (!valid()) throw cancelled();};
    return {controller, signal: active, isCurrent: valid, assert, finish: () => {linked.dispose();commands.delete(controller);}};
  };
  const stale = async (error: unknown, stillCurrent: () => boolean): Promise<boolean> => {
    if (status(error, 401) || status(error, 403)) {dispose();return false;}
    if (!status(error, 404) || !stillCurrent()) return false;
    // Refresh the existing state only. Never retry a copy or make another name.
    await load();return true;
  };
  if (signal.aborted) dispose();else signal.addEventListener('abort', dispose, {once: true});
  return Object.freeze({
    getSnapshot: () => {if (!closed && (signal.aborted || !isCurrent())) retire(true);return state;},
    subscribe(listener: () => void) {if (closed) return () => {};listeners.add(listener);return () => {listeners.delete(listener);};},
    load, relabel, contains,
    async play(row: MusicMixRow, port: Pick<V1QueuePlayer, 'playSegments'>, caller: MusicMixLifetime, shuffle = false): Promise<QueueHeader | undefined> {
      const command = capture(row, caller);playing?.abort();playing = command.controller;
      try {
        const result = await port.playSegments.call(port, [{source: {container: {kind: 'mix', id: row.mix.id}}, ...(shuffle ? {order: {mode: 'shuffle' as const}} : {})}],
          {signal: command.signal, isCurrent: command.isCurrent});
        command.assert();return result;
      } catch (error) {if (await stale(error, command.isCurrent)) return undefined;throw error;}
      finally {command.finish();if (playing === command.controller) playing = undefined;}
    },
    async save(row: MusicMixRow, caller: MusicMixLifetime): Promise<PostMusicMixesIdPlaylistResponse | undefined> {
      // A second caller never inherits the first caller's navigation lease.
      if (saves.has(row)) return Promise.reject(cancelled());
      const command = capture(row, caller);
      const key = JSON.stringify([row.mix.id, row.name]);
      let operation = receipts.get(key);
      if (!operation) {
        operation = Promise.resolve().then(requestId);receipts.set(key, operation);
        const issued = operation;
        void issued.catch(() => {if (receipts.get(key) === issued) receipts.delete(key);});
      }
      const issued = operation;
      const task = Promise.resolve().then(async () => {
        try {
          const operationId = await issued;command.assert();
          // The playlist is named as the row shows the mix; the operation is its Idempotency-Key, which a Retry keeps.
          const path = routePath('post_music_mixes_id_playlist', {id: row.mix.id}), body = {name: row.name};
          const list = sendRaw ? await sendRaw(path, 'POST', {headers: {'Idempotency-Key': operationId}, body, signal: command.signal}).then(answer => {
            if (answer.status >= 400) {
              const error = (answer.body as {error?: {code?: string; message?: string}} | undefined)?.error;
              throw Object.assign(new Error(error?.message ?? 'The playlist could not be made.'), {status: answer.status, code: error?.code ?? 'request_failed'});
            }
            return answer.body;
          }) : await api.request<unknown>(path, 'POST', body, command.signal);
          command.assert();
          const made = list as {id?: unknown; revision?: unknown} | undefined;
          if (typeof made?.id !== 'string' || !made.id) throw invalid();
          // The rebuilt server answers the new playlist; the screens read the receipt they always did.
          const response: PostMusicMixesIdPlaylistResponse = {serverId: principal.serverId, operationId, playlistId: made.id, revision: Number(made.revision) || 0, deleted: false, viewerFence: 'viewer'};
          if (receipts.get(key) === issued) receipts.delete(key);
          return freeze(response);
        } catch (error) {if (await stale(error, command.isCurrent)) return undefined;throw error;}
        finally {command.finish();if (saves.get(row) === task) saves.delete(row);}
      });
      saves.set(row, task);return task;
    },
    dispose,
  });
}
export type MusicMixesController = ReturnType<typeof createMusicMixesController>;
