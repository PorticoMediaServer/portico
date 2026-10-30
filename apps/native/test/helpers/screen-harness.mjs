import {readFile} from 'node:fs/promises';
import {compileFunction} from 'node:vm';
import {parseSync, transformSync} from 'rolldown/utils';

// Exercises a screen's exported pure helpers without a renderer or a server,
// mirroring portico-server/web/test/helpers/component-harness.mjs. Only pure
// exports are called; React and native modules are stubbed, never executed.
export async function screenModule(file, modules) {
  const transformed = transformSync(file.pathname, await readFile(file, 'utf8'), {tsconfig: false, jsx: {runtime: 'classic'}});
  if (transformed.errors.length) throw new Error(JSON.stringify(transformed.errors));
  // These modules run against explicit namespaces, including already-evaluated side-effect
  // modules. Parse declarations so a bare import cannot swallow the following named import,
  // and import-like text in a comment or string is never rewritten.
  const parsed = parseSync(file.pathname, transformed.code);
  if (parsed.errors.length) throw new Error(JSON.stringify(parsed.errors));
  let linked = transformed.code;
  for (const declaration of parsed.program.body.filter(node => node.type === 'ImportDeclaration').reverse()) {
    const source = JSON.stringify(declaration.source.value);
    // A fixture may intentionally provide virtual namespaces with a Proxy whose
    // unvisited commands throw. Resolve that namespace once, just as the old
    // destructuring did; an own-property check wrongly rejects such explicit ports.
    const namespace = `__screenModuleImport${declaration.start}`;
    const missing = declaration.specifiers.length ? `${namespace} == null`
      : `!Object.hasOwn(modules, ${source}) || ${namespace} == null`;
    const required = `const ${namespace} = modules[${source}]; if (${missing}) throw new Error(${JSON.stringify(`screenModule(${file.pathname}): no namespace for `)} + ${source});`;
    const bindings = declaration.specifiers.map(specifier => {
      const value = specifier.type === 'ImportNamespaceSpecifier' ? namespace
        : `${namespace}[${JSON.stringify(specifier.type === 'ImportDefaultSpecifier' ? 'default' : specifier.imported.name ?? specifier.imported.value)}]`;
      return `const ${specifier.local.name} = ${value};`;
    }).join('\n');
    linked = linked.slice(0, declaration.start) + required + bindings + linked.slice(declaration.end);
  }
  const exported = [];
  const code = linked.replace(/export (async )?(const|function|class) (\w+)/g, (_, asyncPrefix, kind, name) => { exported.push(name); return `${asyncPrefix ?? ''}${kind} ${name}`; })
    .replace(/export\s*\{([^}]+)\};/g, (_, bindings) => bindings.split(',').map(binding => {
      const [local, alias] = binding.trim().split(/\s+as\s+/); exported.push(alias ?? local);
      return alias ? `const ${alias} = ${local};` : '';
    }).join('\n'))
    .replaceAll('import.meta.env', '({DEV:false})');
  return compileFunction((/const React\s*=/.test(code) ? '' : 'const React = modules.react.default;\n') + code + '\nreturn {' + exported.join(',') + '};', ['modules'], {filename: file.pathname})(modules);
}

// P2-73: the touch shell's native stack. Off a stack host, retained pages render in
// place exactly as before (placement is the identity), so shallow fixtures see their previous tree.
const stackSlot = {NativeStackSlots: class {}, NativeStackSlotsProvider: ({children}) => children, NativeStackSlotOutlet: () => null,
  useNativeStackHosted: () => false, useNativeStackPlacement: () => node => node};
export const stackSlotFixture = {'../../kit/navigation/stack-slot': stackSlot, '../kit/navigation/stack-slot': stackSlot,
  './TouchStacks': {TouchStacks: () => null, nativeStackMotionTier: () => 'full', nativeStackAnimation: () => 'default'}};

export function hooks() {
  const slots = []; let cursor = 0;
  const ref = value => { const n = cursor++; return slots[n] ??= {current: value}; };
  const react = {
    createElement: (type, props, ...children) => ({type, props: {...props, children}}),
    useRef: ref,
    useState: initial => { const n=cursor++; if (!(n in slots)) slots[n]=typeof initial==='function'?initial():initial; return [slots[n], value=>{slots[n]=typeof value==='function'?value(slots[n]):value;}]; },
    useMemo: (fn, deps) => { const slot=ref(undefined); if (!slot.current || deps.some((v,i)=>v!==slot.current.deps[i])) slot.current={deps,value:fn()}; return slot.current.value; },
    useCallback: fn => fn,
    useEffect: () => {},
    useSyncExternalStore: (_, get) => get(),
    useContext: context => context.value,
  };
  return {react: {...react, default: react}, render: fn => {cursor=0;return fn();}};
}

// Explicit LIVE-only seams for existing shallow screen tests. These do not
// exercise retained paint; its reconciler coverage loads the real modules.
export const liveOverlayPaint = {
  useOverlayPaintReturnCurrent: () => undefined,
  useOverlayPaintMode: () => ({visible: true, interactive: true, root: true}),
  OverlayPaintProvider: 'OverlayPaintProvider', OverlayPaintDescendants: 'OverlayPaintDescendants',
};
const paintOpenings = new WeakMap(); let paintOpeningSequence = 0;
export const surfacePaintFixture = {
  overlayPaintKey: opening => {if (!paintOpenings.has(opening)) paintOpenings.set(opening, `paint-${++paintOpeningSequence}`); return paintOpenings.get(opening);},
  SurfacePaintHost: 'SurfacePaintHost', RetainedOverlay: ({children}) => children,
  overlayPaintPermit: current => ({current, subscribe: () => () => {}}),
};

// These shallow input/slot cases inspect the materialised current row; the
// actual handover hooks have separate source vectors and are not run here.
export const dockHandoverFixture = {DockHandover: 'DockHandover', DockPaintRow: 'DockPaintRow'};
// Geometry is outside these shallow command/focus fixtures. The production
// default without a publication is no shared cover and an unchanged local slot.
export const listeningCoverFixture = {
  ListeningCoverProvider: ({children}) => children,
  ListeningCoverLocalPaint: 'ListeningCoverLocalPaint',
  ListeningGroup: ({children}) => children,
  ListeningSeekPaint: 'ListeningSeekPaint', useListeningScrubberPublication: () => false,
  useListeningCoverPublication() {},
  useListeningPageStyle: () => ({}),
  useListeningTabBarPaint: () => ({style: {}, measure() {}}),
  useListeningPaintPermit: () => undefined, useListeningPageActive: () => true, useListeningPageOwner() {}, useListeningPageHeight: () => () => {}, useListeningPagePresent: () => false,
  useListeningCoverSlot: () => ({measure() {}, style: {}}),
};
