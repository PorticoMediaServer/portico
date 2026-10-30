import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {Children, createElement, isValidElement, type ReactNode} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from '../transform-tsx.mjs';
import {PlaybackDock, type DockPlayback, type QueuePlaybackFence} from '../../../packages/player-core/src/index.ts';
import type {WebPlaybackRuntimeSnapshot} from '../../src/platform/playback/types.ts';
import type {ShellPlaybackBinding} from '../../src/shell/Playback.tsx';
import type {NowPlayingCardProps} from '../../src/kit/dock/NowPlayingCard.tsx';
import {dockView} from '../../src/kit/dock/model.ts';

type Effect = {deps?: readonly unknown[]; cleanup?: () => void; phase: 'layout' | 'passive'};
const changed = (a: readonly unknown[] | undefined, b: readonly unknown[] | undefined) => !a || !b || a.length !== b.length || a.some((value, index) => !Object.is(value, b[index]));
const bridge = {current: undefined as HookFrame | undefined, router: {state: {location: {pathname: '/', href: '/'}}, navigate: async () => {}, subscribe: () => () => {}}};
const bridgeKey = '__porticoDockPlaybackHooks';
(globalThis as Record<string, unknown>)[bridgeKey] = bridge;
const reactModule = import.meta.resolve('react');
// Bind the real React namespace. A hook called while this frame is rendering uses the frame's
// lifecycle; the same module under the real renderer (the MediaSurface check below) uses React's.
const hookModule = 'data:text/javascript,' + encodeURIComponent(`import * as real from ${JSON.stringify(reactModule)};export * from ${JSON.stringify(reactModule)};
const bridge = globalThis.${bridgeKey};
${['useRef', 'useMemo', 'useCallback', 'useState', 'useEffect', 'useLayoutEffect', 'useSyncExternalStore', 'useId', 'useContext'].map(name => `export const ${name} = (...args) => bridge.current ? bridge.current.${name}(...args) : real.${name}(...args);`).join('\n')}`);

// A deterministic hook-lifecycle harness for the real component functions:
// dependency equality, commit refs, cleanup-before-setup and bounded rerenders.
// It does not stand in for React DOM, browser focus, media or native evidence.
class HookFrame {
  private slots: any[] = [];
  private cursor = 0;
  private dirty = false;
  private queued: {effect: Effect; run: () => void | (() => void)}[] = [];
  private nodes = new Map<{current: unknown}, FocusNode>();
  renders = 0;
  useRef(value: unknown) { const i = this.cursor++; return this.slots[i] ??= {current: value}; }
  useId() { return this.useMemo(() => 'focus-dialog', []); }
  useMemo(make: () => unknown, deps?: readonly unknown[]) {
    const i = this.cursor++, held = this.slots[i];
    if (!held || changed(held.deps, deps)) this.slots[i] = {value: make(), deps};
    return this.slots[i].value;
  }
  useCallback(callback: unknown, deps?: readonly unknown[]) { return this.useMemo(() => callback, deps); }
  useState(initial: unknown) {
    const i = this.cursor++;
    this.slots[i] ??= {value: typeof initial === 'function' ? initial() : initial};
    return [this.slots[i].value, (next: any) => {
      const value = typeof next === 'function' ? next(this.slots[i].value) : next;
      if (!Object.is(value, this.slots[i].value)) { this.slots[i].value = value; this.dirty = true; }
    }];
  }
  useSyncExternalStore(_subscribe: unknown, getSnapshot: () => unknown) { this.cursor++; return getSnapshot(); }
  useContext(context: {value: unknown}) { this.cursor++; return context.value; }
  useLayoutEffect(run: () => void | (() => void), deps?: readonly unknown[]) { this.effect('layout', run, deps); }
  useEffect(run: () => void | (() => void), deps?: readonly unknown[]) { this.effect('passive', run, deps); }
  private effect(phase: Effect['phase'], run: () => void | (() => void), deps?: readonly unknown[]) {
    const i = this.cursor++, previous: Effect | undefined = this.slots[i];
    if (!previous || changed(previous.deps, deps)) {
      const effect: Effect = {phase, deps, cleanup: previous?.cleanup}; this.slots[i] = effect; this.queued.push({effect, run});
    }
  }
  render(component: () => ReactNode): ReactNode {
    for (let attempts = 0; attempts < 8; attempts++) {
      bridge.current = this; this.cursor = 0; this.queued = []; this.dirty = false; this.renders++;
      const result = component(), seen = new Set<{current: unknown}>();
      const commit = (children: ReactNode, parent?: FocusNode) => Children.forEach(children, child => {
        if (!isValidElement<any>(child)) return;
        const ref = child.props.ref ?? child.props.buttonRef;
        let node = parent;
        if (ref) {
          node = this.nodes.get(ref);
          if (!node || !node.isConnected) { node = new FocusNode(); this.nodes.set(ref, node); }
          ref.current = node; node.parent = parent; seen.add(ref);
        }
        commit(child.props.children, node);
      });
      commit(result);
      for (const [ref, node] of this.nodes) if (!seen.has(ref)) { ref.current = null; node.isConnected = false; }
      for (const phase of ['layout', 'passive']) {
        const pending = this.queued.filter(job => job.effect.phase === phase);
        pending.forEach(job => job.effect.cleanup?.());
        pending.forEach(job => { job.effect.cleanup = job.run() ?? undefined; });
      }
      if (!this.dirty) { bridge.current = undefined; return result; }
    }
    bridge.current = undefined;
    throw new Error('Hook effects did not settle within eight renders');
  }
  unmount() { for (const slot of this.slots) if (slot?.cleanup) { slot.cleanup(); slot.cleanup = undefined; } }
}
class FocusNode {
  isConnected = true;
  parent?: FocusNode;
  calls = 0;
  contains(other: FocusNode) { for (let at: FocusNode | undefined = other; at; at = at.parent) if (at === this) return true; return false; }
  focus() { this.calls++; page.activeElement = this; page.emit(this); }
  addEventListener(_name: string, _listener: unknown) {}
  removeEventListener(_name: string, _listener: unknown) {}
}
const listeners = new Set<(event: {target: FocusNode}) => void>();
const page = {activeElement: null as FocusNode | null, addEventListener: (_name: string, fn: any) => listeners.add(fn), removeEventListener: (_name: string, fn: any) => listeners.delete(fn), emit: (target: FocusNode) => listeners.forEach(fn => fn({target}))};
const loader = registerHooks({load(url, context, next) {
  const module = (source: string) => ({format: 'module' as const, source, shortCircuit: true});
  if (url.endsWith('.module.css')) return module('export default new Proxy({}, {get: (_, key) => String(key)});');
  if (url.endsWith('/src/router.tsx')) return module(`export const router = globalThis.${bridgeKey}.router;`);
  if (url.endsWith('/src/shell/viewport.ts')) return module('export const useShellWidth = () => 1440;');
  if (url.endsWith('/kit/dock/index.ts')) return module("export const DockHost = 'dock-host', DockNotice = 'dock-notice', MediaSurface = 'media-surface';");
  if (url.endsWith('/kit/controls/index.ts')) return module("export const Button = 'button', ResumeButton = 'resume-button', Chip = 'chip', Tabs = 'tabs', TabsParts = {Root: 'tabs-root', List: 'tabs-list', Trigger: 'tabs-trigger', Content: 'tabs-content'}, Segmented = 'segmented', RadioGroup = 'radio-group', RadioItem = 'radio-item', Switch = 'switch', TextField = 'text-field', Select = 'select', RichSelect = 'rich-select', Slider = 'slider';");
  if (url.endsWith('/kit/artwork/index.ts')) return module("export const Artwork = 'artwork', AmbientArt = 'ambient-art', ColourBackdrop = 'colour-backdrop', GeneratedArt = 'generated-art', GeneratedCover = 'generated-cover', CircleArt = 'circle-art', ArtistCircle = 'artist-circle', PersonCircle = 'person-circle', CoverMosaic = 'cover-mosaic', ChannelMark = 'channel-mark', ProfileFace = 'profile-face', ProfilePicker = 'profile-picker', createArtworkRuntime = () => ({}), scheduledAmbientSource = () => null;");
  if (url.endsWith('/kit/dock/parts.tsx')) return module("export const Facts = 'facts', Control = 'dock-control', useDockActions = () => () => {}, playbackAction = () => 'pause', playbackIcon = () => 'i-pause', playbackLabel = () => 'Pause';");
  // The watch screen graph runs on the lifecycle frame; the kit renderers it names stay inert
  // and the real MediaSurface is loaded separately below by renderToStaticMarkup.
  if (url.endsWith('.tsx') || url.includes('/src/screens/watch/') || url.endsWith('/kit/dock/use-motion.ts')) {
    let source = transformTsx(url);
    source = source.replace(/from (['"])react\1/g, `from ${JSON.stringify(hookModule)}`);
    return module(source);
  }
  return next(url, context);
}});
const {PlaybackHost} = await import('../../src/shell/Playback.tsx');
const {NowPlayingCard} = await import('../../src/kit/dock/NowPlayingCard.tsx');
const {MediaSurface} = await import('../../src/kit/dock/MediaSurface.tsx');
test.after(() => { loader.deregister(); delete (globalThis as Record<string, unknown>)[bridgeKey]; });

function element(tree: ReactNode, type: string): any {
  for (const child of Children.toArray(tree)) if (isValidElement<any>(child)) {
    if (child.type === type) return child;
    const found = element(child.props.children, type); if (found) return found;
  }
}
const scope = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const fence = (id: string): QueuePlaybackFence => ({kind: 'queue', playback: {scope, run: 1}, sessionId: id, itemId: id, sourceId: id, entryId: id, generation: 1});
const audio: DockPlayback = {kind: 'music', fence: fence('song'), positionSeconds: 12, playing: true, expanded: false, queue: null};
const video: DockPlayback = {kind: 'video', fence: fence('movie'), positionSeconds: 42, playing: true, expanded: false, queue: null};
const display = {fence: audio.fence, identity: {id: 'song', kind: 'music' as const, title: 'Actual song', subtitle: []}, controls: ['play', 'pause'] as const, skipBackSeconds: 10, skipForwardSeconds: 30};

function focusFixture(t: import('node:test').TestContext) {
  const previous = {document: globalThis.document, HTMLElement: globalThis.HTMLElement};
  Object.assign(globalThis, {document: page, HTMLElement: FocusNode});
  const hooks = new HookFrame(), dock = new PlaybackDock(scope); dock.start(audio);
  t.after(() => { hooks.unmount(); listeners.clear(); Object.assign(globalThis, previous); });
  return {hooks, props: {view: dockView(dock.getSnapshot(), [display])!, binding: {getSnapshot: dock.getSnapshot, onAction() {}}, expanded: false, onExpandedChange() {}} satisfies NowPlayingCardProps};
}
test('focus cleanup uses the target captured on expansion, even when the supplied ref changes', t => {
  const {hooks, props} = focusFixture(t), intended = new FocusNode(), later = new FocusNode(), returnFocusRef = {current: intended};
  hooks.render(() => NowPlayingCard({...props, expanded: true, returnFocusRef}));
  returnFocusRef.current = later;
  hooks.render(() => NowPlayingCard({...props, expanded: false, returnFocusRef}));
  assert.equal(intended.calls, 1); assert.equal(later.calls, 0);
});
test('fallback focus is captured before entering the flyout and never jumps to later outside focus', t => {
  const {hooks, props} = focusFixture(t), intended = new FocusNode(), later = new FocusNode();
  hooks.render(() => NowPlayingCard(props)); intended.focus();
  hooks.render(() => NowPlayingCard({...props, expanded: true})); later.focus();
  hooks.render(() => NowPlayingCard(props));
  assert.equal(intended.calls, 2); assert.equal(later.calls, 1);
});
test('a disconnected captured target is not focused or replaced by a changing ref at unmount', t => {
  const {hooks, props} = focusFixture(t), intended = new FocusNode(), later = new FocusNode(), returnFocusRef = {current: intended};
  hooks.render(() => NowPlayingCard({...props, expanded: true, returnFocusRef}));
  intended.isConnected = false; returnFocusRef.current = later; hooks.unmount();
  assert.equal(intended.calls, 0); assert.equal(later.calls, 0);
});

test('host rerenders retain one media ref and presentations while actual authority handover retires private facts', t => {
  const hooks = new HookFrame(), events: unknown[] = [], dock = new PlaybackDock(scope);
  const timers = new Map<number, () => void>(); let serial = 0;
  const originalTimers = {setTimeout: globalThis.setTimeout, clearTimeout: globalThis.clearTimeout};
  const originalDocument = globalThis.document, originalWindow = globalThis.window, originalObserver = globalThis.MutationObserver;
  const motionPreference = {matches: false, addEventListener() {}, removeEventListener() {}};
  Object.assign(globalThis, {setTimeout: (fn: () => void) => { timers.set(++serial, fn); return serial; }, clearTimeout: (id: number) => timers.delete(id)});
  // The watch screen's motion preference reads a real browser window; the surface under test is
  // still the inert tree, so the document and observer are the smallest honest browser facts.
  Object.assign(globalThis, {
    document: {documentElement: {dataset: {}}, body: {dataset: {}}, fullscreenElement: null, fullscreenEnabled: false, activeElement: null, pictureInPictureEnabled: false, pictureInPictureElement: null, addEventListener() {}, removeEventListener() {}},
    window: {matchMedia: () => motionPreference, addEventListener() {}, removeEventListener() {}},
    MutationObserver: class {observe() {} disconnect() {}},
  });
  t.after(() => { hooks.unmount(); Object.assign(globalThis, originalTimers); Object.assign(globalThis, {document: originalDocument, window: originalWindow, MutationObserver: originalObserver}); });
  let state: WebPlaybackRuntimeSnapshot;
  const snapshot = (playback: DockPlayback, title: string): WebPlaybackRuntimeSnapshot => ({dock: dock.getSnapshot(), fence: playback.fence,
    playback: {playbackRun: 1, intentId: 1, revision: 1, phase: 'ready', intent: 'playing', positionSeconds: 12, duration: 100, cleanupPending: 0},
    presentation: {fence: playback.fence, subject: playback.kind === 'music' ? {kind: 'music', item: {kind: 'song', id: 'song', title}} : {kind: 'video', item: {kind: 'movie', id: 'movie', title}}, kind: playback.kind === 'music' ? 'music' : 'video', durationSeconds: 100, positionSeconds: 12, controls: ['play', 'pause'], playing: true, expanded: false, finished: false, rate: 1.25, volume: .4, muted: true, policy: null},
  } as WebPlaybackRuntimeSnapshot);
  // The screen's own listening port is not this test's subject: it stays unavailable, exactly as it is
  // when the runtime offers no queue/lyrics for the current song.
  const runtime = (id: string) => ({getSnapshot: () => state, subscribe: () => () => {}, dock, onAction: async () => {}, policyAction: async () => {}, replay: async () => {}, stop: async () => {}, browserMediaReady: true, collapse() {}, open() {}, speedChoices: () => [], setSpeed() {}, sleepTimer: () => null, setSleepTimer() {}, captureListening: () => null, mount(node: unknown) { events.push(['mount', id, node]); return () => events.push(['dispose', id]); }});
  const first = runtime('first'), second = runtime('second');
  const makeBinding = (runtime: unknown) => ({runtime, environment: {t: (key: string) => key}, artwork: {foreground: () => null, ambient: () => null}, skips: {back: 10, forward: 30}, reportError: (error: unknown) => { throw error; }} as unknown as ShellPlaybackBinding);
  let binding: ShellPlaybackBinding | undefined = makeBinding(first);
  const render = () => hooks.render(() => PlaybackHost({binding}));
  dock.start(audio); state = snapshot(audio, 'Private song'); render();
  dock.start(video); state = snapshot(video, 'Current movie');
  let tree = render();
  const media = element(tree, 'video').props.ref.current, surface = element(tree, 'media-surface');
  assert.equal(surface.props.finished, false);
  const presentations = element(tree, 'dock-host').props.presentations;
  assert.equal(presentations.length, 2);
  assert.equal(element(tree, 'dock-notice').props.notice.presentation.identity.title, 'Private song');
  assert.equal(element(tree, 'dock-host').props.presentations.find((p: any) => p.identity.title === 'Current movie').rate, 1.25);
  assert.equal(element(tree, 'dock-host').props.presentations.find((p: any) => p.identity.title === 'Current movie').muted, true);
  for (const tick of [...timers.values()]) tick();
  const before = hooks.renders; tree = render();
  assert.equal(hooks.renders - before, 1, 'notice timer state settles without changing derived presentations');
  assert.equal(element(tree, 'dock-host').props.presentations, presentations);
  assert.equal(element(tree, 'video').props.ref.current, media);
  assert.equal(element(tree, 'media-surface').type, surface.type); assert.equal(element(tree, 'media-surface').key, surface.key);
  state = {...state, presentation: {...state.presentation!, finished: true, controls: ['replay']}}; tree = render();
  const completed = element(tree, 'dock-host').props.presentations.find((p: any) => p.identity.title === 'Current movie');
  assert.equal(completed.finished, true); assert.deepEqual(completed.controls, ['replay']);
  assert.equal(element(tree, 'media-surface').props.finished, true);
  assert.equal(element(tree, 'media-surface').key, surface.key);
  assert.equal(element(tree, 'video').props.ref.current, media);
  state = {...state, presentation: {...state.presentation!, finished: false, controls: ['pause']}}; tree = render();
  assert.equal(element(tree, 'dock-host').props.presentations.find((p: any) => p.identity.title === 'Current movie').finished, false);
  assert.equal(element(tree, 'media-surface').props.finished, false);
  dock.beginRemote({id: 'receiver', kind: 'cast', phase: 'playing', sequence: '1', playback: video, positionSeconds: 42}, 0);
  state = {...state, dock: dock.getSnapshot()}; tree = render();
  const remoteHost = element(tree, 'dock-host').props;
  assert.equal(remoteHost.state.remote.id, 'receiver');
  assert.equal(remoteHost.presentations.length, 2);
  assert.equal(remoteHost.presentations.find((p: any) => p.identity.title === 'Current movie').volume, .4);
  assert.equal(element(tree, 'dock-notice').props.notice.presentation.identity.title, 'Private song');
  binding = makeBinding(second); state = snapshot(video, 'Other viewer movie'); tree = render();
  assert.equal(element(tree, 'video').props.ref.current, media);
  assert.deepEqual(element(tree, 'dock-host').props.presentations.map((p: any) => p.identity.title), ['Other viewer movie']);
  assert.equal(element(tree, 'dock-notice').props.notice, null);
  assert.deepEqual(events, [['mount', 'first', media], ['dispose', 'first'], ['mount', 'second', media]]);
  binding = undefined; tree = render();
  assert.equal(element(tree, 'video').props.ref.current, media);
  assert.equal(element(tree, 'dock-host'), undefined);
  assert.equal(element(tree, 'media-surface').props.finished, undefined);
  assert.deepEqual(events.at(-1), ['dispose', 'second']);
  binding = makeBinding(runtime('third')); state = snapshot(video, 'New authority movie'); tree = render();
  assert.equal(element(tree, 'video').props.ref.current, media);
  assert.deepEqual(element(tree, 'dock-host').props.presentations.map((p: any) => p.identity.title), ['New authority movie']);
  assert.deepEqual(events.at(-1), ['mount', 'third', media]);
});

test('the actual MediaSurface renders the explicit finished glyph around its one supplied video', () => {
  const video = createElement('video', {src: '/controlled-video', playsInline: true});
  const slot = {current: null};
  const render = (finished?: boolean, expanded = false) => renderToStaticMarkup(createElement(MediaSurface, {finished, expanded, dockSlot: slot, expandedSlot: slot, children: video}));
  for (const finished of [undefined, false, true, false]) {
    const html = render(finished);
    assert.equal((html.match(/<video\b/g) ?? []).length, 1);
    assert.match(html, /src="\/controlled-video"/);
    assert.equal(html.includes('data-finished="true"'), finished === true);
    assert.match(html, new RegExp(`href="#${finished ? 'i-back' : 'i-zoom'}"`));
  }
  const expanded = render(true, true);
  assert.match(expanded, /data-finished="true"/);
  assert.doesNotMatch(expanded, /surfaceExpand/);
});
