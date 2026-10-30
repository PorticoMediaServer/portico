import {parseBrowseNode, type BrowseNode, type BrowseSortSelection} from '@core/browse.ts';
import type {ContentEntry, ContentView} from '@core/library-content.ts';
import type {HomeSeeAll} from '@core/home.ts';

const views: readonly ContentView[] = ['discover', 'browse', 'collections', 'categories', 'show', 'season', 'artist', 'album', 'book', 'collection', 'releases', 'songs', 'authors', 'series', 'author', 'book_series', 'disc'];
const bounded = (value: unknown, max = 256): string | undefined => typeof value === 'string' && value.length > 0 && value.length <= max && !/[\x00-\x1f\x7f]/.test(value) ? value : undefined;
export function searchQuery(raw: Record<string, unknown>): {q: string} {
  return {q: typeof raw.q === 'string' && raw.q.length <= 2048 ? raw.q : ''};
}
export type LibrarySearch = Readonly<{view?: ContentView; entityId?: string; pivot?: string; section?: 'mixes'; query?: BrowseNode; sort?: readonly BrowseSortSelection[]; q?: string}>;
export function librarySearch(raw: Record<string, unknown>): LibrarySearch {
  const view = views.includes(raw.view as ContentView) ? raw.view as ContentView : undefined;
  const entityId = bounded(raw.entityId), pivot = bounded(raw.pivot, 64), q = bounded(raw.q, 512);
  let query: BrowseNode | undefined;
  try { if (raw.query !== undefined) query = parseBrowseNode(raw.query); } catch { /* An invalid URL predicate has no authority. */ }
  const sort = Array.isArray(raw.sort) && raw.sort.length <= 8 ? raw.sort.flatMap(value => {
    if (!value || typeof value !== 'object') return [];
    const candidate = value as Record<string, unknown>, field = bounded(candidate.field, 64);
    if (!field || candidate.direction !== 'asc' && candidate.direction !== 'desc' || candidate.seed !== undefined && (!Number.isSafeInteger(candidate.seed) || Number(candidate.seed) < 0)) return [];
    return [{field, direction: candidate.direction as 'asc' | 'desc', ...(candidate.seed === undefined ? {} : {seed: candidate.seed as number})}];
  }) : undefined;
  return {...(view ? {view} : {}), ...(entityId ? {entityId} : {}), ...(pivot ? {pivot} : {}), ...(raw.section === 'mixes' && view === 'discover' ? {section: 'mixes' as const} : {}), ...(query ? {query} : {}), ...(sort?.length ? {sort} : {}), ...(q ? {q} : {})};
}
export type HomeDestination =
  | {to: '/library/$libraryId'; params: {libraryId: string}; search: LibrarySearch}
  | {to: '/item/$itemId'; params: {itemId: string}}
  | {to: '/home/rows/$rowId'; params: {rowId: string}}
  | {to: '/saved'; search: {resourceId: string}}
  | {to: '/live'; search: {group?: string; sourceId?: string}};
export function entryDestination(entry: ContentEntry): HomeDestination {
  const navigation = entry.navigation;
  if (entry.kind === 'radio' || navigation?.view === 'radio') throw new DOMException('Radio requires its captured seed admission', 'AbortError');
  if(entry.kind==='playlist'||navigation?.view==='playlist')return{to:'/saved',search:{resourceId:navigation?.entityId??entry.id}};
  if (entry.libraryId && navigation && navigation.view !== 'item' && navigation.browse) {
    const {pivot, query, sort} = navigation.browse;
    return {to: '/library/$libraryId', params: {libraryId: entry.libraryId}, search: {view: navigation.view, pivot, query, sort}};
  }
  if (entry.libraryId && navigation && navigation.view !== 'item') return {to: '/library/$libraryId', params: {libraryId: entry.libraryId}, search: {view: navigation.view, entityId: navigation.entityId ?? entry.id}};
  return {to: '/item/$itemId', params: {itemId: navigation?.entityId ?? entry.id}};
}
export function seeAllDestination(target: HomeSeeAll): HomeDestination {
  switch (target.type) {
    case 'library': return {to: '/library/$libraryId', params: {libraryId: target.libraryId}, search: {pivot: target.pivot, query: target.query, sort: target.sort}};
    case 'row': return {to: '/home/rows/$rowId', params: {rowId: target.rowId}};
    case 'saved_view': return {to: '/saved', search: {resourceId: target.resourceId}};
    case 'collection': return {to: '/library/$libraryId', params: {libraryId: target.libraryId}, search: {view: 'collection', entityId: target.resourceId}};
    case 'guide': return {to: '/live', search: {sourceId: target.sourceId, group: target.group}};
  }
}
