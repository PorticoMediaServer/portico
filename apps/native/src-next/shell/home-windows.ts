/** Plan one-item TV registrations against the single SelectionModel population.
 * Visible members and retained openers are mandatory; only overscan is optional. */
export type HomeWindowPopulation = Readonly<{id: string; visible: boolean; first: number; last: number; physicalIds: readonly string[]; retainedIds?: readonly string[]; visibleIds?: readonly string[]}>;
export function planHomeWindows(rows: readonly HomeWindowPopulation[], spotlightCount: number, maxWindows = 32): ReadonlyMap<string, readonly string[]> {
  const result = new Map<string, string[]>();
  let count = spotlightCount;
  for (const row of rows) {
    const members = new Set(row.retainedIds?.filter(id => row.physicalIds.includes(id)));
    if (row.visible) for (const id of (row.visibleIds ?? row.physicalIds.slice(Math.max(0, row.first), row.last + 1)).filter(id => row.physicalIds.includes(id))) members.add(id);
    result.set(row.id, [...members]); count += members.size;
  }
  // Never silently evict an interactive visible item to admit another row's overscan.
  if (count > maxWindows) throw new Error('home-visible-registration-limit');
  for (const row of rows) {
    if (!row.visible) continue;
    const members = result.get(row.id)!;
    for (const index of [row.first - 1, row.last + 1, row.first - 2, row.last + 2]) {
      const id = row.physicalIds[index];
      if (id !== undefined && !members.includes(id) && count < maxWindows) {members.push(id); count++;}
    }
  }
  return result;
}
