import {validSelectionBasis} from '@core/selection-basis.ts';
import type {BrowseWholeSelection} from '@core/browse.ts';
import {selectionCapabilityNames} from '@core/selection-model/capabilities.ts';
import type {SelectionCapabilities, SelectionQuery, WholeSelectionSelector} from '@core/selection-model/index.ts';

/** The server's approved whole-view descriptor as the selection model's query
 * contract. Every fact is copied from the descriptor; nothing is inferred, and
 * `summary.every` is derived exactly as the model recomputes it (a capability is
 * offered for the whole view only when every displayed title has it). */
export function wholeViewQuery(whole: BrowseWholeSelection): SelectionQuery {
  if (whole.selectionBasis !== undefined && !validSelectionBasis(whole.selectionBasis)) throw new Error('invalid-selection-basis');
  const selector = {
    query: {
      libraryId: whole.libraryId,
      pivot: whole.pivot,
      ...(whole.selectionBasis === undefined ? {} : {selectionBasis: whole.selectionBasis}),
      ...(whole.filter ? {filter: whole.filter} : {}),
      ...(whole.sort.length ? {sort: whole.sort} : {}),
    },
  } as WholeSelectionSelector;
  const types = whole.types.map(type => ({
    kind: type.kind as SelectionQuery['summary']['types'][number]['kind'],
    count: type.count,
    capabilityCounts: type.capabilities,
    libraries: type.libraries,
  }));
  const every = Object.fromEntries(selectionCapabilityNames.map(name => [name,
    types.length > 0 && types.every(type => type.capabilityCounts[name] === type.count)])) as Record<keyof SelectionCapabilities, boolean>;
  return {
    // The page revision and opaque full witness distinguish held selections.
    id: `whole:${whole.catalogRevision}:${whole.pivot}${whole.selectionBasis === undefined ? '' : ':' + whole.selectionBasis}`,
    selector,
    total: whole.total,
    catalogRevision: whole.catalogRevision,
    summary: {
      types,
      ...(whole.unavailableActions === undefined ? {} : {unavailableActions: Object.freeze({...whole.unavailableActions})}),
      library: types.length ? {kind: 'single' as const, libraryId: whole.libraryId} : {kind: 'none' as const},
      every,
    },
  };
}
