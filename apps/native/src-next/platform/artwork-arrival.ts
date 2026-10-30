type Clock = {now: () => number; set: (run: () => void, ms: number) => unknown; clear: (handle: unknown) => void};
type Slot = {order: number; sequence: number; ready?: () => void};

/** Decode may finish out of order. Only visible, uncached consumers join this
 * queue; retiring or failing a consumer releases its place without blocking art. */
export function createArtworkArrivals(clock: Clock, staggerMs = 40) {
  const slots = new Map<object, Slot>();
  let sequence = 0, timer: unknown, nextAt = 0;
  const pump = () => {
    if (timer !== undefined) return;
    const first = [...slots.values()].sort((a, b) => a.order - b.order || a.sequence - b.sequence)[0];
    if (!first?.ready) return;
    timer = clock.set(() => {
      timer = undefined;
      const entry = [...slots.entries()].sort((a, b) => a[1].order - b[1].order || a[1].sequence - b[1].sequence)[0];
      if (!entry?.[1].ready) return;
      slots.delete(entry[0]);
      nextAt = clock.now() + staggerMs;
      entry[1].ready();
      pump();
    }, Math.max(0, nextAt - clock.now()));
  };
  return {
    register(order: number) {
      const key = {}, slot: Slot = {order, sequence: sequence++};
      slots.set(key, slot);
      return {
        ready(run: () => void) {if (slots.has(key)) {slot.ready = run; pump();}},
        move(next: number) {if (slots.has(key)) {slot.order = next; pump();}},
        retire() {
          slots.delete(key);
          if (!slots.size) {if (timer !== undefined) clock.clear(timer); timer = undefined; nextAt = 0;}
          else pump();
        },
      };
    },
  };
}

const clock: Clock = {now: Date.now, set: (run, ms) => setTimeout(run, ms), clear: handle => clearTimeout(handle as ReturnType<typeof setTimeout>)};
let current = createArtworkArrivals(clock);
/** Retained outgoing pages finish their own arrivals, never holding up a new page. */
export function beginArtworkArrivalScreen() {current = createArtworkArrivals(clock);}
export function artworkArrivalScreen() {return current;}
