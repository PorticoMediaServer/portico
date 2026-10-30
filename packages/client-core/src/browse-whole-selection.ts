import {validSelectionBasis} from './selection-basis.ts';
import type {BrowseWholeSelection} from './browse.ts';
import {contentEntryKind} from './library-content.ts';
import {selectionCapabilityNames} from './selection-model/capabilities.ts';
import type {SelectionCapabilities, SelectionQuery, SelectionQueryType, SelectionSummary} from './selection-model/index.ts';

/** Sorted-key JSON: one view must encode identically however its object was built. */
function stable(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value) ?? 'null';
  if (Array.isArray(value)) return `[${value.map(stable).join(',')}]`;
  const record = value as Record<string, unknown>;
  return `{${Object.keys(record).sort().map(key => `${JSON.stringify(key)}:${stable(record[key])}`).join(',')}}`;
}

/** FNV-1a over the encoded view: a bounded, deterministic label, not a security
 * proof. Two different views can collide in principle; the model compares the
 * whole descriptor before it treats two registrations as one selection, so a
 * collision re-registers rather than merging two views' membership. */
function label(text: string): string {
  let hash = 0xcbf29ce484222325n;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= BigInt(text.charCodeAt(index));
    hash = (hash * 0x100000001b3n) & 0xffffffffffffffffn;
  }
  return hash.toString(16).padStart(16, '0');
}

/** One whole-view selection from the server's exact aggregate, or nothing when a
 * row cannot be expressed as a selection kind. Nothing is narrowed by a cast: the
 * aggregate's own kind strings pass the canonical content-kind guard or the whole
 * descriptor is refused, so Select all stays absent instead of selecting a subset.
 *
 * `every` is true only when that capability is offered for every displayed title
 * of every kind, and the library is single because the aggregate is read for one
 * library. The sort carries no random order or seed: the server only attaches this
 * aggregate for a view its job compiler replays unchanged.
 */
export function browseWholeSelectionQuery(whole: BrowseWholeSelection): SelectionQuery | undefined {
  if (!whole.total || !whole.types.length || whole.selectionBasis !== undefined && !validSelectionBasis(whole.selectionBasis)) return undefined;
  const types: SelectionQueryType[] = [];
  for (const type of whole.types) {
    if (!contentEntryKind(type.kind) || type.count < 1) return undefined;
    types.push({kind: type.kind, count: type.count,
      capabilityCounts: {...type.capabilities},
      libraries: type.libraries.map(({libraryId, count}) => ({libraryId, count}))});
  }
  const every = {} as Record<keyof SelectionCapabilities, boolean>;
  for (const name of selectionCapabilityNames) every[name] = whole.types.every(type => type.capabilities[name] === type.count);
  const summary: Omit<SelectionSummary, 'count' | 'types'> & Readonly<{types: readonly SelectionQueryType[]}> = {
    library: Object.freeze({kind: 'single' as const, libraryId: whole.libraryId}),
    every: Object.freeze(every) as SelectionCapabilities,
    types: Object.freeze(types),
    ...(whole.unavailableActions === undefined ? {} : {unavailableActions: Object.freeze({...whole.unavailableActions})}),
  };
  return Object.freeze({
    id: `browse-whole:${label(stable([whole.libraryId, whole.pivot, whole.filter, whole.sort, whole.catalogRevision]))}${whole.selectionBasis === undefined ? '' : ':' + whole.selectionBasis}`,
    selector: Object.freeze({query: Object.freeze({libraryId: whole.libraryId, pivot: whole.pivot,
      ...(whole.selectionBasis === undefined ? {} : {selectionBasis: whole.selectionBasis}),
      ...(whole.filter === null ? {} : {filter: whole.filter}),
      ...(whole.sort.length === 0 ? {} : {sort: Object.freeze(whole.sort.map(({field, direction}) => Object.freeze({field, direction})))})})}),
    total: whole.total,
    catalogRevision: whole.catalogRevision,
    summary: Object.freeze(summary),
  });
}
