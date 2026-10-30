import {validSelectionUnavailableActions} from './unavailable-actions.ts';
import {validSelectionBasis} from '../selection-basis.ts';
import {selectionCapabilities, selectionCapabilityNames, selectionKinds, selectionKindAllowsCapability} from './capabilities.ts';
import {summarizeQuerySelection} from './query-summary.ts';
import type {BrowseSelectionContext, SelectionCapabilities, SelectionContext, SelectionItem, SelectionKind, SelectionLimits, SelectionQuery, SelectionQueryType, WholeSelectionSelector} from './types.ts';

export type SelectionInputErrorCode = 'invalid-context' | 'invalid-item' | 'invalid-window' | 'invalid-query' | 'invalid-limits' | 'registration-limit' | 'conflicting-windows';
/** Diagnostic codes only. Renderers own catalogue-backed explanations. */
export class SelectionInputError extends Error {
  readonly code: SelectionInputErrorCode;
  constructor(code: SelectionInputErrorCode) { super(code); this.name = 'SelectionInputError'; this.code = code; }
}
export function requireSelection(value: unknown, code: SelectionInputErrorCode): asserts value {
  if (!value) throw new SelectionInputError(code);
}
export const selectionID = (value: unknown): value is string => typeof value === 'string' && value.length > 0 && value.length <= 4096 && !/[\x00-\x1f\x7f]/.test(value);
export const catalogueID = (value: unknown): value is string => selectionID(value) && value.length <= 256;
export const selectionCount = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const selectionKind = (value: unknown): value is SelectionKind => selectionKinds.some(kind => kind === value);
const record = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value) && [Object.prototype, null].includes(Object.getPrototypeOf(value));

export function checkedContext(input: SelectionContext): SelectionContext {
  requireSelection(record(input) && record(input.scope) && record(input.revision), 'invalid-context');
  const scope = input.scope, revision = input.revision;
  requireSelection(['serverId', 'accountId', 'profileId', 'viewId', 'privacyFence'].every(key => selectionID(scope[key as keyof typeof scope]))
    && (scope.authority === 'local' || scope.authority === 'hosted')
    && selectionID(revision.catalogRevision) && selectionID(revision.viewerRevision), 'invalid-context');
  return Object.freeze({
    scope: Object.freeze({serverId: scope.serverId, authority: scope.authority, accountId: scope.accountId, profileId: scope.profileId, viewId: scope.viewId, privacyFence: scope.privacyFence}),
    revision: Object.freeze({catalogRevision: revision.catalogRevision, viewerRevision: revision.viewerRevision}),
  });
}

export function checkedBrowseContext(input: BrowseSelectionContext): BrowseSelectionContext {
  requireSelection(record(input) && record(input.scope) && record(input.revision), 'invalid-context');
  const scope = input.scope, revision = input.revision;
  requireSelection(['serverId', 'accountId', 'profileId', 'viewId'].every(key => selectionID(scope[key as keyof typeof scope]))
    && (scope.authority === 'local' || scope.authority === 'hosted')
    && Object.keys(scope).every(key => ['serverId', 'authority', 'accountId', 'profileId', 'viewId'].includes(key))
    && Object.keys(revision).length === 1 && selectionID(revision.pageRevision), 'invalid-context');
  return Object.freeze({
    scope: Object.freeze({serverId: scope.serverId, authority: scope.authority, accountId: scope.accountId, profileId: scope.profileId, viewId: scope.viewId}),
    revision: Object.freeze({pageRevision: revision.pageRevision}),
  });
}

export function checkedItem(input: SelectionItem): SelectionItem {
  requireSelection(record(input) && selectionKinds.includes(input.kind), 'invalid-item');
  const capabilities = selectionCapabilities(input.kind, record(input.capabilities) ? input.capabilities : {});
  if (input.kind === 'channel') {
    requireSelection(catalogueID(input.sourceId) && catalogueID(input.channelId)
      && !('libraryId' in input) && !('id' in input), 'invalid-item');
    return Object.freeze({kind: input.kind, sourceId: input.sourceId, channelId: input.channelId, capabilities});
  }
  requireSelection(catalogueID(input.id) && !('sourceId' in input) && !('channelId' in input), 'invalid-item');
  if (input.kind === 'person') {
    requireSelection(!('libraryId' in input), 'invalid-item');
    return Object.freeze({kind: input.kind, id: input.id, capabilities});
  }
  requireSelection(catalogueID(input.libraryId), 'invalid-item');
  return Object.freeze({id: input.id, libraryId: input.libraryId, kind: input.kind, capabilities});
}

export const defaultSelectionLimits: SelectionLimits = Object.freeze({maxWindows: 32, maxWindowItems: 256, maxRegisteredItems: 2048, maxQueries: 32, maxQueryExclusions: 2048});
export function checkedLimits(input: Partial<SelectionLimits>): SelectionLimits {
  const result = {...defaultSelectionLimits, ...input};
  requireSelection(Object.values(result).every(value => selectionCount(value) && value > 0), 'invalid-limits');
  return Object.freeze(result);
}

/** Copy bounded JSON without retaining mutable filter objects or accepting executable values. */
function queryJSON(input: unknown): unknown {
  let remaining = 4096, text = 65536;
  const ancestors = new Set<object>();
  const visit = (value: unknown, depth: number): unknown => {
    requireSelection(--remaining >= 0 && depth <= 24, 'invalid-query');
    if (value === null || typeof value === 'boolean') return value;
    if (typeof value === 'number') { requireSelection(Number.isFinite(value), 'invalid-query'); return value; }
    if (typeof value === 'string') { text -= value.length; requireSelection(text >= 0, 'invalid-query'); return value; }
    requireSelection(Array.isArray(value) || record(value), 'invalid-query');
    requireSelection(!ancestors.has(value), 'invalid-query');
    ancestors.add(value);
    let result: unknown;
    if (Array.isArray(value)) {
      requireSelection(value.length <= remaining, 'invalid-query');
      result = Object.freeze(value.map(child => visit(child, depth + 1)));
    } else {
      const entries = Object.entries(value);
      requireSelection(entries.length <= remaining, 'invalid-query');
      result = Object.freeze(Object.fromEntries(entries.map(([key, child]) => {
        text -= key.length;
        requireSelection(text >= 0, 'invalid-query');
        return [key, visit(child, depth + 1)];
      })));
    }
    ancestors.delete(value);
    return result;
  };
  return visit(input, 0);
}

function checkedSelector(input: WholeSelectionSelector): WholeSelectionSelector {
  requireSelection(record(input) && Object.keys(input).length === 1, 'invalid-query');
  if ('query' in input) {
    const query = input.query;
    requireSelection(record(query) && catalogueID(query.libraryId) && catalogueID(query.pivot)
      && Object.keys(query).every(key => ['libraryId', 'pivot', 'filter', 'sort', 'selectionBasis'].includes(key))
      && (query.selectionBasis === undefined || validSelectionBasis(query.selectionBasis)), 'invalid-query');
    const sort = query.sort;
    requireSelection(sort === undefined || Array.isArray(sort) && sort.length <= 32 && sort.every(value =>
      record(value) && catalogueID(value.field) && (value.direction === 'asc' || value.direction === 'desc')
      && Object.keys(value).every(key => key === 'field' || key === 'direction')), 'invalid-query');
    return Object.freeze({query: Object.freeze({
      libraryId: query.libraryId, pivot: query.pivot,
      ...(query.selectionBasis === undefined ? {} : {selectionBasis: query.selectionBasis}),
      ...(query.filter === undefined ? {} : {filter: queryJSON(query.filter)}),
      ...(sort === undefined ? {} : {sort: Object.freeze(sort.map(value => Object.freeze({field: value.field, direction: value.direction})))}),
    })});
  }
  requireSelection('container' in input, 'invalid-query');
  const container = input.container;
  requireSelection(record(container) && catalogueID(container.id)
    && ['show', 'season', 'album', 'artist', 'book', 'collection', 'playlist', 'library'].includes(container.kind)
    && Object.keys(container).every(key => key === 'id' || key === 'kind'), 'invalid-query');
  return Object.freeze({container: Object.freeze({kind: container.kind, id: container.id})});
}

export function checkedQuery(input: SelectionQuery, context: SelectionContext): SelectionQuery {
  return validateQuery(input, context.revision.catalogRevision);
}

/** Browse reports one opaque page revision; the query contract's catalog revision is that
 * same value. Any other revision is a different view and is refused. */
export function checkedBrowseQuery(input: SelectionQuery, context: BrowseSelectionContext): SelectionQuery {
  requireSelection(record(context) && record(context.revision) && selectionID(context.revision.pageRevision), 'invalid-query');
  return validateQuery(input, context.revision.pageRevision);
}

function validateQuery(input: SelectionQuery, expectedRevision: string): SelectionQuery {
  requireSelection(record(input) && catalogueID(input.id) && selectionCount(input.total)
    && input.catalogRevision === expectedRevision && record(input.summary), 'invalid-query');
  const selector = checkedSelector(input.selector);
  const summary = input.summary;
  requireSelection(Array.isArray(summary.types) && summary.types.length <= selectionKinds.length
    && record(summary.every) && selectionCapabilityNames.every(name => typeof summary.every[name] === 'boolean')
    && record(summary.library), 'invalid-query');
  requireSelection(summary.unavailableActions === undefined || validSelectionUnavailableActions(summary.unavailableActions), 'invalid-query');
  const kinds = new Set<string>();
  let total = 0;
  let libraryFacts = 0;
  const types: SelectionQueryType[] = summary.types.map(row => {
    requireSelection(record(row), 'invalid-query');
    const kind = row.kind, count = row.count;
    requireSelection(selectionKind(kind) && kind !== 'channel' && kind !== 'person' && !kinds.has(kind)
      && selectionCount(count) && count > 0, 'invalid-query');
    kinds.add(kind); total += count;
    requireSelection(Number.isSafeInteger(total), 'invalid-query');
    requireSelection(record(row.capabilityCounts) && Array.isArray(row.libraries) && row.libraries.length <= 2048, 'invalid-query');
    const capabilityCounts: Record<keyof SelectionCapabilities, number> = {
      play: 0, queue: 0, playlist: 0, myList: 0, watched: 0, collection: 0,
      favorite: 0, download: 0, edit: 0, refresh: 0, delete: 0,
    };
    for (const name of selectionCapabilityNames) {
      const offered = row.capabilityCounts[name];
      requireSelection(selectionCount(offered) && offered <= count
        && (selectionKindAllowsCapability(kind, name) || offered === 0), 'invalid-query');
      capabilityCounts[name] = offered;
    }
    libraryFacts += row.libraries.length;
    requireSelection(libraryFacts <= 2048, 'invalid-query');
    let libraryTotal = 0;
    const seenLibraries = new Set<string>();
    const libraries = row.libraries.map(fact => {
      requireSelection(record(fact) && catalogueID(fact.libraryId) && !seenLibraries.has(fact.libraryId)
        && selectionCount(fact.count) && fact.count > 0, 'invalid-query');
      seenLibraries.add(fact.libraryId); libraryTotal += fact.count;
      requireSelection(Number.isSafeInteger(libraryTotal), 'invalid-query');
      return Object.freeze({libraryId: fact.libraryId, count: fact.count});
    });
    requireSelection(libraryTotal === count, 'invalid-query');
    return Object.freeze({kind, count, libraries: Object.freeze(libraries),
      capabilityCounts: Object.freeze(capabilityCounts)});
  });
  requireSelection(total === input.total, 'invalid-query');
  const library = summary.library;
  requireSelection(['none', 'single', 'multiple', 'unknown'].includes(library.kind)
    && (library.kind !== 'single' || catalogueID(library.libraryId))
    && (input.total === 0 ? library.kind === 'none' : library.kind !== 'none'), 'invalid-query');
  const libraryId = 'query' in selector ? selector.query.libraryId === '*' ? undefined : selector.query.libraryId
    : selector.container.kind === 'library' ? selector.container.id : undefined;
  requireSelection(input.total === 0 || libraryId === undefined || library.kind === 'single' && library.libraryId === libraryId, 'invalid-query');
  const facts = summarizeQuerySelection(types, null, []);
  requireSelection(facts.library.kind === library.kind
    && (facts.library.kind !== 'single' || library.kind === 'single' && facts.library.libraryId === library.libraryId)
    && selectionCapabilityNames.every(name => summary.every[name] === facts.every[name]), 'invalid-query');
  return Object.freeze({
    id: input.id, selector, total: input.total, catalogRevision: input.catalogRevision,
    summary: Object.freeze({types: Object.freeze(types), library: facts.library, every: facts.every,
      ...(summary.unavailableActions === undefined ? {} : {unavailableActions: Object.freeze({...summary.unavailableActions})})}),
  });
}
