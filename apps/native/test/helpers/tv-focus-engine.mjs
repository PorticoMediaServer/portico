// A fake native TV focus engine for unit tests (TV-FOCUS-REVIEW §5, step 1).
//
// Real app components render through the installed production Fabric reconciler. This file plays
// the native side: it lays the committed host tree out, keeps one focused host, and answers the
// D-pad, OK, Back and the imperative focus commands the way react-native-tvos 0.83.10-1 does on
// Android TV or tvOS. Every rule below cites the library source it models. Where the answer
// depends on device behaviour nobody has observed yet, it is a named assumption with the device
// check that must confirm it (TV-FOCUS-REVIEW §6, D1–D10). It is a model, not a device.
//
// Assumptions (override per engine with `assume`):
//   blockedFocusStays (Android, D1): an ancestor turning FOCUS_BLOCK_DESCENDANTS leaves focus where it is.
//   coverMovesFocus   (tvOS, D7):    an ancestor turning non-interactive moves focus to the root's preferred view.
//   propSettersRepeat (Android, D10): Fabric re-applies every prop on any update of a host, so `autoFocus`
//                                     re-runs setAutoFocusTV, which clears the guide's last-focused memory
//                                     (ReactViewManager.kt:170-173, ReactViewGroup.kt:1515-1518; the same repeat is
//                                     why setTVPreferredFocus guards itself, ReactViewManager.kt:150-160).
//
// Native `Modal` windows (refused-link page, 4 Nov). Always on; each is a device check (TV-FOCUS.md):
//   D12 (both):    a visible Modal is its own window (Android: a Dialog window that owns key focus,
//                  ReactModalHostView.kt; tvOS: a presented view controller whose focus environment excludes the
//                  presenting hierarchy, RCTModalHostView.mm). The last one shown is on top. D-pad moves, OK and the
//                  window's first focus see only the top window; Back (Android) and Menu (tvOS) call its
//                  `onRequestClose` and never reach BackHandler.
//   D13 (Android): a focus request in a lower window sets that window's focused view without key focus
//                  (View.requestFocus on an unfocused window); it is what that window shows again when the Modal
//                  closes (the hidden-picture risk the shell's `Covered` signal keeps away).
//   D14 (tvOS):    requests outside the top window move nothing (the focus engine only searches the focused
//                  environment) but still set the root's preferred view (focusSelf). `visible={false}` starts a
//                  dismissal: the Modal stays on screen and every request is ignored until it ends
//                  (`finishDismissals()`); then UIKit gives the presenting hierarchy back the view focused when the
//                  Modal opened (`restoresFocusAfterTransition`), else the root's preferred view, else its first,
//                  and only then RN fires `onDismiss`.
//   D15 (tvOS):    a Modal presented at cold start reaches the window and reports its dismissal. The page's bounded
//                  fallback (`useNativeDismissal`) covers the case where it never does.
//   D16 (tvOS):    Menu in a Modal is UIKit's own dismissal (RCTModalHostViewComponentView.mm:149-160): the window starts
//                  dismissing at once, and its completion (`finishDismissals()`) restores the presenting focus as in
//                  D14, then emits `onDismiss` and `onRequestClose`. They reach JS here as two batches (the worst case:
//                  the dismissal commits before the request). The view keeps `_isPresented`, so a later
//                  `visible={false}` has nothing left to dismiss and reports nothing.
// The top window is the one shown last (presentation order, not its place in the tree). An explicit nextFocus* that
// names a host in another window is ignored and the geometric search runs inside the top window.
import {readFile} from 'node:fs/promises';
import {createRequire} from 'node:module';
import {compileFunction} from 'node:vm';
import {dirname, join} from 'node:path';

const require = createRequire(import.meta.url);
export const React = require(join(dirname(require.resolve('react')), 'cjs/react.production.js'));

const DIRECTIONS = ['up', 'down', 'left', 'right'];
const NEXT = {up: 'nextFocusUp', down: 'nextFocusDown', left: 'nextFocusLeft', right: 'nextFocusRight'};
const TRAP = {up: 'trapFocusUp', down: 'trapFocusDown', left: 'trapFocusLeft', right: 'trapFocusRight'};
const flatten = style => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style && typeof style === 'object' ? style : {};

export async function tvFocusEngine({platform = 'android', width = 1920, height = 1080, assume = {}} = {}) {
  const android = platform === 'android';
  const assumptions = {blockedFocusStays: true, coverMovesFocus: true, propSettersRepeat: true, ...assume};
  const errors = [], log = [], backHandlers = [], keyListeners = new Set();
  const publicInstances = new Map();
  /** Per-host native state that outlives commits: guide destinations and memory, tvOS guide existence. */
  const state = new Map();
  const nativeOf = tag => {let value = state.get(tag); if (!value) state.set(tag, value = {destinations: [], last: undefined, guide: false, rect: undefined}); return value;};
  let root = [], nodes = new Map(), parents = new Map(), order = [], focused, rootPreferred, commands = [], events = [], scheduled = false;
  /** D12-D14: per Modal window, the focus it was opened over; per window (BASE for the app's own), its focused view;
   *  the tvOS dismissals in progress; the windows in the order they were shown (D12); tvOS windows Menu is dismissing or
   *  has dismissed natively while their Modal is still mounted (D16). */
  const BASE = 'app-window', presenting = new Map(), windowFocus = new Map(), dismissals = [], presentedOrder = [], menuDismissing = new Set(), menuGone = new Set();

  // ---------- host tree, layout ----------
  const rebuild = () => {
    const previous = nodes;
    nodes = new Map(); parents = new Map(); order = [];
    const visit = (node, parent) => {nodes.set(node.tag, node); parents.set(node.tag, parent); order.push(node.tag); node.children.forEach(child => visit(child, node.tag));};
    root.forEach(node => visit(node, undefined));
    layout();
    for (const [tag, node] of nodes) {
      const old = previous.get(tag), was = old?.props ?? {}, now = node.props;
      const native = nodeState(tag);
      // tvOS: updateLayoutMetrics runs on mount and on every layout change, and only then
      // (RCTMountingManager.mm:85-86, 126-130). It re-asserts a preferred view and re-runs handleFocusGuide.
      const moved = !old || !sameRect(native.rect, native.nextRect);
      native.rect = native.nextRect;
      if (android && old && was !== now && assumptions.propSettersRepeat && now.autoFocus) native.last = undefined;
      if (android && was.autoFocus !== now.autoFocus) native.last = undefined; // setAutoFocusTV clears memory
      if (!android && moved) {
        if (now.hasTVPreferredFocus && rootPreferred !== tag) request(tag, 'preferred-layout'); // RCTViewComponentView.mm:1267-1272
        handleFocusGuide(tag);
      }
      if (!was.hasTVPreferredFocus && now.hasTVPreferredFocus) request(tag, 'preferred'); // both: on change to true
    }
    for (const tag of previous.keys()) if (!nodes.has(tag)) state.delete(tag);
    const closed = [...previous.keys()].filter(tag => previous.get(tag).type === MODAL && !nodes.has(tag));
    if (closed.length && (focused === undefined || !nodes.has(focused))) closeWindows(closed);
    else if (focused !== undefined && !nodes.has(focused)) recoverRemoved(previous);
    else if (focused !== undefined && !reachable(focused)) recoverBlocked();
    for (const tag of closed) {presenting.delete(tag); windowFocus.delete(tag); menuDismissing.delete(tag); menuGone.delete(tag); const at = presentedOrder.indexOf(tag); if (at >= 0) presentedOrder.splice(at, 1);}
    for (const tag of order.filter(isWindow)) if (!previous.has(tag)) openWindow(tag);
  };
  // ---------- windows (D12-D15) ----------
  const MODAL = 'RCTModalHostView';
  const isWindow = tag => nodes.get(tag)?.type === MODAL;
  const windowOf = tag => tag === undefined ? undefined : [tag, ...ancestors(tag)].find(isWindow);
  const windows = () => presentedOrder.filter(tag => nodes.has(tag) && !menuGone.has(tag));
  const topWindow = () => windows().at(-1);
  const isDismissing = tag => props(tag).__dismissing === true || menuDismissing.has(tag);
  const dismissing = () => {const top = topWindow(); return top !== undefined && isDismissing(top);};
  const firstInTop = () => {const top = topWindow(); return top === undefined ? firstInTree() : firstFocusable(top);};
  const openWindow = tag => {
    // The window takes focus: its first focusable (a preferred control inside asks for itself as well).
    presenting.set(tag, focused); presentedOrder.push(tag);
    const first = firstFocusable(tag);
    if (first !== undefined) setFocus(first, 'window-open');
  };
  const closeWindows = closed => {
    const was = focused; focused = undefined;
    const top = topWindow(), inTop = tag => tag !== undefined && nodes.has(tag) && windowOf(tag) === top && reachable(tag);
    let next;
    if (android) {
      // D13: the window under it gets key focus back on its own focused view.
      const kept = windowFocus.get(top ?? BASE);
      next = inTop(kept) ? kept : firstInTop();
    } else {
      // D14: restoresFocusAfterTransition, else the root's preferred view, else the first.
      const opened = closed.map(tag => presenting.get(tag)).find(inTop);
      next = opened ?? (inTop(rootPreferred) ? resolve(rootPreferred) : undefined) ?? firstInTop();
    }
    log.push({why: 'window-closed', from: was});
    if (next !== undefined) setFocus(next, 'window-close');
  };
  const nodeState = tag => nativeOf(tag);
  const sameRect = (a, b) => !!a && !!b && a.x === b.x && a.y === b.y && a.w === b.w && a.h === b.h;
  const layout = () => {
    const measure = node => {
      const s = flatten(node.props.style);
      if (s.display === 'none') return {w: 0, h: 0};
      const row = s.flexDirection === 'row', gap = s.gap ?? 0;
      const pad = padding(s);
      const flow = node.children.filter(child => flatten(child.props.style).position !== 'absolute');
      const sizes = flow.map(measure);
      const main = sizes.reduce((sum, size) => sum + (row ? size.w : size.h), 0) + gap * Math.max(0, sizes.length - 1);
      const cross = sizes.reduce((max, size) => Math.max(max, row ? size.h : size.w), 0);
      return {w: s.width ?? (row ? main : cross) + pad.l + pad.r, h: s.height ?? (row ? cross : main) + pad.t + pad.b};
    };
    const place = (node, x, y, w, h) => {
      const s = flatten(node.props.style);
      nodeState(node.tag).nextRect = {x, y, w, h};
      if (s.display === 'none') {node.children.forEach(child => place(child, x, y, 0, 0)); return;}
      const row = s.flexDirection === 'row', gap = s.gap ?? 0, pad = padding(s);
      const inner = {x: x + pad.l, y: y + pad.t, w: Math.max(0, w - pad.l - pad.r), h: Math.max(0, h - pad.t - pad.b)};
      const flow = node.children.filter(child => flatten(child.props.style).position !== 'absolute');
      const sizes = flow.map(measure);
      const fixed = flow.reduce((sum, child, index) => sum + (flatten(child.props.style).flex ? 0 : row ? sizes[index].w : sizes[index].h), 0) + gap * Math.max(0, flow.length - 1);
      const flexTotal = flow.reduce((sum, child) => sum + (flatten(child.props.style).flex ?? 0), 0);
      const room = Math.max(0, (row ? inner.w : inner.h) - fixed);
      const scroll = node.type === 'RCTScrollView' ? nodeState(node.tag).scroll ?? 0 : 0;
      let at = (row ? inner.x : inner.y) - scroll;
      flow.forEach((child, index) => {
        const cs = flatten(child.props.style), flex = cs.flex ?? 0;
        const mainSize = flex ? room * flex / flexTotal : row ? sizes[index].w : sizes[index].h;
        const crossSize = (row ? cs.height : cs.width) ?? (row ? inner.h : inner.w);
        if (row) place(child, at, inner.y, mainSize, crossSize); else place(child, inner.x, at, crossSize, mainSize);
        at += mainSize + gap;
      });
      for (const child of node.children.filter(child => flatten(child.props.style).position === 'absolute')) {
        const cs = flatten(child.props.style), inset = cs.inset ?? undefined;
        const left = cs.left ?? inset, right = cs.right ?? inset, top = cs.top ?? inset, bottom = cs.bottom ?? inset;
        const size = measure(child);
        const cw = cs.width ?? (left !== undefined && right !== undefined ? w - left - right : size.w);
        const ch = cs.height ?? (top !== undefined && bottom !== undefined ? h - top - bottom : size.h);
        place(child, x + (left ?? (right !== undefined ? w - right - cw : 0)), y + (top ?? (bottom !== undefined ? h - bottom - ch : 0)), cw, ch);
      }
    };
    root.forEach(node => place(node, 0, 0, width, height));
  };
  const padding = s => ({l: s.paddingLeft ?? s.paddingHorizontal ?? s.padding ?? 0, r: s.paddingRight ?? s.paddingHorizontal ?? s.padding ?? 0,
    t: s.paddingTop ?? s.paddingVertical ?? s.padding ?? 0, b: s.paddingBottom ?? s.paddingVertical ?? s.padding ?? 0});
  const rect = tag => nodeState(tag).rect ?? {x: 0, y: 0, w: 0, h: 0};
  const ancestors = tag => {const out = []; for (let at = parents.get(tag); at !== undefined; at = parents.get(at)) out.push(at); return out;};
  const props = tag => nodes.get(tag)?.props ?? {};
  const hidden = tag => [tag, ...ancestors(tag)].some(at => flatten(props(at).style).display === 'none');

  // ---------- what can hold focus ----------
  /** Android: a guide is a TV focus guide while it has destinations or autoFocus (ReactViewGroup.kt:1314-1323). */
  const isGuide = tag => props(tag).__guide === true && (android ? nodeState(tag).destinations.length > 0 || !!props(tag).autoFocus : nodeState(tag).guide);
  /** Android FOCUS_BLOCK_DESCENDANTS (ReactViewManager.kt:113-121); tvOS: a guide's isUserInteractionEnabled is its
   * isTVSelectable (RCTViewComponentView.mm:463-469), which TVFocusGuideView.js:117-120 derives. */
  const blocks = tag => {
    const p = props(tag);
    if (p.__guide !== true) return p.pointerEvents === 'none' && !android;
    if (android) return p.tvFocusable === false;
    return nodeState(tag).guide && p.isTVSelectable !== true;
  };
  const scrollerFocusable = tag => android && nodes.get(tag)?.type === 'RCTScrollView' && props(tag).horizontal && props(tag).scrollEnabled !== false;
  const leaf = tag => {
    const node = nodes.get(tag);
    if (!node || node.props.__guide) return false;
    if (node.type === 'RCTScrollView') return false; // handled as a group below
    return node.props.focusable === true && !node.props.disabled;
  };
  const reachable = tag => !hidden(tag) && !ancestors(tag).some(blocks) && (leaf(tag) || isGuide(tag) && !blocks(tag) || scrollerFocusable(tag));
  /** ViewGroup.addFocusables: blocked groups add nothing; a guide with no focused child adds itself instead of its
   * children (ReactViewGroup.kt:1366-1382); FOCUS_AFTER_DESCENDANTS groups (a focusable scroller) add themselves only
   * when no descendant was added. */
  const collect = (tag, out, within = focused) => {
    const node = nodes.get(tag);
    if (!node || flatten(node.props.style).display === 'none') return;
    if (node.type === MODAL) return; // its own window (D12)
    if (blocks(tag)) return;
    if (isGuide(tag) && !(within !== undefined && ancestors(within).includes(tag))) {
      if (android || hasFocusableInside(tag)) {out.push(tag); return;}
    }
    if (leaf(tag)) out.push(tag);
    const before = out.length;
    node.children.forEach(child => collect(child.tag, out, within));
    if (scrollerFocusable(tag) && out.length === before) out.push(tag);
  };
  const hasFocusableInside = tag => {const out = []; nodes.get(tag)?.children.forEach(child => collect(child.tag, out, undefined)); return out.length > 0;};
  const firstFocusable = tag => {
    const out = [];
    nodes.get(tag)?.children.forEach(child => collect(child.tag, out, undefined));
    for (const candidate of out) {const landed = resolve(candidate); if (landed !== undefined) return landed;}
    return undefined;
  };
  /** A guide redirects: destinations, then (autoFocus) the last focused element, then its first focusable
   * (Android requestFocus, ReactViewGroup.kt:1445-1487; tvOS handleFocusGuide, RCTViewComponentView.mm:251-266). */
  const resolve = (tag, seen = new Set()) => {
    if (tag === undefined || seen.has(tag) || !nodes.has(tag)) return undefined;
    seen.add(tag);
    if (!isGuide(tag)) return leaf(tag) || scrollerFocusable(tag) ? (reachable(tag) ? tag : undefined) : firstFocusable(tag);
    if (blocks(tag) || ancestors(tag).some(blocks)) return undefined;
    const native = nodeState(tag);
    if (native.destinations.length) {
      const destination = native.destinations.find(at => nodes.has(at));
      if (android && destination === undefined) return undefined; // "Destination is set but there's no such element"
      const landed = resolve(destination, seen);
      if (landed !== undefined || android) return landed;
    }
    if (props(tag).autoFocus) {
      if (native.last !== undefined && nodes.has(native.last) && reachable(native.last)) return native.last;
      return firstFocusable(tag);
    }
    return firstFocusable(tag);
  };

  // ---------- moving focus ----------
  const fire = (tag, name, ...args) => {const handler = props(tag)[name]; if (typeof handler === 'function') handler(...args);};
  const setFocus = (next, why) => {
    if (next === focused) return;
    const previous = focused;
    focused = next;
    if (next !== undefined) windowFocus.set(windowOf(next) ?? BASE, next);
    log.push({why, from: previous, to: next});
    if (next !== undefined) for (const at of ancestors(next)) if (props(at).__guide && props(at).autoFocus) nodeState(at).last = next; // requestChildFocus
    // Native focus changes reach JS as events, after the current JS work (the commit and its layout effects).
    if (previous !== undefined) events.push({tag: previous, name: 'onBlur'});
    if (next !== undefined) events.push({tag: next, name: 'onFocus'});
    schedule();
  };
  const request = (tag, why = 'request') => {commands.push({tag, why}); schedule();};
  const runRequest = ({tag, why}) => {
    if (!nodes.has(tag)) return;
    if (!android && dismissing()) {log.push({why: `${why}-ignored-dismissing`, target: tag}); return;} // D14
    if (!android) rootPreferred = tag; // focusSelf sets the root's preferred view (RCTViewComponentView.mm:198-211)
    const target = resolve(tag);
    if (target === undefined || !reachable(target)) return;
    const window = windowOf(target);
    if (window !== topWindow()) {
      // D13: Android keeps it as that window's focused view; D14: tvOS moves nothing.
      if (android) windowFocus.set(window ?? BASE, target);
      log.push({why: `${why}-lower-window`, target});
      return;
    }
    setFocus(target, why);
  };
  const recoverRemoved = previousNodes => {
    // Android recoverFocus: the nearest parent focus guide still attached takes focus, then its first focusable
    // (ReactViewGroup.kt:646-731). With no guide, the window gives focus to the first focusable view.
    // tvOS: the focus engine asks the root's preferred environment.
    const was = focused; focused = undefined;
    log.push({why: 'removed', from: was});
    if (android) {
      const chain = [];
      for (let at = parentOfRemoved(was, previousNodes); at !== undefined; at = parentOfRemoved(at, previousNodes)) chain.push(at);
      const guide = chain.find(at => nodes.has(at) && isGuide(at) && !blocks(at) && !ancestors(at).some(blocks));
      if (guide !== undefined) {const landed = firstFocusable(guide); setFocus(landed ?? guide, 'recover'); return;}
      setFocus(firstInTop(), 'recover-window');
      return;
    }
    setFocus(preferredOrFirst(), 'recover-preferred');
  };
  const removedParents = new Map();
  const parentOfRemoved = (tag, previousNodes) => {
    if (parents.has(tag)) return parents.get(tag);
    return removedParents.get(tag);
  };
  const recoverBlocked = () => {
    if (android && assumptions.blockedFocusStays) return; // D1: focus stays on the blocked control
    if (!android && !assumptions.coverMovesFocus) return;
    const was = focused; focused = undefined; log.push({why: 'blocked', from: was});
    setFocus(preferredOrFirst(), 'recover-blocked');
  };
  const firstInTree = () => {const out = []; root.forEach(node => collect(node.tag, out, undefined)); for (const tag of out) {const landed = resolve(tag); if (landed !== undefined) return landed;} return undefined;};
  const preferredOrFirst = () => (rootPreferred !== undefined && nodes.has(rootPreferred) && windowOf(rootPreferred) === topWindow() ? resolve(rootPreferred) : undefined) ?? firstInTop();
  const handleFocusGuide = tag => {
    const native = nodeState(tag), p = props(tag);
    if (p.__guide !== true) return;
    native.guide = native.destinations.length > 0 || !!p.autoFocus;
  };

  /** Android FocusFinder, simplified to its beam rule and weighted distance (13·major² + minor²). */
  const search = direction => {
    if (focused === undefined || windowOf(focused) !== topWindow()) return firstInTop();
    const trap = ancestors(focused).find(at => props(at)[TRAP[direction]] && !blocks(at));
    const scope = trap ?? undefined;
    const user = props(focused)[NEXT[direction]];
    if (user !== undefined && user !== null && nodes.has(user) && (scope === undefined || ancestors(user).includes(scope) || user === scope)) {
      const landed = resolve(user);
      if (landed !== undefined && windowOf(landed) === topWindow()) return landed; // never across a window
    }
    const candidates = [];
    const within = scope ?? topWindow();
    if (within !== undefined) nodes.get(within).children.forEach(child => collect(child.tag, candidates));
    else root.forEach(node => collect(node.tag, candidates));
    const from = rect(focused);
    let best, bestScore = Infinity;
    for (const tag of candidates) {
      if (tag === focused || ancestors(focused).includes(tag)) continue;
      const to = rect(tag);
      if (!to.w && !to.h) continue;
      if (!inDirection(from, to, direction)) continue;
      const score = 13 * major(from, to, direction) ** 2 + minor(from, to, direction) ** 2;
      if (best === undefined || better(from, to, rect(best), direction, score, bestScore)) {best = tag; bestScore = score;}
    }
    if (best === undefined) return undefined;
    return resolve(best);
  };
  /** FocusFinder.isCandidate: the destination's far edge is beyond the source's, and its near edge is not behind it. */
  const inDirection = (a, b, d) => d === 'right' ? (a.x < b.x || a.x + a.w <= b.x) && a.x + a.w < b.x + b.w
    : d === 'left' ? (a.x + a.w > b.x + b.w || a.x >= b.x + b.w) && a.x > b.x
    : d === 'down' ? (a.y < b.y || a.y + a.h <= b.y) && a.y + a.h < b.y + b.h
    : (a.y + a.h > b.y + b.h || a.y >= b.y + b.h) && a.y > b.y;
  /** FocusFinder.isBetterCandidate with beamBeats: a candidate in the source's beam beats one outside it (always for
   * Left/Right; for Up/Down when it is nearer than the other's far edge); otherwise the lower weighted distance wins. */
  const better = (from, a, b, d, scoreA, scoreB) => {
    const beamA = inBeam(from, a, d), beamB = inBeam(from, b, d);
    if (beamA && !beamB) return d === 'left' || d === 'right' || major(from, a, d) < farEdge(from, b, d);
    if (beamB && !beamA) return !(d === 'left' || d === 'right' || major(from, b, d) < farEdge(from, a, d));
    return scoreA < scoreB;
  };
  const farEdge = (a, b, d) => Math.max(1, d === 'right' ? b.x + b.w - (a.x + a.w) : d === 'left' ? a.x - b.x : d === 'down' ? b.y + b.h - (a.y + a.h) : a.y - b.y);
  const inBeam = (a, b, d) => d === 'left' || d === 'right' ? b.y < a.y + a.h && b.y + b.h > a.y : b.x < a.x + a.w && b.x + b.w > a.x;
  const major = (a, b, d) => Math.max(0, d === 'right' ? b.x - (a.x + a.w) : d === 'left' ? a.x - (b.x + b.w) : d === 'down' ? b.y - (a.y + a.h) : a.y - (b.y + b.h));
  const minor = (a, b, d) => d === 'left' || d === 'right' ? (a.y + a.h / 2) - (b.y + b.h / 2) : (a.x + a.w / 2) - (b.x + b.w / 2);

  // ---------- scheduling ----------
  const schedule = () => {if (scheduled) return; scheduled = true; setImmediate(drain);};
  const drain = () => {
    scheduled = false;
    const delivered = events; events = [];
    for (const {tag, name} of delivered) if (nodes.has(tag)) fire(tag, name, {nativeEvent: {target: tag}});
    const batch = commands; commands = []; for (const command of batch) runRequest(command);
  };
  const settle = async () => {
    for (let n = 0; n < 40; n++) {
      await new Promise(resolve => setImmediate(resolve));
      if (!scheduled && !commands.length && !events.length && n > 4) {await new Promise(resolve => setImmediate(resolve)); if (!scheduled && !commands.length && !events.length) break;}
    }
    if (errors.length) throw errors[0];
  };

  // ---------- the fake react-native module ----------
  const instance = tag => {
    let value = publicInstances.get(tag);
    if (!value) publicInstances.set(tag, value = {
      tag, _nativeTag: tag,
      requestTVFocus: () => request(tag),
      focus: () => request(tag),
      setDestinations: destinations => {
        // Native command: setFocusDestinations re-runs handleFocusGuide on tvOS (RCTViewComponentView.mm:268-277).
        const native = nodeState(tag);
        native.destinations = (destinations ?? []).map(at => typeof at === 'number' ? at : at?.tag).filter(at => at !== undefined && at !== null);
        if (!android) handleFocusGuide(tag);
        if (focused !== undefined && !reachable(focused)) recoverBlocked();
      },
      measureInWindow: callback => {const r = rect(tag); callback(r.x, r.y, r.w, r.h);},
      measure: callback => {const r = rect(tag); callback(0, 0, r.w, r.h, r.x, r.y);},
    });
    return value;
  };
  const e = React.createElement;
  const View = React.forwardRef(function View(props, ref) {return e('RCTView', {...props, ref});});
  const Text = React.forwardRef(function Text(props, ref) {return e('RCTText', {...props, ref});});
  const Pressable = React.forwardRef(function Pressable({children, style, focusable, ...props}, ref) {
    const pressState = {pressed: false, focused: false, hovered: false};
    return e('RCTView', {...props, ref, focusable: focusable !== false, style: typeof style === 'function' ? style(pressState) : style},
      typeof children === 'function' ? children(pressState) : children);
  });
  /** react-native-tvos Libraries/Components/TV/TVFocusGuideView.js:95-133, host side only. */
  const TVFocusGuideView = React.forwardRef(function TVFocusGuideView({destinations, autoFocus, focusable, enabled = true, style, children, ...props}, ref) {
    const local = React.useRef(null);
    React.useImperativeHandle(ref, () => local.current, []);
    React.useEffect(() => {
      if (focusable === false) local.current?.setDestinations([]);
      else if (destinations != null) local.current?.setDestinations(destinations);
    }, [destinations, focusable]);
    const tvOSSelectable = destinations || autoFocus ? focusable !== false : false;
    return e('RCTView', {...props, ref: local, __guide: true, style: [style, {display: enabled ? 'flex' : 'none'}],
      autoFocus: focusable === false ? true : autoFocus, isTVSelectable: tvOSSelectable, tvFocusable: focusable}, children);
  });
  const ScrollView = React.forwardRef(function ScrollView({children, contentContainerStyle, horizontal, style, ...props}, ref) {
    return e('RCTScrollView', {...props, horizontal, ref, style: [{flexDirection: horizontal ? 'row' : 'column'}, style]},
      e('RCTView', {style: [{flexDirection: horizontal ? 'row' : 'column'}, contentContainerStyle]}, children));
  });
  /** react-native Modal.js with the native side above (D12-D14): a visible Modal is one RCTModalHostView window; on
   *  tvOS `visible={false}` keeps it on screen, dismissing, until `finishDismissals()`. */
  const Modal = function Modal({visible = true, onDismiss, onRequestClose, onShow: _onShow, children, ...rest}) {
    const [shown, setShown] = React.useState(visible), latest = React.useRef(onDismiss), host = React.useRef(null);
    latest.current = onDismiss;
    if (visible && !shown) setShown(true);
    const leaving = !android && shown && !visible;
    React.useEffect(() => {
      if (!leaving || menuGone.has(host.current?.tag)) return; // D16: Menu already dismissed it; nothing is reported
      const entry = {finish: () => setShown(false), onDismiss: () => latest.current?.()};
      dismissals.push(entry);
      return () => {const index = dismissals.indexOf(entry); if (index >= 0) dismissals.splice(index, 1);};
    }, [leaving]);
    if (!shown || android && !visible) return null;
    return e(MODAL, {...rest, ref: host, onDismiss, __dismissing: leaving, onRequestClose, style: {position: 'absolute', inset: 0}}, children);
  };
  const BackHandler = {addEventListener: (name, handler) => {
    if (name !== 'hardwareBackPress') return {remove() {}};
    const entry = {handler}; backHandlers.push(entry);
    return {remove: () => {const index = backHandlers.indexOf(entry); if (index >= 0) backHandlers.splice(index, 1);}};
  }};
  const TVEventHandler = {addListener: listener => {keyListeners.add(listener); return {remove: () => keyListeners.delete(listener)};}};
  const native = {
    View, Text, Pressable, TVFocusGuideView, ScrollView, Modal, Image: () => null,
    Animated: {View, createAnimatedComponent: c => c},
    Platform: {OS: android ? 'android' : 'ios', isTV: true, Version: 35, select: options => options[android ? 'android' : 'ios'] ?? options.default},
    StyleSheet: {flatten, create: value => value, absoluteFill: {position: 'absolute', inset: 0}, hairlineWidth: 1},
    BackHandler, TVEventHandler,
    useTVEventHandler: listener => React.useEffect(() => TVEventHandler.addListener(listener).remove, [listener]),
    findNodeHandle: host => typeof host === 'number' ? host : host?.tag ?? null,
    AccessibilityInfo: {addEventListener: () => ({remove() {}}), isScreenReaderEnabled: async () => false},
  };
  const animated = {default: {View, createAnimatedComponent: c => c}, useSharedValue: value => React.useRef({value}).current,
    useAnimatedStyle: read => read(), withTiming: value => value, withSpring: value => value, cancelAnimation() {}};

  // ---------- the reconciler ----------
  const attributes = p => Object.fromEntries(Object.entries(p).filter(([key]) => key !== 'children' && key !== 'ref'));
  const privateInterface = {
    ReactNativeViewConfigRegistry: {get: name => ({uiViewClassName: name, validAttributes: {}})},
    createAttributePayload: attributes,
    diffAttributePayloads: (oldProps, p) => Object.keys({...oldProps, ...p}).some(key => !Object.is(oldProps[key], p[key])) ? attributes(p) : null,
    createPublicRootInstance: () => ({}), createPublicInstance: tag => instance(tag), createPublicTextInstance: tag => ({tag}),
    RawEventEmitter: {emit() {}}, ReactFiberErrorDialog: {showErrorDialog: () => true},
  };
  const fabric = {
    createNode: (tag, type, _root, p) => ({tag, type, props: p, children: []}),
    cloneNodeWithNewChildren: node => ({...node, children: []}),
    cloneNodeWithNewChildrenAndProps: (node, p) => ({...node, props: {...node.props, ...p}, children: []}),
    cloneNodeWithNewProps: (node, p) => ({...node, props: {...node.props, ...p}}),
    appendChild: (node, child) => node.children.push(child), createChildSet: () => [], appendChildToSet: (set, node) => set.push(node),
    completeRoot: (_tag, next) => {
      for (const [tag, parent] of parents) removedParents.set(tag, parent);
      root = next; rebuild();
    },
    unstable_getCurrentEventPriority: () => 32,
  };
  const renderer = {};
  const path = require.resolve('react-native/Libraries/Renderer/implementations/ReactFabric-prod.js');
  compileFunction(await readFile(path, 'utf8'), ['require', 'exports', 'nativeFabricUIManager'])(name =>
    name.endsWith('ReactNativePrivateInitializeCore') ? {} : name.endsWith('ReactNativePrivateInterface') ? privateInterface : name === 'react' ? React : require(name), renderer, fabric);
  const options = {onUncaughtError: error => errors.push(error), onCaughtError: error => errors.push(error), onRecoverableError: error => errors.push(error)};
  let mounted = false;

  const byTestID = testID => order.find(tag => props(tag).testID === testID);
  const engine = {
    platform, assumptions, native, animated, React, log,
    async render(element) {mounted = true; renderer.render(element, 11, undefined, true, options); await settle();},
    async unmount() {if (!mounted) return; mounted = false; renderer.stopSurface(11); await settle();},
    settle,
    /** The focused host's testID (undefined when nothing has focus, '#<tag>' for an unnamed host). */
    get focused() {return focused === undefined ? undefined : props(focused).testID ?? `#${focused}`;},
    get focusedTag() {return focused;},
    tag: byTestID,
    rect: testID => rect(byTestID(testID)),
    has: testID => byTestID(testID) !== undefined,
    /** Whether a host could take focus from the D-pad right now. */
    focusable: testID => {const tag = byTestID(testID); return tag !== undefined && reachable(tag) && windowOf(tag) === topWindow() && !dismissing();},
    /** The Modal windows bottom first (D12): each one's testID and whether it is dismissing (tvOS). */
    windows: () => windows().map(tag => ({testID: props(tag).testID, dismissing: isDismissing(tag)})),
    /** Each Modal window's props as the app passed them (transparent, animationType, ...). */
    windowProps: () => windows().map(tag => props(tag)),
    /** Android (D13): the focused view a window keeps, by the testID of the Modal (undefined: the app's window). */
    windowFocus: testID => {const tag = windowFocus.get(testID === undefined ? BASE : byTestID(testID)); return tag === undefined ? undefined : props(tag).testID ?? `#${tag}`;},
    /** tvOS (D14, D16): UIKit finishes every dismissal in progress, then RN fires each Modal's onDismiss (and, for
     *  Menu, onRequestClose in a later batch). */
    async finishDismissals() {
      const batch = dismissals.splice(0);
      for (const entry of batch) entry.finish();
      await settle();
      for (const entry of batch) entry.onDismiss();
      await settle();
      for (const entry of batch) entry.then?.();
      await settle();
    },
    /** Put focus on a host as a user would have left it there (a starting state for a sequence). */
    async focus(testID) {const tag = byTestID(testID); if (tag === undefined) throw new Error(`no host ${testID}`); const target = resolve(tag); if (target === undefined) throw new Error(`${testID} cannot take focus`); setFocus(target, 'test'); await settle();},
    async press(key, {repeat = 1} = {}) {
      for (let n = 0; n < repeat; n++) {
        for (const listener of [...keyListeners]) listener({eventType: key, eventKeyAction: 0, tag: focused});
        if (!android && dismissing()) {log.push({why: `${key}-ignored-dismissing`}); await settle(); continue;}
        const top = topWindow();
        if (DIRECTIONS.includes(key)) {const next = search(key); if (next !== undefined) setFocus(next, key);}
        else if (key === 'select') {if (focused !== undefined) fire(focused, 'onPress', {nativeEvent: {}});}
        else if (key === 'longSelect') {if (focused !== undefined) fire(focused, 'onLongPress', {nativeEvent: {}});}
        else if (top !== undefined && key === 'menu' && !android) {
          // D16: UIKit dismisses the window itself; onDismiss then onRequestClose come from its completion.
          menuDismissing.add(top);
          dismissals.push({
            finish: () => {menuDismissing.delete(top); menuGone.add(top); closeWindows([top]);},
            onDismiss: () => {if (nodes.has(top)) fire(top, 'onDismiss');},
            then: () => {if (nodes.has(top)) fire(top, 'onRequestClose');},
          });
          log.push({why: 'back', handled: true, window: true, native: true});
        }
        else if (top !== undefined && key === 'back') {
          // D12: the Modal's window takes Back; BackHandler never sees it.
          fire(top, 'onRequestClose');
          log.push({why: 'back', handled: true, window: true});
        }
        else if (key === 'back' || key === 'menu' && !android) {
          // BackHandler: last registered first; the first true stops it (BackHandler.android.js:22-30, BackHandler.ios.js:73-112).
          let handled = false;
          for (const entry of [...backHandlers].reverse()) if (entry.handler()) {handled = true; break;}
          log.push({why: 'back', handled});
        }
        await settle();
      }
    },
    /** Scroll a scroller by an offset (content moves, native focus does not). */
    async scroll(testID, offset) {nodeState(byTestID(testID)).scroll = offset; layout(); await settle();},
    backHandlers: () => backHandlers.length,
  };
  return engine;
}
