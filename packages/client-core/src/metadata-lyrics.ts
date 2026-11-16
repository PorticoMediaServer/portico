import type {Lyrics} from '../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';

/**
 * A song's lyrics on the rebuilt server (definition area 4, rule 64): what everyone sees now, lyrics the owner pastes
 * (plain or timed LRC; pasted lyrics are never replaced by a fetch), and Fetch lyrics, which asks LRCLIB for this song.
 */
export type {Lyrics};
export async function readSongLyrics(api: ServerRequester, songId: string, signal?: AbortSignal): Promise<readonly Lyrics[]> {
  return (await callServer(api, 'GET /v1/items/{id}/lyrics', {id: songId, signal})).items ?? [];
}
export async function pasteSongLyrics(api: ServerRequester, songId: string, text: string, language = '', signal?: AbortSignal): Promise<readonly Lyrics[]> {
  return (await callServer(api, 'PUT /v1/admin/items/{id}/lyrics', {id: songId, body: {text, ...(language.trim() ? {language: language.trim()} : {})}, signal})).items ?? [];
}
/** True when lyrics were found and stored. */
export async function fetchSongLyrics(api: ServerRequester, songId: string, signal?: AbortSignal): Promise<boolean> {
  return (await callServer(api, 'POST /v1/admin/items/{id}/lyrics/fetch', {id: songId, signal})).found;
}
