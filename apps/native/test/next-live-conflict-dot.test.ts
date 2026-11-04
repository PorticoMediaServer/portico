import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeRecordingsController} from '../src-next/screens/settings-live/recordings-controller';

// tablet-play6/7: the guide's Recordings entry carries a red dot while a conflict needs a look.
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const authority = {serverId: 'server', accountId: 'owner', profileId: 'main', epoch: 'one', viewerFence: 'fence'};
const summary = (conflicts: number) => ({upcoming: 0, recorded: 0, rules: 0, groups: 0, unwatched: 0, conflicts});

function controller(service: Record<string, unknown>) {
  const lifetime = new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
  return {lifetime, recordings: new NativeRecordingsController(lifetime, {onAccessChanged() {}, dispose() {}, ...service} as any)};
}

test('the guide\'s summary read publishes only the aggregate summary, once at a time', async () => {
  let reads = 0, release!: () => void;
  const gate = new Promise<void>(resolve => {release = resolve;});
  const {lifetime, recordings} = controller({summary: async () => {reads++; await gate; return summary(2);}, storage: async () => {throw new Error('no storage read here');}});
  try {
    const first = recordings.loadSummary(), second = recordings.loadSummary();
    release(); await Promise.all([first, second]);
    assert.equal(reads, 1, 'a read in flight is shared');
    assert.equal(recordings.getSnapshot().summary?.conflicts, 2);
    assert.equal(recordings.getSnapshot().storage, undefined, 'no storage read');
  } finally {recordings.dispose(); lifetime.dispose();}
});

test('a summary read that fails draws no dot and is never the Recordings page\'s error', async () => {
  const {lifetime, recordings} = controller({summary: async () => {throw new Error('offline');}});
  try {
    await recordings.loadSummary();
    assert.equal(recordings.getSnapshot().summary, undefined); assert.equal(recordings.getSnapshot().error, undefined);
  } finally {recordings.dispose(); lifetime.dispose();}
});

test('the tab pair names the conflict and draws the dot on Recordings only while there is one', async () => {
  const h = hooks();
  const {NativeLiveTabs} = await screenModule(new URL('../src-next/screens/settings-live/LiveTabs.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text', View: 'View'}, '../../kit/controls': {Segmented: 'Segmented'},
    '../../kit/shell/visual': {shellType: () => ({})}, '../../kit/theme': {useTheme: () => ({colors: {record: 'red', text: 'white', textTertiary: 'grey'}, type: {button: {}}, textScale: 1})},
  });
  const t = defaultI18n.t;
  for (const conflicts of [0, 1, 3]) {
    const tree = h.render(() => NativeLiveTabs({value: 'guide', t, conflicts, onRecordings() {}}));
    const segmented = nodes(tree).find(node => node.type === 'Segmented');
    const props = segmented.props.itemProps({value: 'recordings'});
    const label = segmented.props.items.find((item: any) => item.value === 'recordings').label;
    const dot = nodes(label).find(node => typeof node.type === 'function' && node.type.name === 'LiveConflictDot');
    if (conflicts) {
      assert.equal(props.accessibilityLabel, t('next.live.recordingsConflicts', {count: conflicts}));
      assert.ok(dot, 'the dot is drawn beside Recordings');
    } else {
      assert.equal(props.accessibilityLabel, undefined); assert.equal(label, t('recordings.title'));
    }
  }
  assert.equal(t('next.live.recordingsConflicts', {count: 1}), 'Recordings, a conflict needs a look', 'tablet-play7\'s words');
});
