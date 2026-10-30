/**
 * FX229 / BLK-PLAY-016: the host, More menu and Equalizer openings each have their own published
 * receipt, minted fresh in layout from immutable rendered facts.
 *
 * Panel cases use a manual receipt factory with the actual panel/admission helpers; the parent
 * cases mount the actual NowPlaying with a stub More/EQ host. The harness runs hooks eagerly and
 * has no React StrictMode or unmount replay; cleanup→setup is modelled as a fresh receipt pair
 * with the same immutable facts, not as relaunching the player. Source only: unrun here.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {queueScope,playbackFence,type QueuePlaybackFence} from '../../packages/player-core/src/index.ts';
import type {WebPlaybackAuthority} from '../src/platform/playback/types.ts';
import {fenceKey} from '../src/shell/playback-model';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, one, tick, tree} from './helpers/interaction-harness.mjs';

const scopeToken=queueScope({serverId:'server',authority:'local',accountId:'account',profileId:'profile',controllerId:'controller',controllerEpoch:'epoch',commandLaneId:'lane'});
const authority:WebPlaybackAuthority={serverId:'server',authority:'local',accountId:'account',profileId:'profile',epoch:1,viewerFence:'viewer'};
const fence = (itemId = 'song-1'):QueuePlaybackFence => playbackFence({kind: 'queue', playback: {scope: scopeToken, run: 7}, sessionId: 'session', itemId, sourceId: 'source', entryId: 'entry', generation: 1});
const equalizer = (enabled = true) => ({enabled, preampDb: 0, bandsDb: Array.from({length: 10}, () => 0)});

async function panelModule(renderer?: any) {
  const h = renderer ?? createRenderer();
  const media = {DeviceEqualizer: 'DeviceEqualizer', ListeningNotice: 'ListeningNotice', SignalLine: 'SignalLine', SignalPath: 'SignalPath'};
  const panel = await componentModule(new URL('../src/screens/watch/music/EqualizerPanel.tsx', import.meta.url), {
    react: h.react, '@i18n': {defaultI18n: {t: (key: string) => key}}, '../../../kit/controls': {Button: 'Button'},
    '../../../kit/media': media, '../../../shell/playback-model': {fenceKey},
  });
  return {h, media, panel};
}

async function mountPanel() {
  const {h, media, panel} = await panelModule();
  const changes: any[] = [];
  const held: {value: any; pending: Promise<void> | undefined} = {value: equalizer(), pending: undefined};
  const port = {getSnapshot: () => held.value, subscribe: () => () => {}, start: () => () => {},
    change: async (value: any) => {changes.push(value); await held.pending;}, reset: async () => {changes.push('reset');}};
  const originalFence = fence();
  const state: any = {disposed: false, authority, fence: originalFence, playback: {playbackRun: 7}, presentation: {kind: 'music', fence: originalFence}, equalizerPort: port};
  let getter: (() => any) | undefined;
  const runtime = {getSnapshot: () => getter ? getter() : state, equalizerPort: port};
  let host = true, scopeAllowed = true;
  const isCurrent = () => host, scope = {current: () => scopeAllowed};
  const calls = {close: 0, settings: 0};
  const facts = (value: any = state) => ({runtime: runtime as any, port: port as any, authority: value.authority,
    fenceAt: fenceKey(value.fence), presentationAt: fenceKey(value.presentation.fence), run: value.playback.playbackRun, isCurrent, scope});
  const publishedHost: {current: any} = {current: null}, publishedSurface: {current: any} = {current: null};
  const paint = (hostFacts: any, hostReceipt: any, surface: any) => tree(h.render(() => panel.WatchEqualizerPanel({receipt: surface, rendered: surface, published: publishedSurface,
    host: hostFacts, hostReceipt, publishedHost, runtime: runtime as any, port: port as any, isCurrent, scope,
    close: () => {calls.close++;}, openSettings: () => {calls.settings++;}})));
  const setup = (value: any = state, reuse?: {host?: any; surface?: any; facts?: any}) => {
    const hostFacts = reuse?.facts ?? reuse?.host?.facts ?? facts(value);
    const reusePair = !!(reuse?.host && reuse?.surface);
    const hostReceipt = reuse?.host ?? {facts: hostFacts, live: true};
    const surface = reuse?.surface ?? {host: hostReceipt, live: true};
    if (!reusePair) {publishedHost.current = hostReceipt; publishedSurface.current = surface;}
    paint(hostFacts, hostReceipt, surface);
    return {hostFacts, hostReceipt, surface, tree: paint(hostFacts, hostReceipt, surface)};
  };
  const early = (hostFacts: any, hostReceipt: any, surface: any) => tree(h.render(() => panel.WatchEqualizerPanel({receipt: surface, rendered: surface, published: publishedSurface,
    host: hostFacts, hostReceipt, publishedHost, runtime: runtime as any, port: port as any, isCurrent, scope,
    close: () => {}, openSettings: () => {}})));
  return {calls, changes, early, facts, getter: (fn: () => any) => {getter = fn;}, held, publishedHost, publishedSurface, scope, setHost: (value: boolean) => {host = value;}, setScope: (value: boolean) => {scopeAllowed = value;}, setup, state};
}

test('a live EQ opening writes; an ordinary rerender keeps the same receipts', async () => {
  const {changes, setup} = await mountPanel();
  const first = setup();
  const eq = one(first.tree, 'DeviceEqualizer').props;
  await eq.port.change(equalizer(false));
  assert.equal(changes.length, 1);
  const again = setup(undefined as any, {host: first.hostReceipt, surface: first.surface});
  assert.equal(one(again.tree, 'DeviceEqualizer').props.port, eq.port);
});

test('the first changed-facts render withholds before any receipt replacement', async () => {
  const {early, facts, setup} = await mountPanel();
  const first = setup();
  const changed = {...first.hostFacts, authority: {}};
  const withheld = early(changed, first.hostReceipt, first.surface);
  assert.equal(all(withheld, 'DeviceEqualizer').length, 0, 'the old published pair cannot draw actionable rows under new facts');
  const live = setup(undefined as any, {facts: changed});
  assert.equal(all(live.tree, 'DeviceEqualizer').length, 1, 'fresh receipts for the current facts draw the panel');
});

test('an explicit same-facts cleanup/setup replay makes the old EQ actions dead and the new live', async () => {
  const {calls, changes, publishedHost, publishedSurface, setup} = await mountPanel();
  const first = setup();
  const oldEq = one(first.tree, 'DeviceEqualizer').props;
  // Clean up A exactly, then allocate fresh receipts from the same immutable facts.
  first.hostReceipt.live = false; first.surface.live = false; publishedHost.current = null;
  const second = setup(undefined as any, {facts: first.hostFacts});
  assert.notEqual(second.surface, first.surface);
  assert.equal(publishedHost.current, second.hostReceipt, 'the fresh host receipt is published');
  assert.equal(publishedSurface.current, second.surface, 'the fresh surface receipt is published');
  assert.equal(second.hostReceipt.live, true);
  assert.equal(second.surface.live, true);
  oldEq.onBack();
  oldEq.onOpenSettings();
  oldEq.reportError(new Error('late'));
  await assert.rejects(oldEq.port.change(equalizer(false)), /retired/i);
  assert.equal(calls.close, 0);
  assert.equal(calls.settings, 0);
  await one(second.tree, 'DeviceEqualizer').props.port.change(equalizer(false));
  assert.equal(changes.length, 1, 'only the successor receipt wrote');
});

test('a getSnapshot that swaps the published receipt mid-check cannot slip a write through', async () => {
  const {changes, publishedHost, setup, state} = await mountPanel();
  const first = setup();
  const eq = one(first.tree, 'DeviceEqualizer').props;
  const successor = {facts: first.hostFacts, live: true};
  let calls = 0;
  // The last external getSnapshot retires A and publishes B while still returning A's facts.
  const getter = () => {if (++calls === 2) {first.hostReceipt.live = false; publishedHost.current = successor;} return state;};
  const original = first.hostFacts.runtime.getSnapshot;
  (first.hostFacts.runtime as any).getSnapshot = getter;
  await assert.rejects(eq.port.change(equalizer(false)), /retired/i);
  (first.hostFacts.runtime as any).getSnapshot = original;
  assert.equal(changes.length, 0);
});

test('the original predicates are rechecked before a write', async () => {
  const {changes, scope, setup} = await mountPanel();
  const v = setup();
  const eq = one(v.tree, 'DeviceEqualizer').props;
  let checks = 0;
  (scope as any).current = () => ++checks === 1;
  await assert.rejects(eq.port.change(equalizer(false)), /retired/i);
  assert.equal(changes.length, 0);
});

test('an absent device record offers Back while it loads', async () => {
  const {held, setup} = await mountPanel();
  held.value = null;
  const v = setup();
  assert.equal(all(v.tree, 'DeviceEqualizer').length, 0);
  assert.ok(all(v.tree, 'Button').some((node: ReturnType<typeof tree>[number]) => (Array.isArray(node.props.children) ? node.props.children.join('') : node.props.children) === 'next.media.eq.backQueue'));
});

async function nowPlaying() {
  const h = createRenderer();
  const {media, panel} = await panelModule(h);
  const held: {value: any; pending: Promise<void> | undefined} = {value: equalizer(), pending: undefined};
  const changes: any[] = [];
  const port = {getSnapshot: () => held.value, subscribe: () => () => {}, start: () => () => {}, change: async (value: any) => {changes.push(value);}, reset: async () => {changes.push('reset');}};
  const queueFence = fence();
  const snapshot: any = {disposed: false, authority, fence: queueFence, playback: {playbackRun: 7, intent: 'playing', phase: 'playing'},
    presentation: {kind: 'music', fence: queueFence, positionSeconds: 0, durationSeconds: 180, playing: true, controls: ['play', 'pause', 'seek', 'next', 'previous'], subject: {}, posterUrl: ''},
    dock: {local: {kind: 'music', expanded: true, fence: queueFence}, remote: null}, queue: {}, sequence: 1};
  const runtime = {getSnapshot: () => snapshot, equalizerPort: port};
  const navigations: any[] = [];
  const app = await componentModule(new URL('../src/screens/watch/music/NowPlaying.tsx', import.meta.url), {
    react: h.react, '@i18n': {defaultI18n: {t: (key: string) => key}},
    '@core/presentation/kit-model/index.ts': {playbackIdentity: () => ({title: 'Song', subtitle: [{text: 'Artist'}]})},
    '../../../kit/artwork': {AmbientArt: 'AmbientArt', Artwork: 'Artwork'}, '../../../kit/Icon': {Icon: 'Icon'}, '../../../kit/controls': {Slider: 'Slider'},
    '../../../kit/overlays': {Menu: 'Menu'}, '../../../kit/media': media,
    '../model': {dispatchWatch: async () => {}, watchClock: () => '0:00', watchSeek: () => null, watchSeekBounds: () => null},
    '../queue-neighbours': {watchQueueNeighbours: () => ({})}, './signal': {watchMusicSignal: () => undefined},
    './audio-facts': {readWatchMusicAudioFacts: () => ({})}, '../../../shell/playback-model': {fenceKey},
    '../../../router': {router: {navigate: async (to: any) => {navigations.push(to);}}},
    './EqualizerPanel': {WatchEqualizerPanel: panel.WatchEqualizerPanel, hostAdmit: panel.hostAdmit, surfaceAdmit: panel.surfaceAdmit},
  });
  const binding = {runtime: runtime as any, artwork: {foreground: () => undefined, ambient: () => undefined}, environment: {locale: 'en'}, skips: {back: 10, forward: 30}, reportError: () => {}};
  let expanded = true;
  const show = (value: any = snapshot) => {
    const paint = () => tree(h.render(() => app.WatchMusicNowPlaying({binding: binding as any, snapshot: value as any, expanded, onCollapse: () => {}, phone: false})));
    paint();
    return paint();
  };
  return {changes, navigations, show, snapshot};
}

const panelHost = (v: any[]) => all(v, 'DeviceEqualizer')[0];
const moreButton = (v: any[]) => all(v, 'button').find((node: ReturnType<typeof tree>[number]) => node.props['aria-label'] === 'player.more');
const menu = (v: any[]) => one(v, 'Menu').props;
const openMenu = (show: any) => {let v = show(); moreButton(v)!.props.onClick(); return show();};
const selectEq = (show: any) => {let v = openMenu(show); menu(v).items[0].onSelect(); return show();};

test('More A dismissed then B opened on the same host: saved A cannot close B', async () => {
  const {show} = await nowPlaying();
  const a = openMenu(show);
  const menuA = menu(a);
  menuA.onOpenChange(false);
  const b = openMenu(show);
  assert.equal(menu(b).open, true, 'B is open on the same expanded host');
  menuA.onOpenChange(false);
  assert.equal(menu(show()).open, true, 'saved A dismissal cannot close B');
  menuA.items[0].onSelect();
  assert.equal(panelHost(show()), undefined, 'saved A selection cannot open EQ');
});

test('EQ A Back then EQ B on the same host: saved A cannot close B', async () => {
  const {show} = await nowPlaying();
  const a = selectEq(show);
  const panelA = panelHost(a);
  assert.ok(panelA);
  panelA.props.onBack();
  const b = selectEq(show);
  const panelB = panelHost(b);
  assert.ok(panelB);
  panelA.props.onBack();
  assert.ok(panelHost(show()), 'saved A Back cannot close B');
  panelB.props.onBack();
  assert.equal(panelHost(show()), undefined);
});

test('actual panel under NowPlaying: saved A Back/Settings/report/write cannot touch B', async () => {
  const {changes, navigations, show} = await nowPlaying();
  const a = selectEq(show);
  const panelA = panelHost(a);
  panelA.props.onBack();
  const b = selectEq(show);
  const panelB = panelHost(b);
  assert.ok(panelB);
  panelA.props.onBack();
  assert.ok(panelHost(show()), 'saved A Back cannot close B');
  panelA.props.onOpenSettings();
  assert.equal(navigations.length, 0, 'saved A Settings cannot navigate');
  panelA.props.reportError(new Error('late'));
  await assert.rejects(panelA.props.port.change(equalizer(false)), /retired/i);
  assert.equal(changes.length, 0, 'saved A never writes the device port');
  const afterAlert = show();
  assert.equal(all(afterAlert, 'p').filter((node: ReturnType<typeof tree>[number]) => node.props.role === 'alert').length, 0, 'saved A error publishes no alert');
  await panelB.props.port.change(equalizer(false));
  assert.equal(changes.length, 1, 'live B writes its own port');
  panelB.props.onOpenSettings();
  assert.equal(navigations.length, 1, 'live B Settings navigates once');
  panelB.props.onBack();
  assert.equal(panelHost(show()), undefined);
});

test('same-tick old dismiss and selection are refused on the next repaint', async () => {
  const {show} = await nowPlaying();
  const a = openMenu(show);
  const menuA = menu(a);
  // Retire A synchronously, then its saved selection runs before any new render/layout.
  menuA.onOpenChange(false);
  menuA.items[0].onSelect();
  let v = show();
  assert.equal(one(v, 'Menu').props.open, false);
  assert.equal(panelHost(v), undefined, 'the retired selection cannot open the Equalizer');
  const b = openMenu(show);
  assert.equal(one(b, 'Menu').props.open, true);
  menuA.onOpenChange(false);
  menuA.items[0].onSelect();
  v = show();
  assert.equal(one(v, 'Menu').props.open, true, 'the successor menu stays open');
  assert.equal(panelHost(v), undefined, 'the successor EQ was never opened by the old surface');
});

test('an ordinary position update keeps the actual EQ receipt', async () => {
  const {show, snapshot} = await nowPlaying();
  const opened = selectEq(show);
  const position = {...snapshot, presentation: {...snapshot.presentation, positionSeconds: 42}};
  const after = show(position);
  assert.ok(panelHost(after));
  assert.equal(panelHost(after).props.port, panelHost(opened).props.port, 'the same receipts keep one guarded port');
});
