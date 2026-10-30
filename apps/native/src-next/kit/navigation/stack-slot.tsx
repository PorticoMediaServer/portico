import React, {createContext, useCallback, useContext, useLayoutEffect, useRef, useSyncExternalStore, type ReactNode} from 'react';

/** A screen module's retained page, keyed by the exact route object it hosts. */
type SlotRoute = object;
type Slot = Readonly<{id: number; node: ReactNode}>;

/** Where retained pages are drawn when the touch shell hosts a native stack.
 * Each module keeps owning its page; the stack screen for that route draws it,
 * so the page under a pop or an edge swipe stays painted. */
export class NativeStackSlots {
  private slots = new Map<SlotRoute, Map<number, ReactNode>>();
  private snapshots = new Map<SlotRoute, readonly Slot[]>();
  private listeners = new Map<SlotRoute, Set<() => void>>();
  private sequence = 0;
  nextId = (): number => ++this.sequence;
  set(route: SlotRoute, id: number, node: ReactNode): void {
    const slots = this.slots.get(route) ?? new Map<number, ReactNode>();
    if (slots.get(id) === node) return;
    slots.set(id, node); this.slots.set(route, slots); this.changed(route);
  }
  delete(route: SlotRoute, id: number): void {
    const slots = this.slots.get(route);
    if (!slots?.delete(id)) return;
    if (!slots.size) this.slots.delete(route);
    this.changed(route);
  }
  get = (route: SlotRoute): readonly Slot[] => this.snapshots.get(route) ?? noSlots;
  subscribe = (route: SlotRoute, listener: () => void): (() => void) => {
    const set = this.listeners.get(route) ?? new Set<() => void>();
    set.add(listener); this.listeners.set(route, set);
    return () => {set.delete(listener); if (!set.size && this.listeners.get(route) === set) this.listeners.delete(route);};
  };
  private changed(route: SlotRoute): void {
    const slots = this.slots.get(route);
    if (slots) this.snapshots.set(route, Object.freeze([...slots].map(([id, node]) => Object.freeze({id, node}))));
    else this.snapshots.delete(route);
    for (const listener of [...this.listeners.get(route) ?? []]) listener();
  }
}
const noSlots: readonly Slot[] = Object.freeze([]);

const SlotsContext = createContext<NativeStackSlots | undefined>(undefined);
export const NativeStackSlotsProvider = SlotsContext.Provider;

/** True when a native stack hosts this page: it stays laid out while covered,
 * because the stack (not `display: none`) hides it. Television: always false. */
export function useNativeStackHosted(): boolean {
  return useContext(SlotsContext) !== undefined;
}

/** Places a retained page in the stack screen for its own route. Without a stack
 * host (television, shallow fixtures) `place` returns the page unchanged, so the
 * tree is exactly as before; with one, the host renders nothing here and the
 * route's stack screen draws the page. `place` is a plain function: call the hook
 * first, then pass every returned page through it. */
export function useNativeStackPlacement(route: SlotRoute | undefined): (node: ReactNode) => ReactNode {
  const slots = useContext(SlotsContext), active = slots && route ? slots : undefined;
  const id = useRef<number | undefined>(undefined), pending = useRef<ReactNode>(null);
  if (active && id.current === undefined) id.current = active.nextId();
  useLayoutEffect(() => {if (active && route && id.current !== undefined) active.set(route, id.current, pending.current);});
  useLayoutEffect(() => {
    const value = id.current;
    return () => {if (active && route && value !== undefined) active.delete(route, value);};
  }, [active, route]);
  return node => {
    if (!active) return node;
    pending.current = node;
    return null;
  };
}

/** Draws the retained pages published for `route`. */
export function NativeStackSlotOutlet({slots, route}: {slots: NativeStackSlots; route: SlotRoute}) {
  const subscribe = useCallback((listener: () => void) => slots.subscribe(route, listener), [route, slots]);
  const read = useCallback(() => slots.get(route), [route, slots]);
  const value = useSyncExternalStore(subscribe, read, read);
  return <>{value.map(slot => <React.Fragment key={slot.id}>{slot.node}</React.Fragment>)}</>;
}
