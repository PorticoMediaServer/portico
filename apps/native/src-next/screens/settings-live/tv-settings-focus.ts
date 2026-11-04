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

/** What Show all arms on TV: `from` is the revision it was pressed at; `target` is the revision it produced (the first
 *  change after the press), the only one the ask may fire on. */
export type GuideReentry = Readonly<{from: number; target?: number}>;
export type GuideReentryEvent = Readonly<
  | {kind: 'press'; revision: number}
  | {kind: 'revision'; revision: number}
  | {kind: 'key'; key: string}
  | {kind: 'focus'}
  | {kind: 'filter'}>;

/** The one-shot entry ask after Show all. Armed at the press; the first revision change records the revision it
 *  produced; any later revision, a key event from the remote, a guide cell taking focus or a filter chosen again
 *  disarms it for good. Native recovery also moves focus when Show all unmounts, so only the remote's keys tell the
 *  person from recovery. A select key is not one of them: it is the press itself, which its key event may trail. */
export function guideReentryNext(state: GuideReentry | undefined, event: GuideReentryEvent): GuideReentry | undefined {
  switch (event.kind) {
    case 'press': return {from: event.revision};
    case 'revision':
      if (!state) return undefined;
      if (state.target === undefined) return event.revision === state.from ? state : {from: state.from, target: event.revision};
      return event.revision === state.target ? state : undefined;
    case 'key': return event.key === 'select' ? state : undefined;
    default: return undefined;
  }
}

/** Whether the guide's first row asks for TV entry focus: on the first draw of the page (not after a return from
 *  Recordings or the source page, which restore their own control), or once after Show all replaced the
 *  filtered-empty state, only on the revision it produced. A revision after that target never asks, even before
 *  the reducer has disarmed. */
export function liveGuideEntry(revision: number, recordingsReturn: number, managementReturn: number, reentry?: GuideReentry): boolean {
  return revision === 0 && recordingsReturn === 0 && managementReturn === 0
    || reentry !== undefined && revision !== reentry.from && (reentry.target === undefined || revision === reentry.target);
}

/** Whether the page can take a focus request now: its destination is Live and visible, and nothing is over it. */
export function livePageTakesFocus(page: Readonly<{covered: boolean; destination: string; contentVisible?: boolean; mediaExpanded?: boolean}>): boolean {
  return !page.covered && page.destination === 'live' && page.contentVisible !== false && !page.mediaExpanded;
}
