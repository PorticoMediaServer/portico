import {decodeGetMusicMixesResponse, decodePostMusicMixesIdPlaylistResponse, routePath,
  type GetMusicMixesResponse, type PostMusicMixesIdPlaylistResponse} from '../../contracts/src/index.ts';
import {musicMixDisplayName} from './music-mixes.ts';
import type {V1QueuePlayer} from './playback-v1/v1-queue-player.ts';
import type {QueueHeader} from './playback-v1/types.ts';
import type {DetailApi} from './detail.ts';

type Mix = GetMusicMixesResponse['data']['mixes'][number];
type Immutable<T> = T extends object ? {readonly [K in keyof T]: Immutable<T[K]>} : T;
export type MusicMixRow = Readonly<{mix: Immutable<Mix>; name: string}>;
export type MusicMixLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
export type MusicMixesSnapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error' | 'retired'; rows: readonly MusicMixRow[]; error?: unknown}>;
export type MusicMixPrincipal = Readonly<{serverId: string; authority: 'local' | 'hosted'; accountId: string; profileId: string}>;
export type MusicMixesOptions = Readonly<{api: DetailApi; principal: MusicMixPrincipal; requestId: () => Promise<string>;
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
export function createMusicMixesController(options: MusicMixesOptions) {
  const api = {request: options.api.request.bind(options.api)}, principal = Object.freeze({serverId: options.principal.serverId,
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
        const response = decodeGetMusicMixesResponse(await api.request<unknown>(routePath('get_music_mixes'), 'GET', undefined, active));
        if (!current() || ticket !== generation || active.aborted) throw cancelled();
        if (Object.entries(principal).some(([key, value]) => response.scope[key as keyof MusicMixPrincipal] !== value)) {dispose();throw invalid();}
        if (new Set(response.data.mixes.map(mix => mix.id)).size !== response.data.mixes.length) throw invalid();
        raw = freeze(response.data.mixes);publish({phase: 'ready', rows: names()});
      } catch (error) {
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
          const response = decodePostMusicMixesIdPlaylistResponse(await api.request<unknown>(routePath('post_music_mixes_id_playlist', {id: row.mix.id}),
            'POST', {operationId, name: row.name}, command.signal));
          command.assert();
          if (response.serverId !== principal.serverId || response.operationId !== operationId) throw invalid();
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
