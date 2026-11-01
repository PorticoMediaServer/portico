import {homeSelectionWindowLimit} from '../../../../packages/client-core/src/home-selection/limits';
/** Plan one-item TV registrations against the single SelectionModel population.
 * Visible members and retained openers are mandatory; only overscan is optional. */
export type HomeWindowPopulation = Readonly<{id: string; visible: boolean; first: number; last: number; physicalIds: readonly string[]; retainedIds?: readonly string[]; visibleIds?: readonly string[]}>;
export function planHomeWindows(rows: readonly HomeWindowPopulation[], spotlightCount: number, maxWindows = homeSelectionWindowLimit): ReadonlyMap<string, readonly string[]> {
  return planHomeWindowsReport(rows, spotlightCount, maxWindows).plan;
}

/** The plan and how many mandatory members did not fit. Retained openers come first, then
 * visible members in row order; an overflow is reported for diagnostics, never thrown at the
 * person using Home (it is not theirs to fix, and there is nothing to retry). */
export function planHomeWindowsReport(rows: readonly HomeWindowPopulation[], spotlightCount: number, maxWindows = homeSelectionWindowLimit): Readonly<{plan: ReadonlyMap<string, readonly string[]>; overflow: number}> {
  const result = new Map<string, string[]>(rows.map(row => [row.id, []]));
  let count = spotlightCount, overflow = 0;
  const admit = (row: HomeWindowPopulation, id: string) => {
    const members = result.get(row.id)!;
    if (members.includes(id) || !row.physicalIds.includes(id)) return;
    if (count < maxWindows) {members.push(id); count++;} else overflow++;
  };
  for (const row of rows) for (const id of row.retainedIds ?? []) admit(row, id);
  for (const row of rows) if (row.visible) for (const id of row.visibleIds ?? row.physicalIds.slice(Math.max(0, row.first), row.last + 1)) admit(row, id);
  // Overscan is optional and only fills room that is left.
  for (const row of rows) {
    if (!row.visible) continue;
    const members = result.get(row.id)!;
    for (const index of [row.first - 1, row.last + 1, row.first - 2, row.last + 2]) {
      const id = row.physicalIds[index];
      if (id !== undefined && !members.includes(id) && count < maxWindows) {members.push(id); count++;}
    }
  }
  return {plan: result, overflow};
}

/** The selection model's registration budget, from this planner or the model itself. It is
 * recorded for diagnostics and never shown to the viewer as an error toast. */
export function homeRegistrationLimit(error: unknown): boolean {
  const code = (error as {code?: unknown} | null)?.code, message = error instanceof Error ? error.message : '';
  return [code, message].some(value => value === 'home-visible-registration-limit' || value === 'registration-limit');
}
