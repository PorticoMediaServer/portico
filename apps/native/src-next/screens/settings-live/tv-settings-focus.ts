/** Vertical crossings join real groups; native geometry still moves within a
 * horizontal group and keeps its last focused child for the return crossing. */
export function tvGroupEdges(groups: readonly string[], id: string, above?: string): {up?: string; down?: string} {
  const index = groups.indexOf(id);
  return {up: index > 0 ? groups[index - 1] : above, down: index >= 0 ? groups[index + 1] : undefined};
}

export function tvSettingsResultGroups(groups: readonly string[], entry: (id: string) => unknown): readonly string[] {
  return groups.filter(id => entry(id) !== undefined);
}

/** The groups in reading order. `laterFirst` is the TV sheet, which reads "Later on" before its actions. */
export function tvProgrammeGroups(value: Readonly<{watch: boolean; record: boolean; remind: boolean; later: readonly string[]; recordError: boolean; preferenceError: boolean; laterFirst?: boolean}>): readonly string[] {
  const later = value.later.map(id => `later:${id}`);
  return [...(value.laterFirst ? later : []), ...(value.watch ? ['watch'] : []), ...(value.record ? ['record'] : []), 'favorite',
    ...(value.remind ? ['remind'] : []), ...(value.laterFirst ? [] : later),
    ...(value.recordError ? ['record-error'] : []), ...(value.preferenceError ? ['preference-error'] : [])];
}

/** Whether the guide's first row asks for TV entry focus: on the first draw of the page (not after a return from
 *  Recordings or the source page, which restore their own control), or once after Show all replaced the
 *  filtered-empty state, on the first revision after the one it was pressed at. */
export function liveGuideEntry(revision: number, recordingsReturn: number, managementReturn: number, reentryFrom?: number): boolean {
  return revision === 0 && recordingsReturn === 0 && managementReturn === 0 || reentryFrom !== undefined && revision !== reentryFrom;
}
