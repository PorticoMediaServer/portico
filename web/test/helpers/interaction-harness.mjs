/**
 * Behaviour QA: a small renderer for screen interaction tests.
 *
 * It runs hooks with per-position storage, runs effects when their dependencies change, and
 * expands the screen's own components in place so nested controls (cards, shelves, dialogs) are
 * reached and their real callbacks invoked. Host elements are inert: only their props matter.
 * This models wiring, not layout or painting; visual capture stays with Visual QA.
 */
export function createRenderer(options = {}) {
  const fibers = new Map();
  const focused = [];
  let pending = [];
  let path = 'root';
  const firstResult = {focus: () => focused.push('first-result')};
  // A screen that measures its host (a width, a height) can ask for those host facts without
  // changing the inert element every other screen gets.
  const fakeElement = () => ({focus: () => focused.push('field'), querySelector: () => firstResult, ...options.element});
  const fiber = key => {
    let found = fibers.get(key);
    if (!found) { found = {slots: [], effects: [], cursor: 0}; fibers.set(key, found); }
    return found;
  };
  const react = {
    Fragment: 'Fragment',
    Suspense: 'Suspense',
    createElement: (type, props, ...children) => ({type, props: {...props, children}}),
    createContext: value => ({value, Provider: 'provider'}),
    memo: component => component,
    lazy: () => 'LazyPage',
    forwardRef: component => component,
    useRef: value => { const f = fiber(path); const index = f.cursor++; return (f.slots[index] ??= {current: value}); },
    useState: initial => {
      const f = fiber(path); const index = f.cursor++;
      if (!(index in f.slots)) f.slots[index] = typeof initial === 'function' ? initial() : initial;
      return [f.slots[index], next => { f.slots[index] = typeof next === 'function' ? next(f.slots[index]) : next; }];
    },
    useMemo: (run, deps) => {
      const f = fiber(path); const index = f.cursor++; const slot = f.slots[index];
      if (!slot || slot.deps.length !== deps.length || deps.some((value, i) => value !== slot.deps[i])) f.slots[index] = {deps, value: run()};
      return f.slots[index].value;
    },
    useCallback: (run, deps) => {
      const f = fiber(path); const index = f.cursor++; const slot = f.slots[index];
      if (!slot || deps === undefined || slot.deps.length !== deps.length || deps.some((value, i) => value !== slot.deps[i])) f.slots[index] = {deps, value: run};
      return f.slots[index].value;
    },
    useEffect: (run, deps) => { const f = fiber(path); pending.push({key: path, index: f.cursor++, run, deps}); },
    useLayoutEffect: (run, deps) => { const f = fiber(path); pending.push({key: path, index: f.cursor++, run, deps}); },
    useSyncExternalStore: (_subscribe, get) => get(),
    useContext: context => context.value,
  };
  react.default = react;
  const same = (a, b) => !!a && !!b && a.length === b.length && a.every((value, i) => value === b[i]);
  const walk = (node, key) => {
    if (Array.isArray(node)) return node.map((child, i) => walk(child, `${key}/${i}`));
    if (!node || typeof node !== 'object' || node.type === undefined) return node;
    if (typeof node.type === 'function') {
      const componentKey = `${key}#${node.type.name || 'component'}`;
      const f = fiber(componentKey); f.cursor = 0;
      const previous = path; path = componentKey;
      const out = node.type(node.props);
      path = previous;
      return walk(out, key);
    }
    if (node.props?.ref && typeof node.props.ref === 'object' && 'current' in node.props.ref) node.props.ref.current = fakeElement();
    if (!node.props) return node;
    // Children and a control the row was given both render.
    const children = 'children' in node.props ? walk(node.props.children, `${key}/c`) : node.props.children;
    const control = node.props.control !== undefined ? walk(node.props.control, `${key}/k`) : node.props.control;
    return {...node, props: {...node.props, children, control}};
  };
  const render = run => {
    pending = [];
    fiber(path).cursor = 0;
    const tree = walk(run(), path);
    for (const {key, index, run: effect, deps} of pending) {
      const f = fiber(key); const held = f.effects[index];
      if (held && deps !== undefined && same(held.deps, deps)) continue;
      held?.cleanup?.();
      const cleanup = effect();
      f.effects[index] = {deps, cleanup: typeof cleanup === 'function' ? cleanup : undefined};
    }
    pending = [];
    return tree;
  };
  // Unmount: the cleanups the screen's effects returned. A screen that is never unmounted leaves
  // whatever it started (a listener, a deadline) behind, and the file cannot leave the runner. The
  // order is React's for a deleted subtree: the parent lets go first, then its children in tree
  // order, and within one component in declaration order. react-dom 19.2.8 destroys the fiber
  // before descending to fiber.child in commitPassiveUnmountEffectsInsideOfDeletedTree_begin (and
  // the insertion/layout destroys in commitDeletionEffectsOnFiber do the same). Fibers are created
  // in that same pre-order, so creation order is unmount order. Every cleanup runs even if an
  // earlier one throws; the first thrown value is raised afterwards, falsy or not.
  const dispose = () => {
    let failure;
    let failed = false;
    for (const key of [...fibers.keys()]) {
      const f = fibers.get(key);
      for (let index = 0; index < f.effects.length; index++) {
        const held = f.effects[index];
        if (!held) continue;
        f.effects[index] = undefined;
        try { held.cleanup?.(); } catch (error) { if (!failed) { failed = true; failure = error; } }
      }
      f.cursor = 0;
    }
    fibers.clear();
    pending = [];
    if (failed) throw failure;
  };
  return {react, render, dispose, focused};
}

/** Every element in a rendered tree, depth first, including the actions a dialog is given. */
export function tree(node, out = []) {
  if (Array.isArray(node)) { for (const child of node) tree(child, out); return out; }
  if (!node || typeof node !== 'object' || node.type === undefined) return out;
  out.push(node);
  if (node.props) {
    tree(node.props.children, out);
    if (node.props.actions) tree(node.props.actions, out);
    if (node.props.action) tree(node.props.action, out);
    if (node.props.secondaryAction) tree(node.props.secondaryAction, out);
    if (node.props.trigger) tree(node.props.trigger, out);
    if (node.props.control) tree(node.props.control, out);
    if (node.props.secondary) tree(node.props.secondary, out);
  }
  return out;
}
export const all = (rendered, type) => rendered.filter(node => node.type === type);
export const one = (rendered, type) => {
  const found = all(rendered, type);
  if (found.length !== 1) throw new Error(`expected exactly one ${String(type)}, found ${found.length}`);
  return found[0];
};
/** The visible text of a node (its children only; props such as a label are read directly). */
export function textOf(node) {
  if (node === null || node === undefined || node === false) return '';
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(textOf).join('');
  return node.props ? textOf(node.props.children) : '';
}
export const tick = () => new Promise(resolve => setTimeout(resolve, 0));
export const pause = (ms = 320) => new Promise(resolve => setTimeout(resolve, ms));
