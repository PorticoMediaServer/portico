import type {BrowseCapabilities, BrowseFieldCapability, BrowseNode, BrowseQuickFilter, BrowseSortCapability, BrowseSortSelection} from '../../browse.ts';
import {part, type FactPart, type KitMessageId, type TextEnvironment} from './text.ts';
import {orderToolbarPredicates} from '../browse-toolbar.ts';

export type BrowseToolbarEnvironment = TextEnvironment & Readonly<{
  owner?: boolean;
  /** False when typed-only filters cannot be entered, as on TV. */
  textEntry?: boolean;
  /** Only a genuine group page may offer a published requiresGroup sort. */
  scope?: 'library' | 'group';
}>;
export type BrowseQuickModel = BrowseQuickFilter & Readonly<{
  /** Unknown presets keep their server labelKey for the capability-name resolver. */
  label?: FactPart;
  active: boolean;
}>;
export type BrowseFilterGroup = Readonly<{id: 'title' | 'activity' | 'file'; fields: readonly BrowseFieldCapability[]}>;
export type BrowseToolbarModel = Readonly<{
  quick: readonly BrowseQuickModel[];
  /** The other real presets remain available within Filters. */
  filterQuick: readonly BrowseQuickModel[];
  groups: readonly BrowseFilterGroup[];
  sorts: readonly BrowseSortCapability[];
  sort: BrowseSortSelection & Readonly<{flippable: boolean; reshufflable: boolean}>;
  /** Compound/negative predicates are retained whole, never flattened into equality. */
  applied: readonly BrowseNode[];
}>;

// desktop-browse#3 (:402); File size is the owner-only technical sort before Random.
const sortOrder = ['forYou', 'title', 'added', 'year', 'releaseDate', 'duration', 'communityRating', 'criticRating', 'personalRating', 'lastPlayed', 'playCount', 'resolution', 'fileSize', 'random'] as const;
// desktop-browse#1/#3 versus desktop-listen#4; music My List stays inside Filters.
const videoQuick = ['unwatched', 'in-progress', 'favorites', 'watchlist'];
const musicQuick = ['unwatched', 'watched', 'favorites'];
const quickCopy = {
  'in-progress': 'next.kit.browse.inProgress', unwatched: 'next.kit.browse.unwatched', watched: 'next.kit.browse.watched',
  favorites: 'next.kit.browse.favorites', watchlist: 'next.kit.browse.myList', available: 'next.kit.browse.available', missing: 'next.kit.browse.unavailable',
} as const satisfies Record<string, KitMessageId>;
const fieldOrder = ['match', 'genre', 'year', 'contentRating', 'studio', 'network', 'collection', 'series', 'author', 'narrator', 'artist', 'album', 'actor', 'director', 'writer', 'tag', 'label', 'communityRating', 'criticRating', 'releaseDate',
  'playState', 'favorite', 'watchlisted', 'personalRating', 'lastPlayedAt',
  'resolution', 'dynamicRange', 'audioLanguage', 'durationMs', 'dateAdded', 'availability'];
const activity = new Set(['playState', 'favorite', 'watchlisted', 'personalRating', 'lastPlayedAt']);
const file = new Set(['resolution', 'dynamicRange', 'audioLanguage', 'subtitleLanguage', 'versions', 'durationMs', 'dateAdded', 'availability']);
const internal = new Set(['title', 'entityKind', 'memberOf', 'person', 'cast', 'crew', 'homeRow', 'list', 'savedCollection']);
const rank = (order: readonly string[], id: string): number => {const index = order.indexOf(id); return index < 0 ? order.length : index;};

/** Only fields whose sole usable control is text require text entry. Facets and ranges do not. */
function typedOnly(field: BrowseFieldCapability): boolean {
  return field.id !== 'year' && !field.facetSource && field.controlHint !== 'facet-multi-select' && field.type !== 'boolean' && field.controlHint !== 'toggle'
    && !(field.type === 'enum' && field.allowedValues) && field.type !== 'number' && field.type !== 'date' && field.controlHint !== 'number-range' && field.controlHint !== 'date-range';
}

function sameNode(a: BrowseNode, b: BrowseNode): boolean {
  if ('field' in a) {
    if (!('field' in b) || a.field !== b.field || a.operator !== b.operator) return false;
    const value = b.value;
    return Array.isArray(a.value) ? Array.isArray(value) && a.value.length === value.length && a.value.every((v, i) => v === value[i]) : a.value === value;
  }
  if ('not' in a) return 'not' in b && sameNode(a.not, b.not);
  if ('all' in a) return 'all' in b && a.all.length === b.all.length && a.all.every((n, i) => sameNode(n, b.all[i]!));
  return 'any' in b && a.any.length === b.any.length && a.any.every((n, i) => sameNode(n, b.any[i]!));
}

/** Validate offered quick presets against the resolved vocabulary, without generating a query. */
function offeredNode(node: BrowseNode, fields: readonly BrowseFieldCapability[]): boolean {
  if ('field' in node) {
    const field = fields.find(f => f.id === node.field);
    if (!field?.operators.includes(node.operator)) return false;
    const values = Array.isArray(node.value) ? node.value : [node.value];
    return !field.allowedValues || values.every(v => v === null || typeof v === 'string' && field.allowedValues!.includes(v));
  }
  if ('not' in node) return offeredNode(node.not, fields);
  return ('all' in node ? node.all : node.any).every(n => offeredNode(n, fields));
}

/** One presentation policy for every new browse renderer. No old toolbar/formatter output is used.
 * Capabilities must describe the current resolved browse. Field/sort identities, operators,
 * directions, flags and queries remain the server's; unknown vocabulary retains server order.
 * This model never selects a replacement sort, computes results or infers missing capabilities.
 */
export function browseToolbarModel(capabilities: BrowseCapabilities, libraryKind: string, predicates: readonly BrowseNode[], sort: BrowseSortSelection, env: BrowseToolbarEnvironment): BrowseToolbarModel {
  const pivot = capabilities.resolvedPivot;
  // Match is a public browse capability, not an owner repair operation. Local-only
  // libraries omit it at the server; members use the same offered filter.
  const fields = capabilities.fields.filter(f => (!pivot?.filters || pivot.filters.includes(f.id)) && (env.textEntry !== false || !typedOnly(f)));
  const visible = fields.filter(f => !internal.has(f.id) && !(f.id === 'decade' && fields.some(f => f.id === 'year')));
  const groups = (['title', 'activity', 'file'] as const).map(id => Object.freeze({id, fields: Object.freeze(visible.filter(f => (activity.has(f.id) ? 'activity' : file.has(f.id) ? 'file' : 'title') === id)
    .sort((a, b) => rank(fieldOrder, a.id) - rank(fieldOrder, b.id)))})).filter(group => group.fields.length > 0);
  const order = libraryKind === 'music' ? musicQuick : videoQuick;
  const offered = capabilities.quickFilters.filter(q => offeredNode(q.query, fields));
  const quick = order.flatMap(id => {
    const q = offered.find(q => q.id === id);
    return q ? [q] : [];
  });
  const modelQuick = (q: BrowseQuickFilter): BrowseQuickModel => {
    const key = Object.hasOwn(quickCopy, q.id) ? quickCopy[q.id as keyof typeof quickCopy] : undefined;
    const label = libraryKind === 'music' && q.id === 'unwatched' ? 'next.kit.browse.unplayed' : libraryKind === 'music' && q.id === 'watched' ? 'next.kit.browse.played' : key;
    return Object.freeze({...q, ...(label ? {label: part('label', env.t(label), 'context')} : {}), active: predicates.some(p => sameNode(p, q.query))});
  };
  const sorts = capabilities.sorts.filter(s => (!pivot?.sorts || pivot.sorts.includes(s.id)) && (env.owner === true || s.id !== 'fileSize') && (env.scope === 'group' || !s.requiresGroup))
    .sort((a, b) => (a.requiresGroup ? -1 : rank(sortOrder, a.id)) - (b.requiresGroup ? -1 : rank(sortOrder, b.id)));
  const selected = sorts.find(s => s.id === sort.field);
  const validDirection = selected?.directions.includes(sort.direction) === true;
  const reshufflable = validDirection && !!selected && (selected.seeded === true || selected.id === 'random');
  return Object.freeze({quick: Object.freeze(quick.map(modelQuick)), filterQuick: Object.freeze(offered.filter(q => !quick.includes(q)).map(modelQuick)), groups: Object.freeze(groups), sorts: Object.freeze(sorts),
    sort: Object.freeze({...sort, flippable: validDirection && !reshufflable && !!selected && selected.directions.length > 1, reshufflable}),
    applied: Object.freeze(orderToolbarPredicates(predicates.filter(p => !quick.some(q => sameNode(p, q.query))))),
  });
}
