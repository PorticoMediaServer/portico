/**
 * Downloads, for every app: what a profile asked to have on this device, and keeping the device
 * in step with the server at every contact.
 *
 * A request (`POST /v1/downloads`) is a title, or a show, season, album, artist or playlist with
 * a selection; the server works out its titles. What the device has actually fetched is known to
 * the device alone: the app keeps its files and tells the server what it holds at each contact
 * (`POST /v1/downloads/sync`), which answers what to remove, what to fetch, and for how long
 * everything plays without reaching it again. Asking for a download and removing one go through
 * the outbox, so a wish made out of reach is sent later like any other tap, with an
 * Idempotency-Key. Plays of a download, made with or without the server, go through the state
 * batch with a play id kept for that playing, so each is counted once and uses none of the
 * account's streams. Edits made offline (watched, favourite, rating) go through the outbox as
 * they always have, with how long ago they were made.
 */
import type {Download, DownloadAsk, DownloadChange, DownloadedItem, DownloadOptions, DownloadUsage, HeldRemoval, HeldTitle,
  OfflinePlay, StateBatchReply} from '../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';

/** This device's requests, or with `everyDevice` all of the profile's (a browser's Downloads page). */
export const listDownloads = (api: ServerRequester, everyDevice = false, signal?: AbortSignal): Promise<Download[]> =>
  callServer(api, 'GET /v1/downloads', {query: {scope: everyDevice ? 'profile' : undefined}, signal}).then(page => page.items);
/** One request with each title and where it stands. */
export const readDownload = (api: ServerRequester, id: string, signal?: AbortSignal): Promise<Download> =>
  callServer(api, 'GET /v1/downloads/{id}', {id, signal});
/** The choices for downloading a title, or a holder with a selection, with the size of each. */
export const readDownloadOptions = (api: ServerRequester, itemId: string,
  choice: Readonly<{selection?: 'all' | 'unwatched' | 'next'; keepNext?: number; versionId?: string}> = {}, signal?: AbortSignal): Promise<DownloadOptions> =>
  callServer(api, 'GET /v1/items/{id}/download-options', {id: itemId, query: choice, signal});
/** What the server's copies for this profile's requests take, and the room left. */
export const readDownloadUsage = (api: ServerRequester, signal?: AbortSignal): Promise<DownloadUsage> =>
  callServer(api, 'GET /v1/downloads/usage', {signal});
/** What to fetch for one title: its details, the addresses and sizes of its files, subtitles, chapters, markers and lyrics. */
export const readDownloadedItem = (api: ServerRequester, downloadId: string, itemId: string, signal?: AbortSignal): Promise<DownloadedItem> =>
  callServer(api, 'GET /v1/downloads/{id}/items/{itemId}', {id: downloadId, ids: {itemId}, signal});
/** Asks for a download. Out of reach, the outbox keeps it and the call rejects with `OutboxWaiting`. */
export const askForDownload = (api: ServerRequester, ask: DownloadAsk & {operationId?: string}, signal?: AbortSignal): Promise<Download> =>
  callServer(api, 'POST /v1/downloads', {body: ask, signal});
/** Pause, resume, retry, or change "keep the next" (0 stops it and keeps what is there). Needs the server. */
export const changeDownload = (api: ServerRequester, id: string, change: DownloadChange, signal?: AbortSignal): Promise<Download> =>
  callServer(api, 'PATCH /v1/downloads/{id}', {id, body: change, signal});
/** Removes a request. The app deletes its files at once; out of reach, the outbox tells the server later. */
export const removeDownload = (api: ServerRequester, id: string, signal?: AbortSignal): Promise<void> =>
  callServer(api, 'DELETE /v1/downloads/{id}', {id, signal}).then(() => undefined);

/** Removes one held title without cancelling the rest of a container. */
export const removeDownloadedTitle = (api: ServerRequester, downloadId: string, itemId: string, signal?: AbortSignal): Promise<void> =>
  callServer(api, 'DELETE /v1/downloads/{id}/items/{itemId}', {id: downloadId, ids: {itemId}, signal});

/** A title this device holds for the profile signed in now: fetched, or being fetched. */
export type HeldDownload = Readonly<{downloadId: string; itemId: string}>;

/**
 * How long downloads play without reaching the server, as the server gave it at a contact:
 * `playableForMs` from `receivedAt`, this device's wall clock when the answer arrived.
 * `playableUntil` is the same moment by the server's clock, for showing.
 */
export type OfflinePermission = Readonly<{receivedAt: number; playableForMs: number; playableUntil: string}>;

/** What the app does with its own files and records. Each is the app's to make safe to repeat. */
export interface DownloadHoldings {
  /** What this device holds for the profile signed in now, on this server. */
  held(): Promise<readonly HeldDownload[]>;
  /** Deletes a title's files and record: the profile may no longer see it (`no_access`), downloads were turned off for
   * it (`downloads_off`), or under "keep the next" it was watched (`watched`) or passed over (`not_next`). */
  remove(title: HeldRemoval): Promise<void>;
  /** Starts fetching a title, in the background, carrying on by range after a lost connection. */
  fetch(title: HeldTitle): Promise<void>;
  /** Keeps the permission given at this contact, for every download of this profile here. */
  renew(permission: OfflinePermission): Promise<void>;
}

/** What one contact did: removals and fetches that could not be done are tried again at the next one. */
export type DownloadsInStep = Readonly<{removed: readonly HeldRemoval[]; fetched: readonly HeldTitle[]; permission: OfflinePermission;
  failed: readonly unknown[]}>;

/**
 * Brings this device in step with the server: call it at every contact (the app opening, coming
 * to the front, the network coming back). It says what the device holds; every title the server
 * names is removed, every one it offers is fetched, and the permission to play offline is renewed
 * for everything, without the person doing anything. A device the server cannot be reached from
 * changes nothing: its downloads keep playing until the permission it has runs out.
 */
export async function keepDownloadsInStep(api: ServerRequester, holdings: DownloadHoldings, now: () => number = Date.now,
  signal?: AbortSignal): Promise<DownloadsInStep> {
  const held = await holdings.held();
  const reply = await callServer(api, 'POST /v1/downloads/sync', {body: {holds: held.map(h => ({downloadId: h.downloadId, itemId: h.itemId}))}, signal});
  const permission = {receivedAt: now(), playableForMs: reply.playableForMs, playableUntil: reply.playableUntil};
  const failed: unknown[] = [], removed: HeldRemoval[] = [], fetched: HeldTitle[] = [];
  for (const title of reply.remove) await holdings.remove(title).then(() => { removed.push(title); }, error => { failed.push(error); });
  for (const title of reply.fetch) await holdings.fetch(title).then(() => { fetched.push(title); }, error => { failed.push(error); });
  await holdings.renew(permission);
  return {removed, fetched, permission, failed};
}

/**
 * Says where a playing of a download got to: when it stops, ends, or the app leaves the front.
 * Sent through the state batch, with or without the server: out of reach the outbox keeps it and
 * adds how long ago it was. The same play id sent again changes nothing; past the profile's
 * watched point the title is marked watched and the play counted once.
 */
export const reportDownloadPlay = (api: ServerRequester, play: Omit<OfflinePlay, 'madeAgoMs'> & {madeAgoMs?: number}, signal?: AbortSignal): Promise<StateBatchReply> =>
  callServer(api, 'POST /v1/items/state:batch', {body: {items: [], plays: [{...play, madeAgoMs: play.madeAgoMs ?? 0}]}, signal});

/** Sidecar SubRip timing is WebVTT timing with a comma. Cue numbers remain valid cue ids. */
export function downloadedSubtitleText(format: string, text: string): string {
  if (format !== 'srt' || text.replace(/^\uFEFF/, '').startsWith('WEBVTT')) return text;
  return 'WEBVTT\n\n' + text.replace(/^\uFEFF/, '').replace(/(\d{2}:\d{2}:\d{2}),(\d{3})/g, '$1.$2');
}
