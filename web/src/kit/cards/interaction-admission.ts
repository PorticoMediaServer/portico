/** A producer-owned row, independent of its repeatedly published descriptor. */
export type CardInteractionSource = Readonly<{
  owner: object;
  row: object;
  capture(node: HTMLElement): CardInteractionAdmission | undefined;
}>;

/** One original producer/row/physical-node admission. No selection grant. */
export type CardInteractionAdmission = Readonly<{
  source: CardInteractionSource;
  node: HTMLElement;
  /** Pure render projection; never an action/admission check. */
  readonly live: boolean;
  /** Imperative original-source check; may retire, so do not call during render. */
  current(): boolean;
  preview(enabled: boolean): void;
  /** Marks dead and clears only this admission's preview before notifying. */
  retire(): void;
  onRetire(listener: () => void): () => void;
}>;
type Entry = {admission: CardInteractionAdmission; live: boolean; listeners: Set<() => void>};
const mounted = new WeakMap<HTMLElement, Entry>();
const marker = 'data-card-interaction';

/** The committed physical tuple owns its receipt, including a retirement
 * tombstone. A preview-clear publication must not immediately recapture a row
 * that the presence owner just withdrew before making it inert. */
export function createCardInteractionBinding() {
  type Binding = {source?: CardInteractionSource; node: HTMLElement | null; disabled: boolean; admission?: CardInteractionAdmission};
  let committed: Binding | undefined;
  return Object.freeze({
    commit(source: CardInteractionSource | undefined, node: HTMLElement | null, disabled: boolean) {
      const previous = committed;
      const same = previous && previous.source === source && previous.node === node && previous.disabled === disabled;
      if (same && previous.admission) return previous.admission.current() ? previous.admission : undefined;
      const next: Binding = same ? previous : {source, node, disabled};
      committed = next;
      if (!same) previous?.admission?.retire();
      if (committed !== next || !source || !node || disabled) return;
      const admission = source.capture(node);
      if (committed !== next) {admission?.retire();return;}
      next.admission = admission;return admission;
    },
    release() {
      const previous = committed;committed = undefined;previous?.admission?.retire();
    },
  });
}

/** The presence owner can withdraw original interactions before making paint inert.
 * Only actual registered nodes below this captured DOM root are considered. */
export function retireCardInteractions(root: HTMLElement): void {
  captureCardInteractionRetirement([root])();
}

/** Snapshot before a caller's focus callbacks; withdraw after that handoff, before
 * DOM mutation. Neither callback reentrancy nor replacement rows renew this set. */
export function captureCardInteractionRetirement(roots: readonly HTMLElement[]): () => void {
  const originals = new Set(roots.flatMap(root => [root, ...root.querySelectorAll<HTMLElement>(`[${marker}]`)]
    .flatMap(node => {const original = mounted.get(node)?.admission;return original ? [original] : [];})));
  return () => {for (const original of originals) original.retire();};
}

/** One real selection producer owns preview arbitration. A stale row or old
 * physical mount can never clear another admission's preview, even for the same
 * catalogue identity. Predicates and preview callbacks are captured once. */
export function createCardInteractionOwner() {
  const owner = Object.freeze({}), sources = new WeakMap<object, CardInteractionSource>(), entries = new Set<Entry>();
  let closed = false, previewing: Entry | undefined;
  return Object.freeze({
    source(row: object, ports: Readonly<{current(): boolean; preview(enabled: boolean): void}>): CardInteractionSource {
      const previous = sources.get(row);if (previous) return previous;
      const originalCurrent = ports.current, originalPreview = ports.preview;
      const source: CardInteractionSource = Object.freeze({owner, row, capture(node: HTMLElement) {
        if (closed || !node.isConnected || node.closest('[inert],[aria-hidden="true"]') || !originalCurrent()) return;
        // The physical node, not a latest descriptor lookup, owns replacement.
        mounted.get(node)?.admission.retire();
        // Retirement can synchronously publish or replace the original source.
        if (!originalCurrent()) return;
        if (closed || !node.isConnected || node.closest('[inert],[aria-hidden="true"]') || mounted.has(node)) return;
        let entry: Entry;
        const clear = () => {
          if (previewing !== entry) return;
          previewing = undefined;
          originalPreview(false);
        };
        const admission: CardInteractionAdmission = Object.freeze({source, node,
          get live() {return entry.live;},
          current() {
            if (!entry.live || closed || mounted.get(node) !== entry) return false;
            if (!node.isConnected || node.closest('[inert],[aria-hidden="true"]') || !originalCurrent()) {admission.retire();return false;}
            return entry.live && !closed && mounted.get(node) === entry;
          },
          preview(enabled: boolean) {
            if (!entry.live || mounted.get(node) !== entry) return;
            if (!enabled) {clear();return;}
            if (!admission.current()) return;
            previewing = entry;
            originalPreview(true);
          },
          retire() {
            if (!entry.live) return;
            entry.live = false;entries.delete(entry);
            if (mounted.get(node) === entry) {mounted.delete(node);node.removeAttribute(marker);}
            // Unregister before any callback; reentrant successor captures survive.
            try {clear();} finally {
              const listeners = [...entry.listeners];entry.listeners.clear();
              for (const listener of listeners) listener();
            }
          },
          onRetire(listener: () => void) {
            if (!entry.live) {listener();return () => {};}
            entry.listeners.add(listener);return () => {entry.listeners.delete(listener);};
          },
        });
        entry = {admission, live: true, listeners: new Set()};
        entries.add(entry);mounted.set(node, entry);node.setAttribute(marker, '');
        return admission;
      }});
      sources.set(row, source);return source;
    },
    /** The real producer already cleared its preview (selection/context change). */
    resetPreview() {previewing = undefined;},
    /** Original producer publication can retire rows before any React cleanup. */
    prune() {for (const entry of [...entries]) entry.admission.current();},
    /** A window/source retirement withdraws all mounted original receipts now. */
    retireRows() {for (const entry of [...entries]) entry.admission.retire();},
    dispose() {closed = true;for (const entry of [...entries]) entry.admission.retire();},
  });
}
