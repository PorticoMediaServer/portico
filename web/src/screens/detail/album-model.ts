import type {ContentEntry, ContentProjection, ContentRoute, ContentSection} from '@core/library-content.ts';
import type {BrowseSortSelection} from '@core/browse.ts';

type TrackGroup = {key: string; section: ContentSection; discNumber?: number; entries: ContentEntry[]};

/** Keep server sections and order; a disc boundary never moves a loaded song. */
export function albumTrackGroups(sections: readonly ContentSection[]): readonly TrackGroup[] {
  const groups: TrackGroup[] = [];
  for (const section of sections) {
    let group: TrackGroup | undefined;
    for (const entry of section.entries) {
      if (entry.kind !== 'song') continue;
      const discNumber = entry.discNumber !== undefined && entry.discNumber > 0 ? entry.discNumber : undefined;
      if (!group || group.discNumber !== discNumber) {
        group = {key: JSON.stringify([section.id, entry.id]), section, discNumber, entries: []};
        groups.push(group);
      }
      group.entries.push(entry);
    }
  }
  return groups;
}

/** Sort only through a capability actually offered by this album projection. */
export function albumSortRoute(projection: ContentProjection, value: BrowseSortSelection): ContentRoute | undefined {
  const offered = projection.sorts.find(sort => sort.id === value.field);
  if (projection.scope.view !== 'album' || !projection.scope.entityId || !offered?.directions.includes(value.direction)) return undefined;
  return {libraryId: projection.scope.libraryId, view: 'album', entityId: projection.scope.entityId, sort: value.field, direction: value.direction};
}
