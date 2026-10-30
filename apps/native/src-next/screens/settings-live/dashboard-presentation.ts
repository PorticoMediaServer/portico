import type {AdminSession} from '../../../../../packages/client-core/src/playback-v1/types';
import {playbackIdentity, type EpisodeNumbering, type TextEnvironment} from '../../../../../packages/client-core/src/presentation/kit-model';

/** Display only the authorized session's supplied item facts; never fetch or infer a title. */
export function nativeDashboardIdentity(session: AdminSession, text: TextEnvironment): Readonly<{title: string; secondary: readonly string[]}> {
  const item = session.item;
  if (!item) return {title: session.title, secondary: []};
  if (item.kind === 'episode') {
    let episode: EpisodeNumbering | undefined;
    if (item.episodeNumbering === 'date') episode = {mode: 'date', day: item.airDate};
    else if (item.episodeNumbering === 'absolute') episode = {mode: 'absolute', number: item.episodeNumber};
    else if (item.episodeNumbering === 'special') episode = {mode: 'special'};
    else if (item.episodeNumbering === undefined || item.episodeNumbering === 'seasonal') episode = {mode: 'seasonal', season: item.seasonNumber, episode: item.episodeNumber};
    const identity = playbackIdentity({kind: 'video', item: {id: item.id, kind: 'episode', title: item.title, ...(item.showTitle ? {show: {id: item.showId, title: item.showTitle}} : {}), episode}}, 'full', text);
    return {title: identity.title, secondary: identity.subtitle.map(part => part.text)};
  }
  return {title: item.title, secondary: item.kind === 'song' ? [item.artist, item.album].filter((value): value is string => !!value) : item.kind === 'chapter' || item.kind === 'book' ? [item.author].filter((value): value is string => !!value) : item.year === undefined ? [] : [String(item.year)]};
}
