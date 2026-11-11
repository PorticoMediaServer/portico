import * as catalogueActions from '../../../../packages/client-core/src/query/catalogue-actions.ts';
import * as catalogueReads from '../../../../packages/client-core/src/query/catalogue-reads.ts';
import * as playbackQueries from '../../../../packages/client-core/src/query/playback.ts';
import * as preparingCleanup from '../../../../packages/client-core/src/playback-v1/preparing-cleanup.ts';
import * as subtitleUploads from '../../../../packages/client-core/src/playback-v1/subtitle-upload.ts';
import * as ownerSettingsPolicy from '../../../../packages/client-core/src/owner-settings-policy.ts';
import * as ownerRequests from '../../../../packages/client-core/src/owner-requests.ts';
import * as ownerLibraryRequests from '../../../../packages/client-core/src/owner-library-requests.ts';
import * as ownerUploadRequests from '../../../../packages/client-core/src/owner-upload-requests.ts';
import * as ownerAccountClients from '../../../../packages/client-core/src/owner-account-clients.ts';
import {readFile} from 'node:fs/promises';
import {compileFunction} from 'node:vm';
import {parseSync, transformSync} from 'rolldown/utils';
import * as receiverQueries from '../../../../packages/client-core/src/query/receiver.ts';
import * as serverClock from '../../../../packages/client-core/src/server-clock.ts';
import * as userMutationClock from '../../../../packages/client-core/src/query/user-mutation-clock.ts';
import * as nativeStopScope from '../../src-next/platform/native-stop-scope.ts';
import * as personalHistoryActions from '../../../../packages/client-core/src/personal-history-actions.ts';
import * as failureWords from '../../src-next/screens/watch/failure-words.ts';
import * as tvPlayPause from '../../src-next/platform/tv-play-pause.ts';
import * as shellFreshness from '../../src-next/shell/freshness.ts';
import {replacementRefocusHook} from '../../src-next/kit/focus/replacement-refocus-hook.ts';
import {railHoldHooks} from '../../src-next/kit/navigation/rail-hold-hooks.ts';

import * as connectionRoutes from '../../../../packages/contracts/src/routes.ts';
import * as hostedConnectionRoutes from '../../../../packages/contracts/src/hosted/routes.ts';
import * as routeIdentity from '../../../../packages/client-core/src/route-identity.ts';
import * as installation from '../../../../packages/client-core/src/installation.ts';
import * as serverApi from '../../../../packages/client-core/src/query/server-api.ts';

import * as nativeAccountClient from '../../../../packages/client-core/src/query/native-account-client.ts';
import * as profileTransport from '../../../../packages/client-core/src/query/profile-security-transport.ts';
import * as core from '../../../../packages/client-core/src/index.ts';
import * as hostedSecurity from '../../../../packages/client-core/src/hosted-security.ts';
import * as accountNotifications from '../../../../packages/client-core/src/account-notifications.ts';
import * as nativeAccountWork from '../../../../packages/client-core/src/query/native-account-work.ts';
import * as nativeAbortSignals from '../../../../packages/client-core/src/native-abort-signals.ts';
import * as browserAccountApi from '../../../../packages/client-core/src/query/browser-account-api.ts';

// Pure leaves a screen gets for real (or as a faithful double) unless a fixture supplies its own.
// - The server clock has no platform dependency: the real module, whether a screen imports it by
//   its packages/ path or a client-core file by its own './server-clock.ts' (tests that fake time
//   supply their own).
// - The player's failure words (screens/watch/failure-words) are pure copy over the catalogue: the real module.
// - The TV Play/Pause owner (platform/tv-play-pause) is module state over react-native's Platform: the real
//   module, so a PIN pad's registration and the player's meet in one place.
// - The shell's cache accessor (shell/freshness): the real module, so a screen over a fixture's API
//   reads through a viewer cache of that same API.
// - The page inset (kit/layout/page-inset) is the real context over the fixture's own React, and the page width
//   is the fixture's own window less it, so a screen sized from a fixture window keeps that size (N2-3).
// - The covered-screen layer (kit/focus/CoveredLayer) behaves as on touch: a View hidden from
//   touches and screen readers while covered.
// - The replacement refocus hook (kit/focus/replacement-refocus) is the real one over the fixture's own React: a
//   component that keeps focus in a replaced row (Row, the TV search grid) needs no entry in a hand-built map.
// - The rail hold (kit/navigation/rail-hold) is the real one over the fixture's own React, so the shell and a page with a
//   side panel need no entry in a hand-built map; a fake `hooks()` React without context holds nothing.
const pageInsets = new WeakMap();
function pageInsetLeaf(react, native) {
  // The fake `hooks()` React has no context: its provider is a named element and the inset reads zero.
  let context = pageInsets.get(react);
  if (!context) {context = react.createContext ? react.createContext(0) : {Provider: 'NativePageInsetProvider'}; pageInsets.set(react, context);}
  // A fake React's useContext can't read a stand-in context: anything but a number is no inset.
  const usePageInset = () => {const value = react.createContext && react.useContext ? react.useContext(context) : 0; return typeof value === 'number' ? value : 0;};
  return {NativePageInsetProvider: context.Provider, usePageInset,
    usePageWidth: () => Math.max(0, (native?.useWindowDimensions?.().width ?? 0) - usePageInset()),
    pageInsetElement: (inset, children) => react.createElement(context.Provider, {value: inset}, children)};
}
const railHolds = new WeakMap();
function railHoldLeaf(react) {
  if (!react.createContext || !react.useContext || !react.useLayoutEffect) return {RailHoldProvider: ({children}) => children, useRailHold: () => {}, useRailHeld: () => false};
  let hooks = railHolds.get(react);
  if (!hooks) {hooks = railHoldHooks(react); railHolds.set(react, hooks);}
  return hooks;
}
// Moved core operations execute against the fixture's captured API; no request is mocked here.
const leaf = (source, react, native) => /client-core\/src\/query\/playback(\.ts)?$/.test(source) ? playbackQueries
  : /client-core\/src\/query\/catalogue-actions(?:\.ts)?$/.test(source) ? catalogueActions
  : /client-core\/src\/query\/catalogue-reads(?:\.ts)?$/.test(source) ? catalogueReads
  : /client-core\/src\/playback-v1\/preparing-cleanup(\.ts)?$/.test(source) ? preparingCleanup
  : /client-core\/src\/playback-v1\/subtitle-upload(\.ts)?$/.test(source) ? subtitleUploads
  : /client-core\/src\/query\/receiver(\.ts)?$/.test(source) ? receiverQueries
  : /\/query\/user-mutation-clock(?:\.ts)?$/.test(source) ? userMutationClock
  : /(^|\/)native-stop-scope$/.test(source) ? nativeStopScope
  : /\/personal-history-actions(?:\.ts)?$/.test(source) ? personalHistoryActions
  : /client-core\/src\/query\/native-account-client(?:\.ts)?$/.test(source) ? nativeAccountClient
  : /client-core\/src\/query\/profile-security-transport(?:\.ts)?$/.test(source) ? profileTransport
  : /(^|\/)server-clock(\.ts)?$/.test(source) && /client-core\/src\/server-clock|^\.\/server-clock/.test(source) ? serverClock
  : /(^|\/)owner-settings-policy(?:\.ts)?$/.test(source) ? ownerSettingsPolicy
  : /(^|\/)owner-requests(?:\.ts)?$/.test(source) ? ownerRequests
  : /(^|\/)owner-library-requests(?:\.ts)?$/.test(source) ? ownerLibraryRequests
  : /(^|\/)owner-upload-requests(?:\.ts)?$/.test(source) ? ownerUploadRequests
  : /(^|\/)owner-account-clients(?:\.ts)?$/.test(source) ? ownerAccountClients
  : /(^|\/)failure-words$/.test(source) ? failureWords
  : /(^|\/)platform\/tv-play-pause$/.test(source) ? tvPlayPause
  // The Play/Pause owner hook over the fixture's own React (TV focus step 6).
  : /(^|\/)platform\/tv-play-pause-owner$/.test(source) ? {useTVPlayPauseOwner: tvPlayPause.tvPlayPauseOwnerHook(react)}
  // The shell's cache accessor (shell/freshness) is pure: a screen built over a fixture's own API reads through a cache of that API.
  : /(^|\/)shell\/freshness$|^\.\/freshness$/.test(source) ? shellFreshness
  : /(^|\/)kit\/layout\/page-inset$/.test(source) ? pageInsetLeaf(react, native)
  : /(^|\/)navigation\/rail-hold$/.test(source) ? railHoldLeaf(react)
  : /(^|\/)focus\/replacement-refocus$/.test(source) ? {useReplacementRefocus: replacementRefocusHook(react)}
  : /(^|\/)kit\/focus\/CoveredLayer$/.test(source) ? {CoveredLayer: ({covered, children, style, testID}) => react.createElement('View',
    {style, testID, pointerEvents: covered ? 'none' : 'auto', accessibilityElementsHidden: covered, importantForAccessibility: covered ? 'no-hide-descendants' : 'auto'}, children)}
  // The one Back dispatcher (kit/navigation/back) forwards to the fixture's own BackHandler, gated by `active`, so a
  // fixture that drives `hardwareBackPress` keeps doing so; the scope order has its own tests.
  // TVRow (kit/layout/TVRow) leaves its scroller as the caller wrote it; the row rule has its own engine tests.
  : /(^|\/)TVRow$/.test(source) ? {TVRow: ({children}) => children}
  : /(^|\/)navigation\/back$/.test(source) ? {registerBack: (handler, options) => {
      const subscription = native?.BackHandler?.addEventListener('hardwareBackPress', () => (!options?.active || options.active()) && handler() === true);
      return () => subscription?.remove();
    }} : undefined;

// Keep a fixture Proxy's deliberately unsupported-port fallback when adding a namespace.
const withModules = (base, additions) => new Proxy(base, {
  get: (target, key, receiver) => Object.hasOwn(additions, key) ? additions[key] : Reflect.get(target, key, receiver),
  getOwnPropertyDescriptor: (target, key) => Object.hasOwn(additions, key)
    ? {value: additions[key], enumerable: true, configurable: true, writable: true}
    : Reflect.getOwnPropertyDescriptor(target, key),
});

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
  // Execute the real named core operations over the fixture's existing route and
  // installation doubles. The fixture's captured requester remains the authority.
  const connectionImports = parsed.program.body.filter(d => d.type === 'ImportDeclaration' && /client-core\/src\/query\/connection-operations(?:\.ts)?$/.test(d.source.value));
  if (connectionImports.length) {
    const operations = await screenModule(new URL('../../../../packages/client-core/src/query/connection-operations.ts', import.meta.url), {
      react: modules.react ?? {default: {}},
      '../index.ts': {...core, ...(modules['../../../../packages/client-core/src'] ?? modules['../../../../packages/client-core/src/index.ts'])},
      '../../../contracts/src/routes.ts': modules['../../../../packages/contracts/src/routes'] ?? connectionRoutes,
      '../../../contracts/src/hosted/routes.ts': modules['../../../../packages/contracts/src/hosted/routes'] ?? hostedConnectionRoutes,
      '../route-identity.ts': routeIdentity, '../server-clock.ts': serverClock, './server-api.ts': serverApi,
      '../installation.ts': modules['../../../../packages/client-core/src/installation'] ?? installation,
    });
    modules = withModules(modules, Object.fromEntries(connectionImports.map(d => [d.source.value, operations])));
  }
  const hostedImports = parsed.program.body.filter(d => d.type === 'ImportDeclaration' && /client-core\/src\/query\/hosted-account-transport(?:\.ts)?$/.test(d.source.value));
  if (hostedImports.length) {
    const transport = await screenModule(new URL('../../../../packages/client-core/src/query/hosted-account-transport.ts', import.meta.url), {
      react: modules.react ?? {default: {}},
      '../index.ts': {...core, ...(modules['../../../../packages/client-core/src'] ?? modules['../../../../packages/client-core/src/index.ts'])},
      '../account-notifications.ts': {...accountNotifications, ...modules['../../../../packages/client-core/src/account-notifications']},
      '../hosted-security.ts': {...hostedSecurity, ...modules['../../../../packages/client-core/src/hosted-security']},
      '../native-abort-signals.ts': nativeAbortSignals,
      './native-account-work.ts': {...nativeAccountWork, ...modules['./account-work']},
      './browser-account-api.ts': browserAccountApi,
    });
    // A fixture's credential double inherits the same configured authority that
    // its old HttpLocalApi constructor received; real credential owners supply it.
    const factories = Object.fromEntries(Object.entries(transport).map(([name, value]) => [name,
      /^create(?:Hosted|Native)|^requestHostedAccountExport$/.test(name) && typeof value === 'function' ? (owner, ...args) => {
        if (owner && typeof owner.authorityOrigin !== 'function') owner.authorityOrigin = () => modules['./account-authority']?.hostedUrl ?? 'https://hosted.test';
        return value(owner, ...args);
      } : value]));
    modules = withModules(modules, Object.fromEntries(hostedImports.map(d => [d.source.value, factories])));
  }
  let linked = transformed.code;
  for (const declaration of parsed.program.body.filter(node => node.type === 'ImportDeclaration').reverse()) {
    const source = JSON.stringify(declaration.source.value);
    // A fixture may intentionally provide virtual namespaces with a Proxy whose
    // unvisited commands throw. Resolve that namespace once, just as the old
    // destructuring did; an own-property check wrongly rejects such explicit ports.
    const namespace = `__screenModuleImport${declaration.start}`;
    const missing = declaration.specifiers.length ? `${namespace} == null`
      : `!Object.hasOwn(modules, ${source}) || ${namespace} == null`;
    const required = `const ${namespace} = Object.hasOwn(modules, ${source}) ? modules[${source}] : __leaf(${source}, modules.react?.default ?? modules.react, modules['react-native']) ?? modules[${source}]; if (${missing}) throw new Error(${JSON.stringify(`screenModule(${file.pathname}): no namespace for `)} + ${source});`;
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
  return compileFunction((/const React\s*=/.test(code) ? '' : 'const React = modules.react.default;\n') + code + '\nreturn {' + exported.join(',') + '};', ['modules', '__leaf'], {filename: file.pathname})(modules, leaf);
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
