import test from 'node:test';
import assert from 'node:assert/strict';
import {fabricHost, React} from './helpers/fabric-host.mjs';
import {sameFence, type PlaybackFence} from '../../../packages/player-core/src/index.ts';
import {currentMorphFence} from '../src-next/kit/dock/morph-fence.ts';
import {releaseDock, validRect} from '../src-next/kit/dock/model.ts';
import {playerPostureCrossFadeMs} from '../src-next/platform/layout/player-presentation.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import {offlineDownloads} from '../platform/offline-downloads.ts';
import * as capabilityReadiness from '../src-next/platform/playback/capability-readiness.ts';

// Runs the real factory/Surface and PlayerMorph on the installed Fabric
// renderer with controlled native hosts, external-store delivery and animation
// callbacks. React owns subscription replacement, ref delivery, commits and
// unmount here; Native layout, AVPlayerLayer/SurfaceFlinger visibility and
// pixels still require platform runtime evidence.
const fence: PlaybackFence = {kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1},
  sessionId: 'session', sourceId: 'source', itemId: 'item', entryId: 'entry', generation: 1};
const frame = {x: 0, y: 0, width: 1920, height: 1080};
const dockFrame = {x: 12, y: 720, width: 160, height: 90};
type HostNode = {tag: number; type: unknown; props: Record<string, any>; children: HostNode[]};
const flatten = (style: any): Record<string, any> => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : style ?? {};
/** The real component mounted on Fabric; each update is one React commit. */
async function mount(component: any, initial: any) {
  const host = await fabricHost();
  const element = (props: any) => React.createElement(component, props);
  await host.render(element(initial));
  return {host, get tree() {return host.root[0] as HostNode;},
    async update(next: any) {await host.render(element(next));},
    async act(work: () => void | Promise<void>) {await work(); await host.settle();},
    unmount: () => host.unmount()};
}

async function surfaceFixture(os = 'android') {
  let snapshot: any = {engine: 'native', lease: '', fence: undefined};
  let attachments = 0, detachments = 0, subscriptions = 0;
  const listeners = new Set<() => void>(), tags: {owner: object; tag: number | null}[] = [], subtitles: boolean[] = [], requested: string[] = [];
  const runtime = {
    subscribe: (listener: () => void) => {subscriptions++; listeners.add(listener); return () => {subscriptions--; listeners.delete(listener);};},
    getSnapshot: () => snapshot,
    attachSurface: (_owner: object) => {attachments++; return () => {detachments++;};},
    setSurfaceTag: (owner: object, tag: number | null) => {tags.push({owner, tag});},
    setSubtitleDisplay: (visible: boolean) => {subtitles.push(visible);},
    installSnapshotCapture() {},
    // The adapter binds this eagerly; this fixture never plays a queue entry, so calling it is a
    // fixture misuse rather than a silently missing member.
    queue: {playEntry: () => {throw new Error('queue.playEntry is not configured in this surface fixture');}},
  };
  const dynamicRange = await screenModule(new URL('../src-next/platform/playback/dynamic-range-matching.ts', import.meta.url), {react: {...React, default: React}, 'react-native': {Platform: {OS: os, isTV: true}, NativeModules: {PorticoPlaybackModule: {}}}});
  const {createNativeShellPlaybackAdapter} = await screenModule(new URL('../src-next/platform/playback/native.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {
      AppState: {addEventListener: () => ({remove() {}})}, Dimensions: {get: () => ({width: 1920, height: 1080})},
      NativeEventEmitter: class {addListener() {return {remove() {}};}},
      NativeModules: {PorticoPlaybackModule: {}, PorticoRuntime: {playbackRequestId: () => 'request'}},
      Platform: {OS: os, isTV: true, Version: 35, constants: {}},
      UIManager: {hasViewManagerConfig: () => true}, findNodeHandle: (node: {tag: number}) => node.tag,
      requireNativeComponent: (name: string) => {requested.push(name); return name;},
    },
    '../../../../../packages/client-core/src/playback-v1/options': {CapabilityPublisher: class {async publish() {}}},
    '../../../../../packages/client-core/src/playback-v1/local-http': {localV1Http: () => ({})},
    '../../../../../packages/client-core/src/presentation/service-text': {serviceText: (key: string) => key},
    './dynamic-range-matching': dynamicRange, './capability-readiness': capabilityReadiness,
    './engine-choice': {chooseEngine() {}, parseEngineFeatures: () => ({})},
    './engine-media': {planEngineOf() {}, requiredSubtitleRendering() {}},
    './capability-document': {deviceCapabilityDocument: () => ({})},
    '../../../platform/offline-downloads': {offlineDownloads},
    './NativeListeningAdapter': {}, './runtime': {NativePlaybackRuntime: class {constructor(options: {offlineDevice: unknown}) {assert.equal(options.offlineDevice, offlineDownloads); return runtime;}}},
    './scope-http': {PlaybackScopeHttp: class {}}, '../video-snapshot': {createVideoSnapshotCapture: () => ({})},
  });
  const adapter = createNativeShellPlaybackAdapter({
    appVersion: 'test', api: {}, session: () => ({viewer: {authority: 'local', profileId: 'profile'}}),
    authority: () => ({serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'}),
    subscribeSettings: () => () => {}, settings: () => ({engine: 'native'}),
  });
  return {adapter, tags, subtitles, requested, get attachments() {return attachments;}, get detachments() {return detachments;}, get subscriptions() {return subscriptions;},
    publish(next: any) {snapshot = next; for (const listener of listeners) listener();},
  };
}

test('the actual native host hides on close before lease release and keeps its type/ref/owner for the next source', async () => {
  const d = await surfaceFixture();
  const props = {expandedBounds: frame, dockBounds: dockFrame, docked: false, subtitlesVisible: true, surfaceActive: true};
  const surface = await mount(d.adapter.Surface, props);
  const type = surface.tree.type, tag = surface.tree.tag;
  try {
    assert.equal(flatten(surface.tree.props.style).display, 'none', 'idle hiding must reach the native host itself');
    assert.equal(surface.tree.props.surfaceActive, false, 'native producer admission must not depend on Fabric laying out a hidden descendant');
    assert.equal(surface.tree.props.lease, '');
    await surface.act(() => d.publish({engine: 'native', lease: 'actual-lease', fence}));
    assert.equal(flatten(surface.tree.props.style).display, 'flex');
    assert.equal(surface.tree.props.surfaceActive, true);
    assert.equal(surface.tree.props.lease, 'actual-lease');
    await surface.update({...props, docked: true, subtitlesVisible: false});
    assert.equal(flatten(surface.tree.props.style).width, dockFrame.width);
    assert.equal(surface.tree.props.surfaceActive, true, 'docking must retain the admitted native producer');
    assert.equal(d.subtitles.at(-1), false);
    // Runtime close can publish fence-less idle before the async coordinator
    // releases its old lease. Neither publication may leave a black host visible.
    await surface.act(() => d.publish({engine: 'native', lease: 'actual-lease', fence: undefined}));
    assert.equal(flatten(surface.tree.props.style).display, 'none');
    assert.equal(surface.tree.props.surfaceActive, false, 'close revokes composition even before asynchronous lease release');
    await surface.act(() => d.publish({engine: 'native', lease: '', fence: undefined}));
    assert.equal(flatten(surface.tree.props.style).display, 'none');
    const next = {...fence, sessionId: 'session-next', generation: 2};
    await surface.act(() => d.publish({engine: 'native', lease: '', fence: next}));
    assert.equal(flatten(surface.tree.props.style).display, 'none');
    await surface.act(() => d.publish({engine: 'native', lease: 'next-lease', fence: next}));
    assert.equal(flatten(surface.tree.props.style).display, 'flex');
    assert.equal(surface.tree.props.surfaceActive, true);
    assert.strictEqual(surface.tree.type, type);
    assert.equal(surface.tree.tag, tag, 'the committed host instance survives the close and the next source');
    assert.equal(d.attachments, 1);
    assert.equal(d.detachments, 0);
    assert.equal(d.subscriptions, 1);
    assert.deepEqual(d.requested, ['PorticoPlayer']);
    assert.equal(d.tags.length, 1);
  } finally {await surface.unmount();}
  assert.strictEqual(d.tags[0]!.owner, d.tags[1]!.owner);
  assert.deepEqual(d.tags.map(value => value.tag), [tag, null]);
  assert.equal(d.detachments, 1);
  assert.equal(d.subscriptions, 0);
});

test('a leased native host stays hidden for missing or invalid target bounds', async () => {
  const d = await surfaceFixture();
  d.publish({engine: 'native', lease: 'actual-lease', fence});
  const props = {expandedBounds: frame, docked: false, subtitlesVisible: false, surfaceActive: true};
  const surface = await mount(d.adapter.Surface, props);
  try {
    assert.equal(flatten(surface.tree.props.style).display, 'flex');
    await surface.update({...props, docked: true});
    assert.equal(flatten(surface.tree.props.style).display, 'none');
    assert.equal(surface.tree.props.surfaceActive, false);
    for (const bounds of [undefined, {...frame, width: 0}, {...frame, height: -1}, {...frame, x: NaN}, {...frame, width: Infinity}]) {
      await surface.update({...props, expandedBounds: bounds});
      assert.equal(flatten(surface.tree.props.style).display, 'none');
      assert.equal(surface.tree.props.surfaceActive, false);
      for (const key of ['left', 'top', 'width', 'height']) assert.equal(Number.isFinite(flatten(surface.tree.props.style)[key]), true);
    }
    await surface.update(props);
    assert.equal(flatten(surface.tree.props.style).display, 'flex');
    assert.equal(surface.tree.props.surfaceActive, true);
    assert.equal(d.attachments, 1);
  } finally {await surface.unmount();}
});

for (const engine of ['native', 'mpv'] as const) {
  test(`${engine} withholds unmeasured expanded bounds without replacing its host and keeps a measured dock active`, async () => {
    const d=await surfaceFixture();d.publish({engine,lease:'actual-lease',fence});
    const props={expandedBounds:frame,dockBounds:dockFrame,docked:false,subtitlesVisible:true,surfaceActive:true};
    const surface=await mount(d.adapter.Surface,props),type=surface.tree.type,tag=surface.tree.tag;
    try {
      await surface.update({...props,expandedBounds:undefined});
      assert.equal(surface.tree.props.surfaceActive,false);assert.equal(flatten(surface.tree.props.style).display,'none');
      assert.strictEqual(surface.tree.type,type);assert.equal(surface.tree.tag,tag);assert.equal(surface.tree.props.lease,'actual-lease');
      assert.equal(d.attachments,1);assert.equal(d.detachments,0);assert.equal(d.subscriptions,1);
      await surface.update({...props,expandedBounds:undefined,docked:true});
      assert.equal(surface.tree.props.surfaceActive,true);assert.equal(flatten(surface.tree.props.style).display,'flex');
      await surface.update(props);assert.equal(surface.tree.props.surfaceActive,true);
      assert.strictEqual(surface.tree.type,type);assert.equal(surface.tree.tag,tag);
    } finally {await surface.unmount();}
    assert.equal(d.detachments,1);
  });

  test(`${engine} keeps its exact host/ref/owner/lease while the decoded cover gates only composition`, async () => {
    const d = await surfaceFixture();
    d.publish({engine, lease: 'actual-lease', fence});
    const props = {expandedBounds: frame, dockBounds: dockFrame, docked: false, subtitlesVisible: true, surfaceActive: true};
    const surface = await mount(d.adapter.Surface, props);
    const type = surface.tree.type, tag = surface.tree.tag;
    try {
      assert.equal(surface.tree.props.surfaceActive, true);
      await surface.update({...props, expandedBounds: {...frame, width: 840, height: 880}});
      assert.equal(surface.tree.props.surfaceActive, true, 'geometry alone does not revoke the producer');
      await surface.update({...props, surfaceActive: false});
      assert.equal(surface.tree.props.surfaceActive, false);
      assert.equal(flatten(surface.tree.props.style).display, 'flex', 'the retained host stays attached below its real-image cover');
      assert.equal(surface.tree.props.lease, 'actual-lease');
      await surface.update(props);
      assert.equal(surface.tree.props.surfaceActive, true);
      await surface.update({...props, expandedBounds: {...frame, width: 0}});
      assert.equal(surface.tree.props.surfaceActive, false, 'a true flag cannot admit invalid bounds');
      await surface.update(props);
      await surface.act(() => d.publish({engine, lease: 'actual-lease', fence: undefined}));
      assert.equal(surface.tree.props.surfaceActive, false, 'a stale lease without a current fence is never admitted');
      await surface.act(() => d.publish({engine, lease: '', fence}));
      assert.equal(surface.tree.props.surfaceActive, false, 'a current fence without the lease is never admitted');
      assert.strictEqual(surface.tree.type, type);
      assert.equal(surface.tree.tag, tag, 'the host instance is never replaced');
      assert.equal(d.attachments, 1);
      assert.equal(d.detachments, 0);
      assert.equal(d.subscriptions, 1);
      assert.equal(d.tags.length, 1);
    } finally {await surface.unmount();}
    assert.equal(d.detachments, 1);
    assert.strictEqual(d.tags[0]!.owner, d.tags[1]!.owner);
  });
}

test('Android producer admission is not sent to the Apple bridge', async () => {
  const d = await surfaceFixture('ios');
  const surface = await mount(d.adapter.Surface, {expandedBounds: frame, docked: false, subtitlesVisible: false, surfaceActive: true});
  try {
    assert.equal('surfaceActive' in surface.tree.props, false);
    await surface.act(() => d.publish({engine: 'native', lease: 'actual-lease', fence}));
    assert.equal('surfaceActive' in surface.tree.props, false);
  } finally {await surface.unmount();}
});

test('Q7 bounds-only changes preserve the actual Surface host/ref/owner/tag/subscription and lease', async () => {
  const d = await surfaceFixture();
  d.publish({engine: 'native', lease: 'retained-lease', fence});
  const props = {expandedBounds: frame, dockBounds: dockFrame, docked: false, subtitlesVisible: true, surfaceActive: true};
  const surface = await mount(d.adapter.Surface, props), type = surface.tree.type, tag = surface.tree.tag;
  try {
    for (const bounds of [
      {x: 0, y: 0, width: 890, height: 626}, {x: 0, y: 0, width: 840, height: 472.5},
      {x: 0, y: 0, width: 421, height: 236.8125}, {x: 0, y: 0, width: 880, height: 404},
      {x: 0, y: 0, width: 800, height: 820},
    ]) {
      await surface.update({...props, expandedBounds: bounds});
      assert.strictEqual(surface.tree.type, type);
      assert.equal(surface.tree.tag, tag);
      assert.equal(surface.tree.props.lease, 'retained-lease');
      assert.equal(flatten(surface.tree.props.style).width, bounds.width);
      assert.equal(flatten(surface.tree.props.style).height, bounds.height);
      assert.equal(d.attachments, 1);
      assert.equal(d.detachments, 0);
      assert.equal(d.tags.length, 1);
      assert.equal(d.subscriptions, 1);
    }
  } finally {await surface.unmount();}
  assert.strictEqual(d.tags[0]!.owner, d.tags[1]!.owner);
  assert.deepEqual(d.tags.map(value => value.tag), [tag, null]);
  assert.equal(d.detachments, 1);
});

test('composition admission toggles the same Android producer without detaching its owner or changing lease/ref/tag', async () => {
  const d = await surfaceFixture();
  d.publish({engine: 'native', lease: 'retained-lease', fence});
  const props = {expandedBounds: frame, dockBounds: dockFrame, docked: false, subtitlesVisible: true, surfaceActive: true};
  const surface = await mount(d.adapter.Surface, props), type = surface.tree.type, tag = surface.tree.tag;
  try {
    await surface.update({...props, surfaceActive: false});
    assert.equal(surface.tree.props.surfaceActive, false, 'the common occlusion signal must reach the native producer gate');
    assert.equal(flatten(surface.tree.props.style).display, 'flex', 'composition alone cannot retire the mounted host geometry');
    assert.equal(surface.tree.props.lease, 'retained-lease');
    assert.equal(surface.tree.tag, tag);
    assert.strictEqual(surface.tree.type, type);
    assert.equal(d.tags.length, 1);
    assert.equal(d.attachments, 1);
    assert.equal(d.detachments, 0);
    await surface.update(props);
    assert.equal(surface.tree.props.surfaceActive, true);
    assert.equal(d.subscriptions, 1);
  } finally {await surface.unmount();}
  assert.equal(d.detachments, 1);
});

async function postureMorphFixture(os: string, image?: {fence: PlaybackFence; image: React.ReactNode; ready?: boolean; failed?: boolean}, prepareLayout?: (source: PlaybackFence) => Promise<void>, reduce = false) {
  const reactions: ((value: any, previous: any) => void)[] = [], fades: {value: number; duration: number; finish?: (finished: boolean) => void}[] = [], contexts: any[] = [];
  const motion = {reduce, transforms: !reduce, timing: () => ({duration: reduce ? 120 : 180})};
  const {PlayerMorph} = await screenModule(new URL('../src-next/kit/dock/PlayerMorph.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {OS: os}, View: 'View'},
    'react-native-gesture-handler': {Gesture: {}},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useSharedValue: (value: any) => React.useRef({value}).current,
      useAnimatedStyle: (fn: () => any) => fn(), useAnimatedReaction: (_prepare: any, react: any) => {reactions.push(react);},
      cancelAnimation() {}, runOnJS: (fn: any) => fn, withTiming: (value: number, timing: any, finish: any) => {fades.push({value, duration: timing.duration, finish}); return value;}},
    '../../../../../packages/player-core/src/index': {}, '../Icon': {Icon: 'Icon'},
    '../theme': {useTheme: () => ({layers: {player: 30}, colors: {shadow: '#000', dim: '#000'}})},
    '../useMotion': {useMotion: () => motion},
    '../../platform/layout/player-presentation': {playerPostureCrossFadeMs},
    './morph-fence': {currentMorphFence}, './model': {releaseDock, validRect}, './visual': {alpha: () => '#000', dockStyles: {fill: {position: 'absolute', inset: 0}}},
  });
  const child = React.createElement('PersistentNativeSurface'), controller = {progress: {value: 0}, velocity: {value: 0}, dragging: {value: false}};
  const props = {controller, fence, docked: false, fullFrame: frame, dockFrame, retainedSnapshot: image, prepareLayout,
    children: (context: any) => {contexts.push(context); return child;}};
  const morph = await mount(PlayerMorph, props);
  /** The same committed child host under the morph's animated layer. */
  const childNode = () => morph.tree.children[0]!.children[0]!;
  return {morph, props, contexts, reactions, fades, childTag: childNode().tag};
}

/** The persistent native child is retained below the same layer, never replaced. */
const assertChild = (d: {morph: {tree: HostNode}; childTag: number}) => assert.equal(d.morph.tree.children[0]!.children[0]!.tag, d.childTag, 'the same persistent native child stays under the morph');

test('Apple geometry without a decoded cover settles with live composition and no blanking fade', async () => {
  const d = await postureMorphFixture('ios'), next = {x: 0, y: 0, width: 890, height: 626};
  try {
    await d.morph.update({...d.props, fullFrame: next});
    assert.deepEqual(d.contexts.at(-1).bounds, next);
    assert.equal(d.contexts.at(-1).surfaceActive, true);
    assertChild(d);
    assert.equal(d.fades.length, 0);
    assert.equal(flatten(d.morph.tree.children[0]!.props.style).opacity, 1);
    await d.morph.update({...d.props, fullFrame: {...next}});
    assert.equal(d.fades.length, 0);
  } finally {await d.morph.unmount();}
});

for (const os of ['android', 'ios']) {
test(`${os} suppresses live composition only under a current-fence snapshot and restores it beneath one outgoing fade`, async () => {
  const image = {fence, image: React.createElement('CapturedFrame'), ready: true}, d = await postureMorphFixture(os, image);
  try {
    const reaction = d.reactions.at(-1)!;
    d.props.controller.progress.value = .4; d.props.controller.velocity.value = .2;
    await d.morph.act(() => reaction({active: true, p: .4, v: .2, drag: false, goal: 0}, {active: true, p: 0, v: 0, drag: false, goal: 0}));
    assert.equal(d.contexts.at(-1).surfaceActive, false);
    d.props.controller.progress.value = 0; d.props.controller.velocity.value = 0;
    await d.morph.act(() => d.reactions.at(-1)!({active: true, p: 0, v: 0, drag: false, goal: 0}, {active: true, p: .4, v: .2, drag: false, goal: 0}));
    assert.equal(d.contexts.at(-1).surfaceActive, true);
    assert.equal(d.contexts.at(-1).moving, true, 'retain capture until its one fade completes');
    assert.equal(d.fades.length, 1);
    await d.morph.act(() => d.fades[0]!.finish!(true));
    assert.equal(d.contexts.at(-1).moving, false);
    assertChild(d);
    await d.morph.update({...d.props, retainedSnapshot: {...image, fence: {...fence, generation: 2}}});
    d.props.controller.progress.value = .4; d.props.controller.velocity.value = .2;
    await d.morph.act(() => d.reactions.at(-1)!({active: true, p: .4, v: .2, drag: false, goal: 0}, {active: true, p: 0, v: 0, drag: false, goal: 0}));
    assert.equal(d.contexts.at(-1).surfaceActive, true, 'a stale snapshot cannot hide the live producer');
  } finally {await d.morph.unmount();}
});

test(`${os} geometry preparation keeps live video until capture is usable`, async () => {
    let ready!: () => void, requests = 0;
    const prepared = new Promise<void>(resolve => {ready = resolve;});
    const d = await postureMorphFixture(os, undefined, async source => {assert.ok(sameFence(source, fence)); requests++; await prepared;});
    const next = {x: 0, y: 0, width: 890, height: 626};
    try {
      await d.morph.update({...d.props, fullFrame: next});
      assert.equal(requests, 1);
      assert.equal(d.contexts.at(-1).surfaceActive, true);
      assert.deepEqual(d.contexts.at(-1).bounds, frame, 'keep old live bounds while preparing');
      await d.morph.act(() => d.reactions.at(-1)!({active: true, p: 0, v: 0, drag: false, goal: 0}, null));
      assert.deepEqual(d.contexts.at(-1).bounds, frame, 'a first settled reaction cannot bypass capture');
      await d.morph.act(async () => {
        await d.morph.update({...d.props, fullFrame: next, retainedSnapshot: {fence, image: React.createElement('CapturedFrame'), ready: false}});
        ready(); await prepared;
      });
      assert.deepEqual(d.contexts.at(-1).bounds, frame, 'capture completion alone is not image readiness');
      assert.equal(d.contexts.at(-1).surfaceActive, true);
      await d.morph.update({...d.props, fullFrame: next, retainedSnapshot: {fence, image: React.createElement('CapturedFrame'), ready: true}});
      assert.deepEqual(d.contexts.at(-1).bounds, next);
      assert.equal(d.contexts.at(-1).surfaceActive, true);
      assert.equal(d.fades.length, 1);
      assert.equal(d.fades[0]!.duration, 250);
      await d.morph.act(() => d.fades[0]!.finish!(true));
      assert.equal(d.contexts.at(-1).moving, false);
      assertChild(d);
      await d.morph.update({...d.props, fullFrame: {...next}, retainedSnapshot: {fence, image: React.createElement('CapturedFrame'), ready: true}});
      assert.equal(d.fades.length, 1, 'equal geometry cannot re-arm the consumed one-pass fade');
    } finally {ready(); await d.morph.unmount();}
});

}

test('foldable9 omits geometry cross-fades entirely with reduced motion', async () => {
  for (const os of ['android', 'ios']) {
    let captures = 0;
    const d = await postureMorphFixture(os, undefined, async () => {captures++;}, true);
    const next = {x: 0, y: 0, width: 890, height: 626};
    try {
      await d.morph.update({...d.props, fullFrame: next});
      assert.deepEqual(d.contexts.at(-1).bounds, next);
      assert.equal(d.contexts.at(-1).surfaceActive, true);
      assert.equal(captures, 0);
      assert.equal(d.fades.length, 0);
    } finally {await d.morph.unmount();}
  }
});

for (const os of ['android', 'ios']) {
  test(`${os} an image decode error cannot suppress live composition or start a cover fade`, async () => {
    const image = {fence, image: React.createElement('CapturedFrame'), ready: false};
    const d = await postureMorphFixture(os, image, async () => {}), next = {x: 0, y: 0, width: 890, height: 626};
    try {
      await d.morph.update({...d.props, fullFrame: next});
      assert.equal(d.contexts.at(-1).surfaceActive, true);
      assert.deepEqual(d.contexts.at(-1).bounds, frame);
      await d.morph.update({...d.props, fullFrame: next, retainedSnapshot: {...image, failed: true}});
      assert.deepEqual(d.contexts.at(-1).bounds, next);
      assert.equal(d.contexts.at(-1).surfaceActive, true);
      assert.equal(flatten(d.morph.tree.children[0]!.props.style).opacity, 1);
      assert.equal(d.fades.length, 0);
      assertChild(d);
    } finally {await d.morph.unmount();}
  });

test(`${os} failed geometry capture stays live and late preparation cannot resize a replacement source`, async () => {
  const d = await postureMorphFixture(os, undefined, async () => {throw new Error('capture unavailable');});
  const next = {x: 0, y: 0, width: 890, height: 626};
  try {
    await d.morph.act(async () => {await d.morph.update({...d.props, fullFrame: next}); await Promise.resolve(); await Promise.resolve();});
    assert.deepEqual(d.contexts.at(-1).bounds, next);
    assert.equal(d.contexts.at(-1).surfaceActive, true);
    assert.equal(d.fades.length, 0);
  } finally {await d.morph.unmount();}
  let ready!: () => void;
  const wait = new Promise<void>(resolve => {ready = resolve;});
  const late = await postureMorphFixture(os, undefined, () => wait);
  try {
    await late.morph.update({...late.props, fullFrame: next});
    const replacement = {...fence, sessionId: 'replacement', generation: 2};
    await late.morph.update({...late.props, fence: replacement, fullFrame: dockFrame});
    await late.morph.act(async () => {ready(); await wait;});
    assert.deepEqual(late.contexts.at(-1).bounds, {x: 0, y: 0, width: dockFrame.width, height: dockFrame.height});
    assert.equal(late.contexts.at(-1).surfaceActive, true);
    assert.equal(late.fades.length, 0);
  } finally {ready(); await late.morph.unmount();}
});

}

test('PlayerMorph projects close to its existing wrapper/child and rejects late settlement from the closed source', async () => {
  const reactions: ((value: any, previous: any) => void)[] = [];
  const {PlayerMorph} = await screenModule(new URL('../src-next/kit/dock/PlayerMorph.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {OS: 'android'}, View: 'View'},
    'react-native-gesture-handler': {Gesture: {}},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useSharedValue: (value: any) => React.useRef({value}).current,
      useAnimatedStyle: (fn: () => any) => fn(), useAnimatedReaction: (_prepare: any, react: any) => {reactions.push(react);},
      cancelAnimation() {}, runOnJS: (fn: any) => fn, withTiming: (value: any) => value},
    '../../../../../packages/player-core/src/index': {}, '../Icon': {Icon: 'Icon'},
    '../theme': {useTheme: () => ({layers: {player: 30}, colors: {shadow: '#000', dim: '#000'}})},
    '../useMotion': {useMotion: () => ({reduce: false, transforms: true, timing: () => ({duration: 180})})},
    '../../platform/layout/player-presentation': {playerPostureCrossFadeMs},
    './morph-fence': {currentMorphFence}, './model': {releaseDock, validRect}, './visual': {alpha: () => '#000', dockStyles: {fill: {position: 'absolute', inset: 0}}},
  });
  const child = React.createElement('PersistentNativeSurface'), presentations: any[] = [], contexts: any[] = [];
  const props = {controller: {progress: {value: 0}, velocity: {value: 0}, dragging: {value: false}},
    fence: undefined as PlaybackFence | undefined, docked: false, fullFrame: frame, dockFrame,
    children: (context: any) => {contexts.push(context); return child;}, onPresentationChange: (value: any) => {presentations.push(value);}};
  const morph = await mount(PlayerMorph, props);
  const childNode = () => morph.tree.children[0]!.children[0]!, childTag = childNode().tag;
  try {
    assert.equal(flatten(morph.tree.props.style).display, 'none');
    assert.equal(contexts.at(-1).active, false);
    assert.equal(childNode().tag, childTag);
    await morph.update({...props, fence});
    assert.equal(flatten(morph.tree.props.style).display, 'flex');
    assert.equal(contexts.at(-1).active, true);
    const late = reactions.at(-1)!;
    await morph.update(props);
    const count = presentations.length;
    await morph.act(() => late({active: true, p: 1, v: 0, drag: false, goal: 1}, {active: true, p: .5, v: .1, drag: false, goal: 1}));
    assert.equal(flatten(morph.tree.props.style).display, 'none');
    assert.equal(contexts.at(-1).active, false);
    assert.equal(presentations.length, count);
    assert.equal(childNode().tag, childTag);
    const next = {...fence, sessionId: 'next-session', generation: 2};
    await morph.update({...props, fence: next});
    assert.equal(sameFence(presentations.at(-1).fence, next), true);
    assert.equal(flatten(morph.tree.props.style).display, 'flex');
    assert.equal(childNode().tag, childTag);
  } finally {await morph.unmount();}
});
