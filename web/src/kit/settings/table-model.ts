/** Presentation only. Filtering and sorting belong to the checked server query. */
export type TableSort = Readonly<{by: string; direction: 'asc' | 'desc'}>;
export type TableQuery = Readonly<{search: string; filters: Readonly<Record<string, string>>; sort: TableSort}>;
export type TableWindow = Readonly<{first: number; last: number; before: number; after: number}>;

export const settingsTableRowHeight = 60;
export const settingsTableHeaderHeight = 40;
export function settingsTableWindow(count: number, scrollTop: number, height: number, overscan = 3): TableWindow {
  const size = Math.max(0, Math.floor(count));
  if (!size) return {first: 0, last: -1, before: 0, after: 0};
  const first = Math.min(size - 1, Math.max(0, Math.floor(Math.max(0, scrollTop) / settingsTableRowHeight) - overscan));
  const last = Math.min(size - 1, Math.ceil((Math.max(0, scrollTop) + Math.max(settingsTableRowHeight, height - settingsTableHeaderHeight)) / settingsTableRowHeight) + overscan - 1);
  return {first, last, before: first * settingsTableRowHeight, after: Math.max(0, size - last - 1) * settingsTableRowHeight};
}
export function nextTableSort(sort: TableSort, by: string): TableSort {
  return {by, direction: sort.by === by && sort.direction === 'asc' ? 'desc' : 'asc'};
}
export function selectTableRows(selected: ReadonlySet<string>, ids: readonly string[], checked: boolean): ReadonlySet<string> {
  const next = new Set(selected);
  for (const id of ids) checked ? next.add(id) : next.delete(id);
  return next;
}
/** A disappearing row returns focus to its neighbour, then to the search field. */
export function tableFocusFallback(before: readonly string[], after: readonly string[], removed: string): string | undefined {
  if (after.includes(removed)) return removed;
  const index = before.indexOf(removed);
  for (let offset = Math.max(0, index + 1); offset < before.length; offset++) if (after.includes(before[offset])) return before[offset];
  for (let offset = index - 1; offset >= 0; offset--) if (after.includes(before[offset])) return before[offset];
  return after[0];
}
