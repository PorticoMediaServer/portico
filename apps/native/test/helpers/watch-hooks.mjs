/** Explicit hook/host fixture for the watch component tests. It runs effects and
 * refs after each render; it does not model native layout or React concurrency. */
export function watchHooks() {
  const cells = [], effects = [], focused = [];
  let cursor = 0, dirty = false, renderBody, tree;
  const same = (a, b) => a && b && a.length === b.length && a.every((value, i) => Object.is(value, b[i]));
  const cell = () => {const index = cursor++; return cells[index] ??= {};};
  const effect = (create, deps) => {
    const value = cell();
    if (!same(value.deps, deps)) {value.deps = deps; effects.push(() => {value.cleanup?.(); value.cleanup = create();});}
  };
  const react = {
    createElement: (type, props, ...children) => ({type, props: {...props, children: children.length === 1 ? children[0] : children}}),
    useRef: initial => {const value = cell(); return value.ref ??= {current: initial};},
    useState: initial => {const value = cell(); if (!Object.hasOwn(value, 'state')) value.state = typeof initial === 'function' ? initial() : initial;
      return [value.state, next => {const resolved = typeof next === 'function' ? next(value.state) : next; if (!Object.is(value.state, resolved)) {value.state = resolved; dirty = true;}}];},
    useMemo: (create, deps) => {const value = cell(); if (!same(value.deps, deps)) {value.deps = deps; value.memo = create();} return value.memo;},
    useCallback: (callback, deps) => react.useMemo(() => callback, deps),
    useEffect: effect, useLayoutEffect: effect,
    useSyncExternalStore: (subscribe, read) => {const value = cell(); if (value.subscribe !== subscribe) {
      value.subscribe = subscribe; effects.push(() => {value.cleanup?.(); value.cleanup = subscribe(() => {dirty = true;});});
    } return read();},
  };
  const attach = node => {
    if (Array.isArray(node)) {node.forEach(attach); return;}
    if (!node?.props) return;
    const ref = node.props.ref, host = {requestTVFocus: () => focused.push(node.props.focusId ?? node.props.testID ?? node.props.id), scrollTo() {}};
    if (typeof ref === 'function') ref(host); else if (ref) ref.current = host;
    attach(node.props.children); attach(node.props.headerContent); attach(node.props.footer);
  };
  const render = body => {
    if (body) renderBody = body;
    cursor = 0; dirty = false; tree = renderBody(); attach(tree);
    for (const run of effects.splice(0)) run();
    return tree;
  };
  return {react: {...react, default: react}, focused, render,
    flush() {for (let count = 0; dirty && count < 30; count++) render(); if (dirty) throw new Error('Watch fixture did not settle'); return tree;},
    unmount() {for (const value of cells) value.cleanup?.();},
  };
}

export const descendants = node => Array.isArray(node) ? node.flatMap(descendants)
  : node?.props ? [node, ...descendants(typeof node.props.children === 'function' ? node.props.children({focused: false}) : node.props.children), ...descendants(node.props.headerContent), ...descendants(node.props.footer)] : [];
export const flattenStyle = value => Array.isArray(value) ? Object.assign({}, ...value.map(flattenStyle)) : value ?? {};
