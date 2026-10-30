export type RowSlot<T> = Readonly<{id: string; focusId: string; sourceFocusId: string; item: T; waiting: boolean; position: number; requestId?: string}>;
export type RowPage<T> = Readonly<{
  hasMore: boolean; loading: boolean; failed?: boolean;
  /** Stable source request cursor; the visible waiting host survives empty page advances. */
  waitingId: string; waitingItem: T; requestNext: () => void;
  /** Only the matching response may fill its waiting host. Omit firstItemId for an empty/filtered page. */
  fulfilled?: Readonly<{waitingId: string; firstItemId?: string}>;
}>;

/** A focused/retained row accepts appends and removals but defers server reordering. */
export function reconcileRowSlots<T>(previous: readonly RowSlot<T>[], items: readonly T[], getKey: (item: T) => string, owned: boolean, page?: RowPage<T>, getFocusId: (item: T) => string = getKey): readonly RowSlot<T>[] {
  const byId = new Map(items.map(item => [getKey(item), item]));
  if (byId.size !== items.length) throw new Error('Row item identities must be unique.');
  const oldById = new Map(previous.filter(slot => !slot.waiting).map(slot => [slot.sourceFocusId, slot]));
  const waiting = previous.find(slot => slot.waiting);
  const matching = waiting && page?.fulfilled?.waitingId === (waiting.requestId ?? waiting.id);
  const firstItemId = matching ? page?.fulfilled?.firstItemId : undefined;
  let tail = previous.reduce((max, slot) => Math.max(max, slot.position), -1) + 1;
  const slots: RowSlot<T>[] = items.map((item, index) => {
    const id = getKey(item), sourceFocusId = getFocusId(item), old = oldById.get(sourceFocusId);
    const fills = waiting && !old && id === firstItemId;
    return {id, focusId: old?.focusId ?? (fills ? waiting.focusId : sourceFocusId), sourceFocusId, item, waiting: false,
      position: owned ? old?.position ?? (fills ? waiting.position : tail++) : index};
  });
  if (new Set(slots.map(slot => slot.focusId)).size !== slots.length) throw new Error('Row focus identities must be unique.');
  if (page?.hasMore && !page.failed && items.length) {
    const filled = waiting && slots.some(slot => slot.focusId === waiting.focusId);
    const keep = waiting && !filled && ((waiting.requestId ?? waiting.id) === page.waitingId || matching && firstItemId === undefined);
    const focusId = keep ? waiting.focusId : page.waitingId;
    if (slots.some(slot => slot.focusId === focusId)) throw new Error('Each waiting page must have a new stable cursor identity.');
    slots.push({id: focusId, focusId, sourceFocusId: focusId, item: page.waitingItem, waiting: true, requestId: page.waitingId,
      position: owned && keep ? waiting.position : slots.reduce((max, slot) => Math.max(max, slot.position), -1) + 1});
  }
  if (!owned) return slots;
  const byFocus = new Map(slots.map(slot => [slot.focusId, slot]));
  const retained = previous.flatMap(slot => {const live = byFocus.get(slot.focusId); return live ? [live] : [];});
  const oldFocus = new Set(previous.map(slot => slot.focusId));
  return [...retained, ...slots.filter(slot => !oldFocus.has(slot.focusId))];
}

export function indexRowSlots<T>(slots: readonly RowSlot<T>[]) {
  return {slots, byFocus: new Map(slots.map(slot => [slot.focusId, slot])), byPosition: new Map(slots.map(slot => [slot.position, slot])),
    length: slots.reduce((max, slot) => Math.max(max, slot.position + 1), 0), order: slots.map(slot => slot.focusId)};
}
type Recovery = {childId: string; order: readonly string[]};
export function visibleRowSlots<T>(index: ReturnType<typeof indexRowSlots<T>>, first: number, last: number, focused?: string, remembered?: string, recovery: readonly Recovery[] = []): readonly RowSlot<T>[] {
  const visible = new Map<string, RowSlot<T>>();
  for (let position = Math.max(0, first - 2); position <= Math.min(index.length - 1, last + 2); position++) {
    const slot = index.byPosition.get(position); if (slot) visible.set(slot.focusId, slot);
  }
  const pin = (id: string | undefined) => {const slot = id ? index.byFocus.get(id) : undefined; if (slot) visible.set(slot.focusId, slot); return !!slot;};
  for (const id of [focused, remembered]) pin(id);
  for (const saved of recovery) {
    if (pin(saved.childId)) continue;
    const at = saved.order.indexOf(saved.childId);
    const fallback = [...saved.order.slice(at + 1), ...saved.order.slice(0, Math.max(0, at)).reverse()].find(id => index.byFocus.has(id));
    pin(fallback ?? index.order[0]);
  }
  if (!visible.size && index.slots.length) pin(index.order[0]);
  return [...visible.values()];
}
export function rowNeedsPage(last: number, count: number, page: {hasMore: boolean; loading: boolean; failed?: boolean} | undefined): boolean {
  return !!page?.hasMore && !page.loading && !page.failed && count > 0 && last >= Math.max(0, count - 10);
}
