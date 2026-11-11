import type {Item} from '../../../contracts/src/next/api.ts';
import type {CardSubject, PlaybackSubject} from '../presentation/kit-model/facts.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';

/** What the player needs to know about the title it plays, from `GET /v1/items/{id}`. */
export type PlaybackItem = Readonly<{
  id: string; libraryId: string; title: string; available: boolean;
  posterUrl?: string; backdropUrl?: string;
  /** What the player screen and the dock name. */
  subject: PlaybackSubject;
  /** An episode's show and season. */
  showId?: string; seasonId?: string;
}>;

/** The kinds a player plays, and the card kind each is drawn as. */
const playable: Readonly<Record<string, Readonly<{subject: PlaybackSubject['kind']; card: CardSubject['kind']}>>> = {
  movie: {subject: 'video', card: 'movie'}, episode: {subject: 'video', card: 'episode'}, extra: {subject: 'video', card: 'extra'},
  track: {subject: 'music', card: 'song'}, audiobook: {subject: 'audiobook', card: 'audiobook_file'},
};

/** The player's facts about one title; a title of a kind no player plays is refused. */
export function playbackItem(item: Item): PlaybackItem {
  const kind = playable[item.kind];
  if (!kind || !item.id || !item.title) throw new Error('invalid_playback_item');
  const episode = item.kind === 'episode', song = item.kind === 'track';
  const card: CardSubject = {id: item.id, kind: kind.card, title: item.title, ...(item.year ? {year: item.year} : {}),
    ...(song && item.parent ? {album: item.parent.title} : {}), ...(song && item.root ? {artist: item.root.title} : {}),
    ...(episode && item.root ? {show: {id: item.root.id, title: item.root.title}} : {}),
    ...(episode && item.index !== undefined ? {episode: item.parentIndex === 0 ? {mode: 'special' as const} : {mode: 'seasonal' as const, season: item.parentIndex, episode: item.index}} : {})};
  const poster = item.images?.poster ?? item.images?.cover ?? item.images?.thumb;
  return Object.freeze({id: item.id, libraryId: item.libraryId, title: item.title, available: item.available !== false,
    ...(poster ? {posterUrl: poster} : {}), ...(item.images?.backdrop ? {backdropUrl: item.images.backdrop} : {}),
    subject: Object.freeze({kind: kind.subject, item: Object.freeze(card)}) as PlaybackSubject,
    ...(episode && item.root ? {showId: item.root.id} : {}), ...(episode && item.parent ? {seasonId: item.parent.id} : {})});
}

/** Reads one title for the player. */
export async function readPlaybackItem(api: ServerRequester, id: string, signal?: AbortSignal): Promise<PlaybackItem> {
  return playbackItem(await callServer(api, 'GET /v1/items/{id}', {id, signal}));
}
