import type {ContentEntry} from '../../../../packages/client-core/src/library-content';
import type {HomeChannel, HomeCollection} from '../../../../packages/client-core/src/home';
import {authorityKey, type ShellAuthority, type ShellDestination} from './model';

/** Public verification contract. Identity comes from the authority fence and real entities. */
export function tvFocusIds(authority: ShellAuthority) {
  const prefix = `tv.${authorityKey(authority)}`;
  const spotlight = `${prefix}.home.spotlight`, customize = `${prefix}.home.customize`, rail = `${prefix}.rail`;
  return {
    spotlight, customize, rail,
    row: (rowId: string) => `${prefix}.home.row.${JSON.stringify(rowId)}`,
    entry: (entry: Pick<ContentEntry, 'kind' | 'libraryId' | 'id'>) => JSON.stringify([entry.kind, entry.libraryId, entry.id]),
    channel: (channel: HomeChannel) => JSON.stringify(['channel', channel.channel.sourceId, channel.channel.id]),
    collection: (collection: HomeCollection) => JSON.stringify(['collection', collection.collection.libraryId, collection.collection.id]),
    destination: tvRailDestinationId,
    profile: `profile:${authority.profileId}`,
    profileMenu: `${prefix}.rail.profile-menu`,
    menu: (windowId: string, key: string) => `${prefix}.home.menu.${JSON.stringify([windowId, key])}`,
    page: (rowId: string, cursor: string) => JSON.stringify(['page', rowId, cursor]),
    surface: (kind: string) => `${prefix}.home.${kind}`,
    start: {regionId: spotlight, childId: 'play'},
    last: {regionId: customize, childId: 'customize'},
  };
}

/** Declarative region boundaries only. Native focus engines choose every move inside. */
/** `spotlightFocusable: false` (a hero with no buttons) makes the first row's top edge a stop: Up has nowhere to go,
 * tv-focus F-HOME-3 "top of the page; nothing moves", instead of falling to the rail. */
export function tvHomeRegionMap(authority: ShellAuthority, rowIds: readonly string[], options: Readonly<{spotlightFocusable?: boolean}> = {}) {
  const ids = tvFocusIds(authority), regions = rowIds.map(ids.row);
  const region = (id: string, previous?: string, next?: string): {id: string; neighbors: {previous?: string; next?: string}; screenStart: typeof ids.start;
    edges: {left: string; up?: string; down?: string}; stops?: Readonly<{up?: boolean}>} => ({id, neighbors: {previous, next}, screenStart: ids.start,
    edges: {left: ids.rail, up: previous, down: next}});
  return {
    spotlight: {...region(ids.spotlight, undefined, regions[0] ?? ids.customize), entry: 'play'},
    rows: new Map(rowIds.map((id, index) => {
      const row = region(regions[index], regions[index - 1] ?? ids.spotlight, regions[index + 1] ?? ids.customize);
      return [id, index === 0 && options.spotlightFocusable === false ? {...row, edges: {...row.edges, up: undefined}, stops: {up: true}} : row];
    })),
    customize: {...region(ids.customize, regions.at(-1) ?? ids.spotlight), entry: 'customize'},
  };
}

export function tvRailDestinationId(destination: ShellDestination): string {
  return destination.kind === 'library' ? `library:${destination.libraryId}` : destination.kind;
}

export function sameDestination(a: ShellDestination, b: ShellDestination): boolean {
  return tvRailDestinationId(a) === tvRailDestinationId(b);
}
