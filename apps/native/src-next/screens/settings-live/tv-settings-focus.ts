/** Vertical crossings join real groups; native geometry still moves within a
 * horizontal group and keeps its last focused child for the return crossing. */
export function tvGroupEdges(groups: readonly string[], id: string, above?: string): {up?: string; down?: string} {
  const index = groups.indexOf(id);
  return {up: index > 0 ? groups[index - 1] : above, down: index >= 0 ? groups[index + 1] : undefined};
}

export function tvSettingsResultGroups(groups: readonly string[], entry: (id: string) => unknown): readonly string[] {
  return groups.filter(id => entry(id) !== undefined);
}

export function tvProgrammeGroups(value: Readonly<{watch: boolean; record: boolean; remind: boolean; later: readonly string[]; recordError: boolean; preferenceError: boolean}>): readonly string[] {
  return [...(value.watch ? ['watch'] : []), ...(value.record ? ['record'] : []), 'favorite',
    ...(value.remind ? ['remind'] : []), ...value.later.map(id => `later:${id}`),
    ...(value.recordError ? ['record-error'] : []), ...(value.preferenceError ? ['preference-error'] : [])];
}
