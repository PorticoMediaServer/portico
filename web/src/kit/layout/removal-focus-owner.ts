import {applyRemovalFocus, removalFocusEligible} from './focus-context';

type Row = Readonly<{key: string; node: HTMLElement}>;
type Committed = Readonly<{epoch: object; root: HTMLElement; rows: readonly Row[]}>;
type Opening = {context: Committed; row: Row; opener: HTMLElement; content: HTMLElement | null; withdrawn: boolean; settled: boolean; retiringFocused?: boolean; pending?: ReadonlySet<string>; detach?: () => void};
const roots = new WeakMap<HTMLElement, RemovalFocusOwner>();
const portals = new WeakMap<HTMLElement, {owner: RemovalFocusOwner; opening: Opening}>();
const refused: ListFocusReturn = Object.freeze({attach() {}, retireContent() {}, detach() {}, restore() {}, dispose() {}});
let nextLifetime = 0;

/** A return receipt belongs to the actual opener and committed list, never to a replacement key. */
export type ListFocusReturn = Readonly<{attach: (content: HTMLElement) => void; retireContent: () => void; detach: () => void; restore: () => void; dispose: () => void}>;

/** Find provenance once, at opening, by walking the real opener's committed DOM ancestry. */
export function captureListFocusReturn(opener: HTMLElement | null): ListFocusReturn | undefined {
  for (let node = opener; node; node = node.parentElement) {
    const portal = portals.get(node);
    if (portal) return portal.owner.capture(opener!, portal.opening) ?? refused;
    const owner = roots.get(node);
    if (owner) return owner.capture(opener!) ?? refused;
  }
  return undefined;
}

/** Committed DOM only. The presence owner calls beforeLeave before its inert/removal commit. */
export class RemovalFocusOwner {
  /** React subtree identity only; it carries no item, source or authority facts. */
  readonly lifetime = ++nextLifetime;
  private epoch: object | undefined;
  private committed: Committed | undefined;
  private keyboard = false;
  private openings = new Set<Opening>();
  private detachRoot: (() => void) | undefined;

  activate() { this.dispose(); this.epoch = {}; }
  dispose() {
    this.epoch = undefined;
    this.detachRoot?.(); this.detachRoot = undefined;
    for (const opening of this.openings) opening.detach?.();
    this.openings.clear(); this.committed = undefined;
  }
  private listen(node: HTMLElement) {
    const key = () => { this.keyboard = true; };
    const pointer = () => { this.keyboard = false; };
    const focus = (event: FocusEvent) => {
      if (event.target instanceof HTMLElement) this.keyboard = event.target.matches(':focus-visible');
    };
    node.addEventListener('keydown', key, true); node.addEventListener('pointerdown', pointer, true); node.addEventListener('focusin', focus, true);
    return () => {node.removeEventListener('keydown', key, true);node.removeEventListener('pointerdown', pointer, true);node.removeEventListener('focusin', focus, true);};
  }
  commit(root: HTMLElement | null, nodes: readonly HTMLElement[] = root ? [...root.children] as HTMLElement[] : []) {
    if (!this.epoch) return;
    if (this.committed?.root !== root) {
      this.detachRoot?.(); this.detachRoot = undefined;
      for (const opening of this.openings) opening.detach?.();
      this.openings.clear(); this.keyboard = false;
      // A different root is a different original list, including strict-effect replay.
      this.epoch = {};
      if (root) {
        roots.set(root, this);
        const detach = this.listen(root);
        // A pointer interaction outside a portalled list is still pointer input. This observes
        // modality only; it never searches that document or chooses a target outside this root.
        const pointer = () => {this.keyboard = false;};
        const document = root.ownerDocument;document.addEventListener('pointerdown', pointer, true);
        this.detachRoot = () => {detach();document.removeEventListener('pointerdown', pointer, true);if (roots.get(root) === this) roots.delete(root);};
      }
    }
    this.committed = root ? {epoch: this.epoch, root, rows: nodes.flatMap(node => {
      const key = node.dataset.layoutItem;
      return key !== undefined && !node.hasAttribute('inert') ? [{key, node}] : [];
    })} : undefined;
  }
  private live(context: Committed) { return this.epoch === context.epoch && this.committed?.root === context.root && context.root.isConnected; }
  private surviving(context: Committed, remaining: ReadonlySet<string>) {
    return context.rows.filter(row => remaining.has(row.key) && row.node.dataset.layoutItem === row.key && context.root.contains(row.node)).map(row => row.node);
  }
  beforeLeave = (remaining: ReadonlySet<string>) => {
    const context = this.committed;
    if (!context || !this.live(context)) return;
    const active = context.root.ownerDocument.activeElement;
    const removed = context.rows.filter(row => !remaining.has(row.key));
    let focused = removed.find(row => active instanceof Node && row.node.contains(active));
    let focusedPortal: Opening | undefined;
    for (const opening of this.openings) {
      if (opening.settled || opening.withdrawn || !this.live(opening.context) || remaining.has(opening.row.key)) continue;
      opening.withdrawn = true; // Even pointer removal must suppress a delayed Radix return.
      const inPortal = active instanceof Node && opening.content?.contains(active);
      const lostWithPortal = opening.retiringFocused && !opening.content?.isConnected && (active === context.root.ownerDocument.body || active === null);
      if (inPortal || lostWithPortal) {focused = opening.row;focusedPortal = opening;}
    }
    if (!focused || !this.keyboard) return;
    // Only exact original survivors may take focus; no latest list/ID lookup or body fallback.
    const target = applyRemovalFocus({root: context.root, order: context.rows.map(row => row.node), surviving: this.surviving(context, remaining)}, focused.node);
    // A modal may still hide the rest of this exact list. Its actual close-autofocus callback
    // retries the original handoff after Radix releases that isolation, never the withdrawn opener.
    if (focusedPortal && (!target.element || context.root.ownerDocument.activeElement !== target.element))
      for (const opening of this.openings) if (opening.withdrawn && this.live(opening.context) && opening.row.node === focused.node) opening.pending = new Set(remaining);
  };
  capture(opener: HTMLElement, parent?: Opening): ListFocusReturn | undefined {
    const context = parent?.context ?? this.committed;
    if (!context || !this.live(context) || parent?.withdrawn || parent?.settled) return undefined;
    const row = parent?.row ?? context.rows.find(value => value.node.contains(opener));
    if (!row || row.node.dataset.layoutItem !== row.key || !context.root.contains(row.node)) return undefined;
    const opening: Opening = {context, row, opener, content: null, withdrawn: false, settled: false};
    if (opener.ownerDocument.activeElement === opener) this.keyboard = opener.matches(':focus-visible');
    this.openings.add(opening);
    return {
      attach: content => {
        if (opening.settled || !this.live(context) || opening.withdrawn) return;
        opening.retiringFocused = undefined;
        if (opening.content === content && opening.detach) return;
        opening.detach?.(); opening.content = content;
        portals.set(content, {owner: this, opening});
        const detach = this.listen(content);
        opening.detach = () => {detach();if (portals.get(content)?.opening === opening) portals.delete(content);};
      },
      // Called by the exact content ref's mutation-phase detach, while its real focus is still
      // observable. A sibling portal can disappear before the list's layout handoff runs.
      retireContent: () => {
        const active = context.root.ownerDocument.activeElement;
        opening.retiringFocused = !opening.settled && this.live(context) && this.keyboard && active instanceof Node && !!opening.content?.contains(active);
      },
      restore: () => {
        if (opening.settled) return;
        opening.settled = true;
        if (!this.live(context) || row.node.dataset.layoutItem !== row.key) return;
        if (opening.withdrawn) {
          const remaining = opening.pending;opening.pending = undefined;
          const active = context.root.ownerDocument.activeElement;
          if (remaining && (active === context.root.ownerDocument.body || active === null || active instanceof Node && opening.content?.contains(active)))
            applyRemovalFocus({root: context.root, order: context.rows.map(value => value.node), surviving: this.surviving(context, remaining)}, row.node);
          return;
        }
        if (!context.root.contains(row.node)) return;
        // A nested portal's opener may be outside the list, but it is still the exact captured node.
        if (removalFocusEligible(opener, parent ? null : context.root)) opener.focus({preventScroll: true});
      },
      detach: () => {opening.detach?.();opening.detach = undefined;},
      dispose: () => {opening.settled = true;opening.detach?.();opening.detach = undefined;this.openings.delete(opening);},
    };
  }
}
