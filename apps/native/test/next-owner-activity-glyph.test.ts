import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree?.props ? [tree, ...nodes(tree.props.children)] : [];
const document = (streams: number, more: {scans?: number; tasks?: number; update?: boolean} = {}) => ({
  streams: {count: streams, items: [], convertingCount: 0, overflow: {count: 0, convertingCount: 0}},
  scans: Array.from({length: more.scans ?? 0}, () => ({})), tasks: {count: more.tasks ?? 0, items: []},
  update: {available: more.update === true, current: '1', state: 'idle'},
});

class FakeService {
  static made: FakeService[] = [];
  active = false; attached: unknown; loads = 0; disposed = false; state: any = {phase: 'unavailable'};
  listeners = new Set<() => void>();
  constructor(readonly options: any) {FakeService.made.push(this);}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => this.listeners.delete(listener);};
  setActive(value: boolean) {this.active = value;}
  attach(events: unknown) {this.attached = events;}
  load = async () => {this.loads++;};
  dispose() {this.disposed = true;}
  publish(state: any) {this.state = state; for (const listener of [...this.listeners]) listener();}
}

async function ownerActivity() {
  return screenModule(new URL('../src-next/shell/owner-activity.ts', import.meta.url), {
    react: {default: {}}, '../../../../packages/client-core/src/admin/activity-summary': {ActivitySummaryService: FakeService},
    '../../../../packages/client-core/src/playback-v1/local-http': {localV1Http: (api: unknown) => ({send() {}, api})},
    './freshness': {viewerQueriesFor: () => ({queries: true})},
  });
}

const context = (role: 'owner' | 'admin' | 'member') => {
  const authority = new Set<() => void>();
  let current = true;
  return {
    api: {}, events: {feed: true}, current: () => current,
    account: {account: {role}},
    session: () => ({sessionFamilyId: 'family', viewer: {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role}}),
    authority: () => ({epoch: 'epoch-1'}),
    subscribeAuthority: (listener: () => void) => {authority.add(listener); return () => authority.delete(listener);},
    retire: () => {current = false; for (const listener of [...authority]) listener();},
  } as any;
};

test('The indicator is unknown until the summary is ready, then grey, blue with a count, and the update dot', async () => {
  const {nativeOwnerActivityIndicator} = await ownerActivity();
  assert.deepEqual({...nativeOwnerActivityIndicator({phase: 'loading'})}, {known: false, count: 0, busy: false, update: false});
  assert.deepEqual({...nativeOwnerActivityIndicator({phase: 'error', error: {code: 'request_failed'}})}, {known: false, count: 0, busy: false, update: false});
  assert.deepEqual({...nativeOwnerActivityIndicator({phase: 'ready', document: document(0)})}, {known: true, count: 0, busy: false, update: false});
  assert.deepEqual({...nativeOwnerActivityIndicator({phase: 'ready', document: document(2, {update: true})})}, {known: true, count: 2, busy: true, update: true});
  assert.equal(nativeOwnerActivityIndicator({phase: 'ready', document: document(0, {scans: 1})}).busy, true, 'a scan is activity');
  assert.equal(nativeOwnerActivityIndicator({phase: 'ready', document: document(0, {tasks: 3})}).busy, true, 'a task is activity');
});

test('Only an owner on a phone or tablet gets the summary; it reads only while a glyph holds it', async () => {
  const {createNativeOwnerActivity} = await ownerActivity();
  FakeService.made = [];
  assert.equal(createNativeOwnerActivity(context('admin'), false), undefined);
  assert.equal(createNativeOwnerActivity(context('member'), false), undefined);
  assert.equal(createNativeOwnerActivity(context('owner'), true), undefined, 'TV draws no glyph');
  assert.equal(FakeService.made.length, 0);
  const owner = context('owner'), activity = createNativeOwnerActivity(owner, false)!;
  const service = FakeService.made[0]!;
  assert.deepEqual(service.options.source.getSnapshot().scope, {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role: 'owner', sessionFamilyId: 'family', authorizationEpoch: 'epoch-1'});
  assert.equal(service.active, false); assert.equal(service.loads, 0);
  const first = activity.hold(), second = activity.hold();
  assert.equal(service.active, true); assert.equal(service.attached, owner.events); assert.equal(service.loads, 1, 'two glyphs share one read');
  let published = 0; activity.subscribe(() => published++);
  service.publish({phase: 'ready', document: document(2, {update: true})});
  assert.deepEqual({...activity.getSnapshot()}, {known: true, count: 2, busy: true, update: true});
  service.publish({phase: 'ready', document: document(2, {update: true})});
  assert.equal(published, 1, 'an unchanged indicator does not redraw');
  first(); first();
  assert.equal(service.active, true, 'a repeated release counts once');
  second();
  assert.equal(service.active, false);
  let sourceChanged = 0; service.options.source.subscribe(() => sourceChanged++);
  owner.retire();
  assert.equal(sourceChanged, 1); assert.equal(service.options.source.getSnapshot(), null, 'a retired viewer offers no source');
  activity.dispose();
  assert.equal(service.disposed, true);
  assert.equal(typeof activity.hold(), 'function'); assert.equal(service.active, false, 'a disposed store never reads again');
});

test('The glyph draws grey idle, blue with the count and its tint, and the bell\'s dot for an update', async () => {
  const theme = createTheme({isTV: false, width: 390}), host = hooks();
  const {ActivityGlyph, activityGlyphLabel} = await screenModule(new URL('../src-next/shell/ActivityGlyph.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../packages/i18n/src': {defaultI18n},
    '../kit/Icon': {Icon: 'Icon'}, '../kit/shell/visual': {shellType: () => ({})}, '../kit/theme': {useTheme: () => theme},
  });
  let state = {known: false, count: 0, busy: false, update: false};
  const activity = {getSnapshot: () => state, subscribe: () => () => {}, hold: () => () => {}, dispose() {}};
  const draw = () => nodes(host.render(() => ActivityGlyph({activity})));
  let tree = draw();
  assert.equal(tree.find(node => node.type === 'Icon').props.color, theme.colors.textSecondary);
  assert.equal(tree.find(node => node.props.testID === 'activity-glyph-count'), undefined);
  assert.equal(tree.find(node => node.props.testID === 'activity-glyph-dot'), undefined);
  assert.equal(activityGlyphLabel(activity), 'Server activity', 'unknown names the button without a count');
  state = {known: true, count: 0, busy: false, update: false};
  tree = draw();
  assert.equal(tree.find(node => node.type === 'Icon').props.color, theme.colors.textSecondary, 'idle is grey');
  assert.equal(activityGlyphLabel(activity), 'Server activity: no streams');
  state = {known: true, count: 2, busy: true, update: true};
  tree = draw();
  assert.equal(tree.find(node => node.type === 'Icon').props.color, theme.colors.accentStrong, 'busy is blue');
  assert.deepEqual([tree.find(node => node.props.testID === 'activity-glyph-count').props.children].flat(), ['2']);
  assert.equal(tree[0].props.style.backgroundColor, 'rgba(64,159,212,.14)');
  assert.ok(tree.find(node => node.props.testID === 'activity-glyph-dot'));
  assert.equal(activityGlyphLabel(activity), 'Server activity: 2 streams, update available');
  state = {known: true, count: 0, busy: true, update: false};
  tree = draw();
  assert.equal(tree.find(node => node.props.testID === 'activity-glyph-count'), undefined, 'a scan alone is blue without a count');
  assert.equal(tree[0].props.style.backgroundColor, 'transparent');
});

test('Disposing the store while a glyph still holds it (bootstrap order) erases the indicator and tells the glyph', async () => {
  const {createNativeOwnerActivity} = await ownerActivity();
  FakeService.made = [];
  const owner = context('owner'), activity = createNativeOwnerActivity(owner, false)!, service = FakeService.made[0]!;
  const release = activity.hold();
  service.publish({phase: 'ready', document: document(1, {update: true})});
  assert.deepEqual({...activity.getSnapshot()}, {known: true, count: 1, busy: true, update: true});
  let changes = 0; activity.subscribe(() => changes++);
  // bootstrap.ts: the context ends, the viewer cache and the bindings (this store) are disposed, then authority listeners run.
  owner.current = () => false; activity.dispose(); owner.retire();
  assert.deepEqual({...activity.getSnapshot()}, {known: false, count: 0, busy: false, update: false}, 'no previous owner\'s activity stays drawn');
  assert.equal(changes, 1, 'a mounted glyph is told once');
  service.publish({phase: 'ready', document: document(3)}); release(); activity.dispose();
  assert.equal(activity.getSnapshot().known, false); assert.equal(changes, 1, 'nothing after disposal reaches it');
});
