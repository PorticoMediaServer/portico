import type {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {downloadCopyId, downloadCopyReference, downloadedBundle} from '../../../packages/client-core/src/downloads-service.ts';
import {keepDownloadsInStep, readDownloadedItem, removeDownloadedTitle, reportDownloadPlay, type DownloadHoldings} from '../../../packages/client-core/src/device-downloads.ts';
import {callServer} from '../../../packages/client-core/src/query/server-api.ts';
import type {DownloadTitle} from '../../../packages/client-core/src/downloads.ts';
import {repeat} from '../../../packages/client-core/src/timer.ts';
import type {SavedDownloadsBinder} from '../../../web/src/screens/saved/native-binding.ts';
import type {SavedDownloadsPort, SavedDownloadsSnapshot, SavedLocalDownload} from '../../../web/src/screens/saved/downloads-port.ts';
import type {Invoke} from './mpv-player.ts';
import type {OfflineDownloadsPort} from '../../../web/src/shell/offline-downloads.ts';

type Entry = Readonly<{serverId: string; profileId: string; downloadId: string; itemId: string; quality: string; title: DownloadTitle;
  files: readonly Readonly<{name: string; bytes: number; done: number}>[]; state: 'downloading' | 'paused' | 'failed' | 'ready'; received: boolean; addedMs: number}>;
type Listing = Readonly<{entries: readonly Entry[]; playableUntilMs: number}>;
type Play = Readonly<{itemId: string; playId: string; positionMs: number; playedMs: number; madeAtMs: number}>;

const unsupported = () => Promise.reject(new Error('unsupported'));
/** The bound viewers' syncs, woken when the title page has a download ready. */
const wakers = new Set<() => void>();
export const downloadReady = () => { for (const wake of wakers) wake(); };

/**
 * This computer's downloads for the profile signed in now (downloads rule 3): kept in the app's
 * data folder (src-tauri/src/downloads.rs), fetched and removed as the server's sync says
 * (`keepDownloadsInStep`), played in mpv with or without the server. The server's own list of
 * requests stays the web's (`DownloadsService`).
 */
export function desktopDownloads(invoke: Invoke): SavedDownloadsBinder {
  return async ({runtime, api, isCurrent, signal}) => {
    const {serverId, profileId} = runtime.scope, key = (e: Pick<Entry, 'downloadId' | 'itemId'>) => ({serverId, profileId, downloadId: e.downloadId, itemId: e.itemId});
    const listeners = new Set<() => void>(), life = new AbortController(), all = AbortSignal.any([signal, life.signal]);
    let snapshot: SavedDownloadsSnapshot = {available: true, phase: 'loading', items: []}, listing: Listing = {entries: [], playableUntilMs: 0};
    const publish = () => { for (const listener of [...listeners]) listener(); };
    const media = api as HttpLocalApi & {mediaUrl(path: string): string};

    const fetchTitle = async (downloadId: string, itemId: string) => {
      const downloaded = await readDownloadedItem(media, downloadId, itemId, all);
      const title = downloadedBundle(downloadCopyId(downloadId, itemId), downloaded).title;
      // Play starts where the person was when it was downloaded (then where the last play here left it).
      await invoke('downloads_fetch', {ask: {serverId, profileId, downloadId, itemId, quality: downloaded.quality, title,
        positionMs: Math.max(0, Math.floor(downloaded.item.state?.positionMs ?? 0)),
        files: downloaded.files.map(file => ({url: file.url, bytes: file.sizeBytes, container: file.container ?? ''}))}});
    };
    const holdings: DownloadHoldings = {
      held: async () => listing.entries.map(e => ({downloadId: e.downloadId, itemId: e.itemId})),
      remove: title => invoke('downloads_change', {key: key(title), action: 'remove'}),
      fetch: title => fetchTitle(title.downloadId, title.itemId),
      renew: permission => invoke('downloads_permission', {serverId, profileId, untilMs: permission.receivedAt + permission.playableForMs}),
    };
    const row = (e: Entry): SavedLocalDownload => {
      const bytes = e.files.reduce((sum, f) => sum + f.bytes, 0), bytesDone = e.files.reduce((sum, f) => sum + f.done, 0), id = downloadCopyId(e.downloadId, e.itemId);
      return Object.freeze({id, revision: 0, preparationId: id, itemId: e.itemId, quality: e.quality, title: e.title,
        state: e.state, held: e.state === 'paused', bytes, bytesDone, percent: bytes ? 100 * bytesDone / bytes : null,
        offlineAccess: listing.playableUntilMs > Date.now() ? 'valid' : 'needsCheckIn', onServer: e.title.onServer});
    };
    const read = async () => {
      listing = await invoke<Listing>('downloads_list', {serverId, profileId});
      // The server is told once each title's whole file is here, so a copy it made can go.
      for (const e of listing.entries.filter(e => e.state === 'ready' && !e.received)) {
        await callServer(api, 'POST /v1/downloads/{id}/items/{itemId}/received', {id: e.downloadId, ids: {itemId: e.itemId}, signal: all})
          .then(() => invoke('downloads_change', {key: key(e), action: 'received'})).catch(() => {});
      }
      snapshot = Object.freeze({available: true, phase: 'ready', items: Object.freeze(listing.entries.map(row))});
      publish();
    };
    const step = async () => {
      await read();
      await keepDownloadsInStep(api, holdings, Date.now, all);
      // Plays of downloads, made with or without the server, each sent once by its play id.
      const plays = await invoke<readonly Play[]>('downloads_plays', {serverId, profileId}), sent: string[] = [];
      for (const play of plays) {
        await reportDownloadPlay(api, {itemId: play.itemId, playId: play.playId, positionMs: play.positionMs, playedMs: play.playedMs,
          madeAgoMs: Math.max(0, Date.now() - play.madeAtMs)}, all).then(() => sent.push(play.playId), () => {});
      }
      if (sent.length) await invoke('downloads_sent', {playIds: sent});
      await read();
    };
    // In step at once and every minute while bound (the server says what to fetch, remove and
    // keep playable); progress read once a second while Downloads is shown.
    const stepping = repeat(() => step().catch(() => { snapshot = Object.freeze({...snapshot, phase: 'error', error: 'read'}); publish(); }),
      {everyMs: 60_000, backoff: {baseMs: 60_000, maxMs: 60_000, jitter: 'none'}, signal: all});
    const wake = () => stepping.runNow();
    wakers.add(wake);
    const following = repeat(() => listeners.size ? read() : Promise.resolve(), {everyMs: 1000, immediate: false, backoff: {baseMs: 1000, maxMs: 10000, jitter: 'none'}, signal: all});

    /** Plays a downloaded title in mpv; where it got to is sent at the next sync. */
    const play = async (id: string) => {
      const {downloadId, itemId} = downloadCopyReference(id);
      await invoke('downloads_play', {key: {serverId, profileId, downloadId, itemId}, playId: crypto.randomUUID()});
    };
    const change = async (id: string, action: 'pause' | 'resume' | 'retry' | 'remove') => {
      const {downloadId, itemId} = downloadCopyReference(id);
      if (action === 'pause') await invoke('downloads_change', {key: {serverId, profileId, downloadId, itemId}, action: 'pause'});
      else if (action === 'remove') {
        // Taken out on the server too, so its next sync doesn't bring it back.
        await removeDownloadedTitle(api, downloadId, itemId, all).catch(() => {});
        await invoke('downloads_change', {key: {serverId, profileId, downloadId, itemId}, action: 'remove'});
      } else await fetchTitle(downloadId, itemId);
      await read();
    };
    const port: SavedDownloadsPort = {
      scope: runtime.scope,
      subscribe: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; },
      getSnapshot: () => isCurrent() ? snapshot : {available: false, phase: 'loading', items: []},
      refresh: () => read(),
      loadMore: () => Promise.resolve(),
      action: (id, _revision, action) => change(id, action),
      play: id => play(id),
      getArtwork: () => Promise.resolve(null),
      actionGroup: unsupported,
      setRules: unsupported,
      chooseFolder: unsupported,
      openFolder: unsupported,
      previewOtherProfiles: unsupported,
      removeOtherProfiles: unsupported,
      dispose: () => { wakers.delete(wake); following.stop(); stepping.stop(); life.abort(); listeners.clear(); },
    };
    return Object.freeze({localDownloads: port, saveReadyPreparation: () => step(), dispose: port.dispose});
  };
}

/**
 * The launch page's list when the server can't be reached (`shell/offline-downloads.ts`): the
 * downloads of the profile last signed in on this computer, the ones still playable offline,
 * played in mpv. Another profile's downloads never show here.
 */
export function offlineDownloads(invoke: Invoke, serverId: string, profileId: string): OfflineDownloadsPort {
  const listeners = new Set<() => void>();
  let snapshot: ReturnType<OfflineDownloadsPort['snapshot']> = {phase: 'loading', items: []};
  const rows = new Map<string, Entry>();
  void invoke<Listing>('downloads_list', {serverId, profileId}).then(listing => {
    const playable = listing.playableUntilMs > Date.now();
    for (const e of listing.entries.filter(e => playable && e.state === 'ready')) rows.set(downloadCopyId(e.downloadId, e.itemId), e);
    snapshot = {phase: rows.size ? 'ready' : 'unavailable', items: [...rows].map(([id, e]) => Object.freeze({id, itemId: e.itemId, revision: 0, quality: e.quality,
      bytes: e.files.reduce((sum, f) => sum + f.bytes, 0), title: e.title.title, artist: e.title.artist, kind: e.title.kind, onServer: e.title.onServer}))};
    for (const listener of [...listeners]) listener();
  }, () => { snapshot = {phase: 'unavailable', items: []}; for (const listener of [...listeners]) listener(); });
  return Object.freeze({
    subscribe: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    snapshot: () => snapshot,
    openFolder: () => invoke<void>('downloads_open_folder'),
    play: async (id: string) => {
      const e = rows.get(id);
      if (!e) throw new Error('download_not_ready');
      await invoke('downloads_play', {key: {serverId, profileId, downloadId: e.downloadId, itemId: e.itemId}, playId: crypto.randomUUID()});
    },
  });
}
