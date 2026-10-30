import type {ContentEntry} from '@core/library-content.ts';
import type {QueuePlaybackView} from '@core/queue-controller.ts';
import type {ShowWorkspaceData} from '@core/show-workspace.ts';

/**
 * desktop-player f6: the drawer lists the season the title page has open, from that page's own
 * workspace read. Rows carry only catalogue facts; the playing row's clock is the live session's,
 * and every other row keeps the catalogue's saved progress. A row without a real playback target
 * is not drawn, and "Up next" comes from the queue's own next entry, never from adjacency.
 */
export type WatchEpisodeSeason = Readonly<{id: string | null; number: number; title: string}>;
export type WatchEpisodeRow = Readonly<{
  itemId: string;
  title: string;
  number?: number;
  stillUrl?: string;
  overview?: string;
  airDate?: string;
  durationSeconds?: number;
  progressSeconds?: number;
  watched: boolean;
  playing: boolean;
  next: boolean;
}>;
export type WatchEpisodes = Readonly<{
  seasons: readonly WatchEpisodeSeason[];
  seasonId: string | null;
  rows: readonly WatchEpisodeRow[];
}>;
export type WatchEpisodesInput = Readonly<{
  data: ShowWorkspaceData;
  queue: QueuePlaybackView | null;
  playingItemId?: string;
  positionSeconds?: number;
  durationSeconds?: number;
}>;

const episodeEntries = (data: ShowWorkspaceData): readonly ContentEntry[] =>
  data.episodes.sections.flatMap(section => section.entries)
    .filter(entry => entry.kind === 'episode' && entry.missing !== true && entry.available !== false && !!(entry.playback?.itemId ?? entry.id));

export function watchEpisodes(input: WatchEpisodesInput): WatchEpisodes {
  const {data, queue, playingItemId} = input;
  const current = queue?.items.find(item => item.entryId === queue.queue.currentEntryId);
  const playing = playingItemId ?? current?.itemId;
  const nextEntry = queue?.next.available ? queue.items.find(item => item.entryId === queue.next.entryId) : undefined;
  const next = nextEntry?.itemId;
  const rows: WatchEpisodeRow[] = [];
  for (const entry of episodeEntries(data)) {
    const itemId = entry.playback?.itemId ?? entry.id;
    if (rows.some(row => row.itemId === itemId)) continue;
    const isPlaying = itemId === playing;
    const duration = isPlaying && input.durationSeconds !== undefined ? input.durationSeconds : entry.duration;
    const progress = isPlaying ? input.positionSeconds : entry.progressSeconds;
    rows.push(Object.freeze({
      itemId,
      title: entry.title,
      ...(entry.episodeNumber !== undefined ? {number: entry.episodeNumber} : {}),
      ...(entry.stillUrl ? {stillUrl: entry.stillUrl} : {}),
      ...(entry.overview ? {overview: entry.overview} : {}),
      ...(entry.airDate ? {airDate: entry.airDate} : {}),
      ...(duration !== undefined && Number.isFinite(duration) && duration > 0 ? {durationSeconds: duration} : {}),
      ...(progress !== undefined && Number.isFinite(progress) && progress > 0 ? {progressSeconds: progress} : {}),
      watched: entry.watched === true,
      playing: isPlaying,
      next: !isPlaying && !!next && itemId === next,
    }));
  }
  return Object.freeze({
    seasons: Object.freeze(data.seasons.map(season => Object.freeze({id: season.id, number: season.number, title: season.title}))),
    seasonId: data.selected.seasonId,
    rows: Object.freeze(rows),
  });
}
