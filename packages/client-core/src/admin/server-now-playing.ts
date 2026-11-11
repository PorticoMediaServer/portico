import type {NowPlaying} from '../../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';
import {parseAdminSession, type AdminSession} from '../playback-v1/types.ts';

/**
 * Now playing on the rebuilt server. `GET /v1/admin/sessions` answers every stream at once, a
 * problem first, then conversions, then the newest start; `DELETE /v1/admin/sessions/{id}` stops
 * one with a message of up to 500 characters. The server offers no pause, resume or Move to for
 * someone else's stream (console definition, "Left out"), so the Dashboard shows none.
 */
export type ServerNowPlaying = NowPlaying;
export type ServerNowPlayingTotals = Readonly<{streams: number; paused: number; converting: number; bandwidthKbps: number; people: number; atHome: number; away: number}>;
export type ServerNowPlayingPage = Readonly<{items: readonly AdminSession[]; totals: ServerNowPlayingTotals}>;

export class ServerNowPlayingError extends Error {
  readonly code = 'invalid_now_playing';
  constructor() { super('invalid_now_playing'); this.name = 'ServerNowPlayingError'; }
}
const object = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, nonempty = true) => typeof v === 'string' && (!nonempty || v.length > 0) && v.length <= 4096;
const whole = (v: unknown) => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;

/** One stream in the card's shape. The person is the account's username, then the profile's name
 * where it differs; the server gives names, not ids, and nothing on the card needs an id. */
export function adminSessionFromServer(value: unknown): AdminSession {
  if (!object(value) || !text(value.id) || !text(value.kind) || !text(value.state) || !whole(value.positionMs) || !text(value.username) ||
      !text(value.deviceName, false) || typeof value.away !== 'boolean' || !text(value.startedAt) || !object(value.delivery)) throw new ServerNowPlayingError();
  return parseAdminSession({
    id: value.id, user: value.username, profile: text(value.profile) ? value.profile : value.username, device: value.deviceName,
    ...(text(value.itemId) ? {itemId: value.itemId} : {}), title: text(value.title) ? value.title : '',
    kind: value.kind, state: value.state, positionMs: value.positionMs,
    ...(whole(value.durationMs) && (value.durationMs as number) > 0 ? {durationMs: value.durationMs} : {}),
    ...(whole(value.bitrateKbps) && (value.bitrateKbps as number) > 0 ? {bitrateKbps: value.bitrateKbps} : {}),
    decision: value.delivery, location: value.away ? 'remote' : 'local', startedAt: value.startedAt,
  });
}

const converting = (s: AdminSession) => s.decision.video?.action === 'transcode' || s.decision.video?.action === 'burn' ||
  s.decision.audio?.action === 'transcode' || s.decision.subtitles?.action === 'burn';

/** The totals of the streams listed: every stream, paused ones included. */
export function nowPlayingTotals(items: readonly AdminSession[]): ServerNowPlayingTotals {
  return Object.freeze({
    streams: items.length, paused: items.filter(s => s.state === 'paused').length, converting: items.filter(converting).length,
    bandwidthKbps: items.reduce((sum, s) => sum + (s.bitrateKbps ?? 0), 0), people: new Set(items.map(s => s.user)).size,
    atHome: items.filter(s => s.location === 'local').length, away: items.filter(s => s.location === 'remote').length,
  });
}

/** Every stream on the server, as the owner and administrators see them. */
export async function readServerNowPlaying(api: ServerRequester, signal?: AbortSignal): Promise<ServerNowPlayingPage> {
  const raw: unknown = await callServer(api, 'GET /v1/admin/sessions', {signal});
  if (!object(raw) || !Array.isArray(raw.items)) throw new ServerNowPlayingError();
  const items = Object.freeze(raw.items.map(adminSessionFromServer));
  if (new Set(items.map(s => s.id)).size !== items.length) throw new ServerNowPlayingError();
  return Object.freeze({items, totals: nowPlayingTotals(items)});
}

/** Stops a stream; the viewer is shown the message. A stream already gone is not an error. */
export async function stopServerStream(api: ServerRequester, id: string, message?: string, signal?: AbortSignal): Promise<void> {
  const text = message?.trim();
  try {
    await callServer(api, 'DELETE /v1/admin/sessions/{id}', {id, body: text ? {message: text.slice(0, 500)} : {}, signal});
  } catch (error) {
    if ((error as {status?: unknown} | null)?.status !== 404) throw error;
  }
}
