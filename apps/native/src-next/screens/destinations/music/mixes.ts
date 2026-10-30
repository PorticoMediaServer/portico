import {createMusicMixesController, type MusicMixLifetime, type MusicMixPrincipal, type MusicMixRow} from '../../../../../../packages/client-core/src/music-mixes-controller';
import type {DetailApi} from '../../../../../../packages/client-core/src/detail';
import type {I18n} from '../../../../../../packages/i18n/src';
import type {NativeShellPlaybackAdapter} from '../../../shell/playback';

export type NativeMusicMixRow = Readonly<{id: string; name: string; tint: string; artists: readonly Readonly<{id: string; name: string}>[]; more: boolean;
  songCount: number; covers: readonly Readonly<{id: string; name: string; posterUrl?: string}>[]}>;
export type NativeMusicMixesSnapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error' | 'retired'; rows: readonly NativeMusicMixRow[]; error?: unknown}>;

export type NativeMusicMixesCaller = Readonly<{lifetime: MusicMixLifetime; dispose: () => void}>;

export type NativeMusicMixesPort = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => NativeMusicMixesSnapshot;
  /** A stable primitive navigation revision for the row's layout lease. */
  subscribeRevision: (listener: () => void) => () => void;
  getRevision: () => number;
  load: () => Promise<void>;
  play: (row: NativeMusicMixRow, caller: MusicMixLifetime, shuffle?: boolean) => Promise<void>;
  save: (row: NativeMusicMixRow, caller: MusicMixLifetime) => Promise<void>;
  /** The route and revision that committed the row drawing these cards; undefined off Home/Discover. */
  captureCaller: () => NativeMusicMixesCaller | undefined;
  /** The canonical controller's retirement, tied to the shell session that made it. */
  dispose: () => void;
}>;

type Options = Readonly<{
  api: DetailApi;
  /** Captured from the session that opened the shell; never re-read at play time. */
  principal: MusicMixPrincipal;
  requestId: () => Promise<string>;
  /** The original shell/session lease: the read and every play command live inside it. */
  signal: AbortSignal;
  isCurrent: () => boolean;
  /** The shell's own route/revision capture; the row holds one lease for its lifetime. */
  captureCaller: () => NativeMusicMixesCaller | undefined;
  subscribeRevision: (listener: () => void) => () => void;
  revision: () => number;
  playback: () => NativeShellPlaybackAdapter | undefined;
  t: I18n['t'];
}>;

const retired = () => Object.assign(new Error('native_music_mixes_retired'), {name: 'AbortError'});

/** music-plus (MC-9): one profile's own Mixes row. The shared controller owns the read,
 * validation, naming and mix play; this port only projects the server's real covers and
 * tint onto the drawn card and keeps the row inside the session that made it. */
export function createNativeMusicMixes(options: Options): NativeMusicMixesPort {
  const controller = createMusicMixesController({api: options.api, principal: options.principal, requestId: options.requestId,
    signal: options.signal, isCurrent: options.isCurrent, t: options.t});
  const listeners = new Set<() => void>(), projected = new Map<MusicMixRow, NativeMusicMixRow>(), currentRows = new Map<string, MusicMixRow>();
  let source = controller.getSnapshot(), disposed = false;
  let state: NativeMusicMixesSnapshot = Object.freeze({phase: source.phase, rows: Object.freeze([])});
  const project = (row: MusicMixRow): NativeMusicMixRow => Object.freeze({id: row.mix.id, name: row.name, tint: row.mix.tint, more: row.mix.more, songCount: row.mix.songCount,
    artists: Object.freeze(row.mix.artists.map(artist => Object.freeze({id: artist.id, name: artist.name}))),
    covers: Object.freeze(row.mix.covers.map(cover => Object.freeze({id: cover.album.id, name: cover.album.name, ...(cover.posterUrl ? {posterUrl: cover.posterUrl} : {})})))});
  /** A snapshot only changes when the controller's does; the UI reads one stable object. */
  const refresh = () => {
    source = controller.getSnapshot();
    currentRows.clear();
    const rows = Object.freeze(source.rows.map(row => {
      let value = projected.get(row);
      if (!value) {value = project(row); projected.set(row, value);}
      currentRows.set(value.id, row);
      return value;
    }));
    // Only the controller's current rows stay projected; a later response must not retain them all.
    const live = new Set(source.rows);
    for (const row of projected.keys()) if (!live.has(row)) projected.delete(row);
    if (state.phase !== source.phase || state.error !== source.error || state.rows.length !== rows.length || state.rows.some((row, index) => row !== rows[index])) {
      state = Object.freeze({phase: source.phase, rows, ...(source.error !== undefined ? {error: source.error} : {})});
    }
  };
  const notify = () => {refresh(); for (const listener of [...listeners]) listener();};
  const unsubscribe = controller.subscribe(notify);
  const stop = () => {
    if (disposed) return;
    disposed = true;
    unsubscribe();
    controller.dispose();
    refresh();
    for (const listener of [...listeners]) listener();
    listeners.clear();
  };
  if (options.signal.aborted) stop();
  return Object.freeze({
    subscribe(listener) {if (disposed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
    getSnapshot: () => {refresh(); return state;},
    subscribeRevision: options.subscribeRevision,
    getRevision: options.revision,
    load: () => controller.load(),
    captureCaller: () => disposed ? undefined : options.captureCaller(),
    async play(row: NativeMusicMixRow, caller: MusicMixLifetime, shuffle = false) {
      refresh();
      const mix = currentRows.get(row.id);
      // The drawn card must still be the controller's own current row, never a later one.
      if (disposed || !mix || projected.get(mix) !== row) throw retired();
      if (caller.signal.aborted || !caller.isCurrent()) throw retired();
      const port = options.playback()?.cataloguePlayback?.();
      if (!port) throw Object.assign(new Error('music_playback_unavailable'), {code: 'music_playback_unavailable'});
      await controller.play(mix, shuffle ? {playSegments: (segments, options) => port.playSegments(segments.map((segment, index) =>
        index === 0 ? {...segment, order: {mode: 'shuffle' as const}} : segment), options)} : port, caller);
    },
    async save(row: NativeMusicMixRow, caller: MusicMixLifetime) {
      refresh(); const mix = currentRows.get(row.id);
      if (disposed || !mix || projected.get(mix) !== row || caller.signal.aborted || !caller.isCurrent()) throw retired();
      await controller.save(mix, caller);
    },
    dispose: stop,
  });
}
