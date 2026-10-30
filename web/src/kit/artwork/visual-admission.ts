/** Paint admission for the currently visible mounted group, not I/O ordering.
 * Mount before decode; a pending predecessor waits for its own decode or retirement.
 * motion §2.14 caps adjacent picture starts at 40ms; it does not require a 40ms
 * minimum. Drain each contiguous decoded frontier in order, without a timer or a
 * second scheduling boundary. The native fade remains the image owner's concern.
 */
export type AdmissionMount = Readonly<{
  /** The actual owning scheduler; absent scope never joins a group. */
  scope?: unknown;
  group?: string;
  /** The consumer's real finite reading position, never an invented index. */
  order?: number;
}>;
export type AdmissionSlot = Readonly<{
  ready: (paint: () => void) => void;
  retire: () => void;
}>;
export type PaintAdmission = Readonly<{
  mount: (target: AdmissionMount) => AdmissionSlot;
  dispose: () => void;
  size: () => number;
}>;

type Entry = {order: number; seq: number; live: boolean; ready: boolean; paint?: () => void};
type Group = {queue: Entry[]; flushing: boolean};

export function createPaintAdmission(): PaintAdmission {
  const scoped = new Map<unknown, Map<string, Group>>();
  let seq = 0, live = 0, disposed = false;
  const groupOf = (scope: unknown, name: string) => scoped.get(scope)?.get(name);
  const withdraw = (entry: Entry) => {
    if (!entry.live) return;
    entry.live = false; entry.paint = undefined; live = Math.max(0, live - 1);
  };
  const flush = (scope: unknown, name: string) => {
    const group = groupOf(scope, name);
    if (disposed || !group || group.flushing) return;
    group.flushing = true;
    try {
      // Re-read after every original callback: it may retire a sibling, replace
      // a pending position, dispose the registry or decode the next slot.
      while (!disposed && groupOf(scope, name) === group) {
        group.queue = group.queue.filter(entry => entry.live);
        const head = group.queue[0];
        if (!head?.ready || !head.paint) break;
        group.queue.shift();
        const paint = head.paint; withdraw(head);
        try {paint();} catch { /* one failed paint never strands the ready frontier */ }
      }
    } finally {
      group.flushing = false;
      group.queue = group.queue.filter(entry => entry.live);
      if (!group.queue.length && groupOf(scope, name) === group) {
        const names = scoped.get(scope)!;
        names.delete(name); if (!names.size) scoped.delete(scope);
      }
    }
  };
  return {
    mount(target) {
      if (disposed) return {ready() {}, retire() {}};
      const scope = target.scope;
      const name = typeof target.group === 'string' && target.group !== '' ? target.group : undefined;
      const order = typeof target.order === 'number' && Number.isFinite(target.order) ? target.order : undefined;
      const entry: Entry = {order: order ?? 0, seq: seq++, live: true, ready: false}; live++;
      if (scope === undefined || scope === null || name === undefined || order === undefined) return {
        ready(paint) {
          if (disposed || !entry.live || entry.ready) return;
          entry.ready = true; withdraw(entry);
          try {paint();} catch { /* independent paint owns no shared ordering */ }
        },
        retire() {withdraw(entry);},
      };
      let names = scoped.get(scope);
      if (!names) {names = new Map(); scoped.set(scope, names);}
      let group = names.get(name);
      if (!group) {group = {queue: [], flushing: false}; names.set(name, group);}
      group.queue.push(entry);
      group.queue.sort((left, right) => left.order - right.order || left.seq - right.seq);
      flush(scope, name);
      return {
        ready(paint) {
          if (disposed || !entry.live || entry.ready) return;
          entry.ready = true; entry.paint = paint; flush(scope, name);
        },
        retire() {if (!entry.live) return; withdraw(entry); flush(scope, name);},
      };
    },
    dispose() {
      disposed = true;
      for (const names of scoped.values()) for (const group of names.values()) {
        for (const entry of group.queue) withdraw(entry);
        group.queue = [];
      }
      scoped.clear(); live = 0;
    },
    size() {return live;},
  };
}
