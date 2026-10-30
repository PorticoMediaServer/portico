/** Only mounted/pinned slots are projected. No data, callbacks or O(total) state. */
export type OfferedNativeSlot = Readonly<{index: number; entityId?: string; waiting: boolean}>;
export type NativeWindowedSlot = OfferedNativeSlot & Readonly<{focusId: string; key: string; source: string; formerEntityId?: string}>;
export type NativeSlotProjection = Readonly<{owner: object; generation: number; ownerKey: number; pendingOwnerKey: number; nextPending: number; slots: readonly NativeWindowedSlot[]}>;

const owners = new WeakMap<object, number>();
let nextOwner = 0;
/** Opaque UI identity only; weak keys never retain a retired collection/authority. */
export function nativeSlotOwnerKey(owner: object): number {
  let key = owners.get(owner);
  if (key === undefined) {key = ++nextOwner; owners.set(owner, key);}
  return key;
}

/** Pure render projection; the caller publishes this only after React commits. */
export function projectNativeSlots(previous: NativeSlotProjection | undefined, owner: object, ownerKey: number, generation: number,
  incoming: readonly OfferedNativeSlot[], pendingOwnerKey = ownerKey): NativeSlotProjection {
  const sameOwner = previous?.owner === owner && previous.ownerKey === ownerKey && previous.pendingOwnerKey === pendingOwnerKey ? previous : undefined;
  const sameSource = sameOwner?.generation === generation ? sameOwner : undefined;
  const entities = new Map((sameOwner?.slots ?? []).filter(slot => slot.entityId !== undefined || slot.formerEntityId !== undefined).map(slot => [slot.entityId ?? slot.formerEntityId!, slot] as const));
  const positions = new Map((sameSource?.slots ?? []).map(slot => [slot.index, slot] as const));
  const source = JSON.stringify([ownerKey, generation]);
  // A resident entity owns its original host ahead of an unknown cell at its
  // former index. A reorder must never give the same native identity to both.
  const claimed = new Set(incoming.flatMap(slot => slot.entityId === undefined ? [] : [entities.get(slot.entityId)?.focusId]).filter(id => id !== undefined));
  let nextPending = sameOwner?.nextPending ?? 0;
  const slots = incoming.map(slot => {
    const at = positions.get(slot.index);
    const entity = slot.entityId === undefined ? undefined : entities.get(slot.entityId);
    // Only an actual same-source arrival fills its waiting host. A new owner or
    // generation never claims that an unrelated item fulfilled the old request.
    const formerEntityId = at?.entityId ?? at?.formerEntityId;
    const sameArrival = at?.waiting && (formerEntityId === undefined || formerEntityId === slot.entityId);
    const inherited = entity ?? ((slot.waiting || sameArrival) && at && !(slot.waiting && claimed.has(at.focusId)) ? at : undefined);
    const focusId = inherited?.focusId ?? JSON.stringify(slot.entityId === undefined
      ? [ownerKey, 'pending', pendingOwnerKey, generation, slot.index, nextPending++] : [ownerKey, 'entry', slot.entityId]);
    return {index: slot.index, entityId: slot.entityId, waiting: slot.waiting, focusId, key: JSON.stringify([ownerKey, focusId]), source,
      ...(slot.waiting && inherited === at && formerEntityId !== undefined ? {formerEntityId} : {})};
  });
  return {owner, ownerKey, pendingOwnerKey, nextPending, generation, slots};
}
