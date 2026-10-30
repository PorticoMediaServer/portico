import {selectionCapabilityNames, selectionKinds} from './capabilities.ts';
import type {SelectedItem, SelectionCapabilities, SelectionKind, SelectionQueryType, SelectionSummary} from './types.ts';

/** Work is bounded by kind/library aggregates and known exclusions, never query.total. */
export function summarizeQuerySelection(
  facts: readonly SelectionQueryType[],
  kind: SelectionKind | null,
  exclusions: Iterable<SelectedItem>,
  unavailableActions?: SelectionSummary['unavailableActions'],
): SelectionSummary {
  const types = new Map<SelectionKind, number>(facts.filter(row => kind === null || kind === row.kind).map(row => [row.kind, row.count]));
  const libraries = new Map<string, number>();
  const capabilities = Object.fromEntries(selectionCapabilityNames.map(name => [name, 0])) as Record<keyof SelectionCapabilities, number>;
  let count = 0;
  for (const row of facts) {
    if (kind !== null && kind !== row.kind) continue;
    count += row.count;
    for (const name of selectionCapabilityNames) capabilities[name] += row.capabilityCounts[name];
    for (const library of row.libraries) libraries.set(library.libraryId, (libraries.get(library.libraryId) ?? 0) + library.count);
  }
  for (const {item} of exclusions) {
    if (item.kind === 'channel' || item.kind === 'person') continue;
    if (!types.has(item.kind)) continue;
    count--;
    types.set(item.kind, types.get(item.kind)! - 1);
    libraries.set(item.libraryId, libraries.get(item.libraryId)! - 1);
    for (const name of selectionCapabilityNames) if (item.capabilities[name]) capabilities[name]--;
  }
  const presentLibraries = [...libraries].filter(([, total]) => total > 0);
  return Object.freeze({
    count,
    ...(unavailableActions === undefined ? {} : {unavailableActions}),
    types: Object.freeze(selectionKinds.flatMap(value => {
      const total = types.get(value) ?? 0;
      return total > 0 ? [Object.freeze({kind: value, count: total})] : [];
    })),
    library: presentLibraries.length === 0 ? Object.freeze({kind: 'none' as const})
      : presentLibraries.length === 1 ? Object.freeze({kind: 'single' as const, libraryId: presentLibraries[0][0]})
      : Object.freeze({kind: 'multiple' as const}),
    every: Object.freeze(Object.fromEntries(selectionCapabilityNames.map(name => [name, count > 0 && capabilities[name] === count]))) as SelectionCapabilities,
  });
}
