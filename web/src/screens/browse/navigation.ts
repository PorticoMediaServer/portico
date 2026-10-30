import type {ContentEntry} from '@core/library-content.ts';
import type {SearchEntry} from '@core/search.ts';
import {entryDestination, type HomeDestination} from '../../shell/navigation';
import type {LibraryScreenSearch} from './route-search';
import type {LiveScreenSearch} from '../live/route-search';

type ScreenDestination = HomeDestination | {to: '/person/$personId'; params: {personId: string}}
  | {to: '/live'; search: LiveScreenSearch}
  | {to: '/library/$libraryId'; params: {libraryId: string}; search: LibraryScreenSearch};
const content = (entry: SearchEntry): entry is ContentEntry => entry.kind !== 'person' && entry.kind !== 'channel';

/** Only the canonical readers' actual destination facts cross this boundary. */
export function screenEntryDestination(entry: SearchEntry): ScreenDestination {
  if (content(entry)) {
    const target = entryDestination(entry);
    return target.to === '/library/$libraryId' && entry.navigation?.category
      ? {...target, search: {...target.search, category: entry.navigation.category}} : target;
  }
  if (entry.kind === 'person') return {to: '/person/$personId', params: {personId: entry.navigation.entityId}};
  const {entityId, sourceId, channelId, provenance} = entry.navigation;
  return {to: '/live', search: sourceId && channelId && provenance
    ? {sourceId, channelId, kind: provenance} : {channelId: entityId}};
}
