import type {ResourceOrderWitness, SavedIntent} from '@core/saved.ts';

export type ResourceReorder = Extract<SavedIntent, {action: 'reorder'}>;
const changed = (message: string) => Object.assign(new Error(message), {code: 'revision_mismatch'});

export type ResourceOrderPreparation = Readonly<{prepare(): Promise<void>; current(): boolean}>;
/** The final authority check and invocation must share one synchronous turn:
 * preparation may yield to a newer resource revision before its caller resumes. */
export async function commitPreparedResourceOrder(preparation: ResourceOrderPreparation, mutate: () => Promise<void>): Promise<void> {
  await preparation.prepare();
  if (!preparation.current()) throw changed('The resource changed before its order could be saved.');
  await mutate();
}

/** A page of a playlist or collection in its own order, as the server published it: its
 * occurrences, the index of the first in the whole resource, how many the resource holds and,
 * under the same revision, the occurrences just outside the page and the resource's last. */
export type ResourceOrderPage = Readonly<{ids: readonly string[]; start: number; count: number; witness?: ResourceOrderWitness}>;

/** The occurrences a move from this page can name, in order: the page with its neighbours. */
export function resourceOrderIds(page: ResourceOrderPage): readonly string[] {
  const {previous, next} = page.witness ?? {};
  return [...(previous ? [previous] : []), ...page.ids, ...(next ? [next] : [])];
}

/** Move one occurrence among known slots without dropping hidden occurrence ids. */
export function resourceOrderMove(ids: readonly string[], entryId: string, targetId: string): ResourceReorder | undefined {
  const from = ids.indexOf(entryId), to = ids.indexOf(targetId);
  if (from < 0 || to < 0 || from === to || new Set(ids).size !== ids.length) return undefined;
  const start = Math.min(from, to), end = Math.max(from, to), moved = ids.slice(start, end + 1);
  if (moved.length > 200) return undefined;
  moved.splice(from - start, 1); moved.splice(to - start, 0, entryId);
  return {action: 'reorder', entryIds: moved};
}

/** Move to top goes to the front ("" anchor); Move to bottom goes after the witnessed last
 * occurrence. Neither is offered where the entry already is, or without a witness. */
export function resourceOrderEdge(page: ResourceOrderPage, entryId: string, edge: 'top' | 'bottom'): ResourceReorder | undefined {
  if (!page.witness || !page.ids.includes(entryId) || new Set(page.ids).size !== page.ids.length) return;
  if (edge === 'top') return page.start === 0 && page.ids[0] === entryId ? undefined : {action: 'reorder', entryIds: [entryId], afterEntryId: ''};
  const last = page.witness.last;
  return !last || last === entryId ? undefined : {action: 'reorder', entryIds: [entryId], afterEntryId: last};
}

/** The 1-based place the entry takes in the whole resource, for the announcement. */
export function resourceOrderPosition(intent: ResourceReorder, page: ResourceOrderPage, entryId: string): number | undefined {
  const index = intent.entryIds.indexOf(entryId);
  if (index < 0 || !page.witness) return;
  if (intent.afterEntryId === '') return index + 1;
  if (intent.afterEntryId !== undefined) return intent.afterEntryId === page.witness.last ? page.count - intent.entryIds.length + index + 1 : undefined;
  const ids = resourceOrderIds(page), offset = page.start - (page.witness.previous ? 1 : 0);
  const moved = new Set(intent.entryIds);
  let next = 0;
  const position = ids.map(id => moved.has(id) ? intent.entryIds[next++] : id).indexOf(entryId);
  return position < 0 ? undefined : offset + position + 1;
}
