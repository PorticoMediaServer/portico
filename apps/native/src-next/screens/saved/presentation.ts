import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {BrowseNode} from '../../../../../packages/client-core/src/browse';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {PlaylistCard, PlaylistOccurrence} from '../../../../../packages/client-core/src/saved';
import type {IconName} from '../../kit/Icon';

const t = defaultI18n.t;

/** Every count on these pages is the server's own; a missing total stays absent. */
export function savedItemCount(count: number | undefined): string | undefined {
  return count === undefined ? undefined : t('web.saved.items', {count});
}

export function savedTitleCount(count: number | undefined): string | undefined {
  return count === undefined ? undefined : t('next.selection.titles', {count});
}

export function savedCollectionCount(count: number | undefined): string | undefined {
  return count === undefined ? undefined : t('lib.count', {count, pivot: 'collections'});
}

export function savedViewCount(count: number | undefined): string | undefined {
  return count === undefined ? undefined : t('next.saved.savedViewsCount', {count});
}

export type SavedVisibilityLine = Readonly<{id: 'server' | 'private' | 'shared'; text: string; icon: IconName; strong: boolean}>;

/** A collection's published policy in the frame's own words. `sharedByOther` is the TV wording
 * for someone else's visible collection; the other clients keep their own line. */
export function savedVisibility(resource: PersonalResource, sharedByOther = false): SavedVisibilityLine {
  if (sharedByOther && resource.visibility === 'server' && resource.role !== 'owner')
    return {id: 'shared', text: t('web.saved.sharedByOwner'), icon: 'i-people', strong: true};
  return resource.visibility === 'server'
    ? {id: 'server', text: t('web.saved.visibleToAll'), icon: 'i-people', strong: true}
    : {id: 'private', text: t('next.selection.onlyYou'), icon: 'i-lock', strong: false};
}

export function savedVisibilityHelp(resource: PersonalResource): string {
  return t(resource.visibility === 'server' ? 'web.saved.sharedHelp' : 'web.saved.privateHelp');
}

export function savedResourceEyebrow(resource: PersonalResource): string {
  if (resource.kind === 'view') return t('web.saved.savedView');
  return resource.role === 'owner' ? t('web.saved.collection') : t('web.saved.sharedCollection');
}

export const savedPlaylistEyebrow = t('web.saved.playlist');

/** Four paths are enough for the drawn 2×2 mosaic; empty art keeps the frame's empty grid. */
export function savedMosaicPaths(paths: readonly (string | undefined)[]): readonly string[] {
  return paths.filter((path): path is string => !!path).slice(0, 4);
}

export function playlistMosaic(card: PlaylistCard): readonly string[] {
  return savedMosaicPaths(card.artworkPaths ?? []);
}

export function playlistEntryMosaic(entries: readonly PlaylistOccurrence[]): readonly string[] {
  return savedMosaicPaths(entries.flatMap(entry => entry.hidden ? [] : [entry.media.posterUrl ?? entry.media.backdropUrl]));
}

export function personalMosaic(resource: PersonalResource | null | undefined): readonly string[] {
  if (!resource) return [];
  return savedMosaicPaths([...(resource.artworkPaths ?? []), ...(resource.samples ?? []).flatMap(entry => [entry.posterUrl ?? entry.backdropUrl])]);
}

export function mediaEntry(entry: PlaylistOccurrence): ContentEntry | undefined {
  return entry.hidden ? undefined : entry.media;
}

/** A total time is shown only when every visible entry states its length. */
export function exactDurationSeconds(entries: readonly ContentEntry[]): number | undefined {
  if (!entries.length) return undefined;
  let seconds = 0;
  for (const entry of entries) {
    if (!entry.duration || !Number.isFinite(entry.duration)) return undefined;
    seconds += entry.duration;
  }
  return seconds > 0 ? seconds : undefined;
}

export function savedDuration(seconds: number | undefined): string | undefined {
  return seconds === undefined ? undefined : defaultI18n.duration(seconds);
}

/** The first real playable item is the only sequential start this shell offers today. */
export function firstPlayable(entries: readonly ContentEntry[]): ContentEntry | undefined {
  return entries.find(entry => !!entry.playback && entry.available !== false && !entry.missing);
}

/** How many conditions the saved query actually applies (a drawn Filters badge). */
export function savedViewConditionCount(resource: PersonalResource): number {
  const walk = (node: BrowseNode | null | undefined): number => {
    if (!node) return 0;
    if ('all' in node) return node.all.reduce((total, child) => total + walk(child), 0);
    if ('any' in node) return node.any.reduce((total, child) => total + walk(child), 0);
    if ('not' in node) return walk(node.not);
    return 1;
  };
  return walk(resource.definition?.query);
}

export function savedLibraryName(libraries: readonly Readonly<{id: string; name: string}>[], libraryId: string | undefined): string | undefined {
  return libraryId ? libraries.find(library => library.id === libraryId)?.name : undefined;
}
