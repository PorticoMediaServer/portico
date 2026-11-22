import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

const t = (key: string) => key;
async function tab(view: Record<string, unknown>) {
  const stub = (names: string[]) => Object.fromEntries(names.map(name => [name, name]));
  const {LiveRecordingTab} = await screenModule(new URL('../src-next/pages/touch/live/LiveRecordingTab.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../kit/controls': stub(['Button', 'Select', 'Switch']), '../../../kit/frames': stub(['SectionCard']), '../../../kit/overlays': stub(['BottomSheet']), '../../../kit/shell': stub(['SettingsRow']),
    '../../../kit/settings/StorageParts': stub(['StorageActions', 'StorageNote']), '../../../kit/live/LiveSourceParts': stub(['LiveFieldCard']),
    '../../../kit/live/RecordingOwnerParts': stub(['RowButtons', 'TunerState']), '../../../kit/live/NewRuleSheet': stub(['NewRuleSheet']),
    '../../../kit/settings/FolderExplorer': stub(['FolderExplorer']),
    '../../shared/live/useLiveRecording': {useLiveRecording: () => ({t, busy: false, tuners: [], accountGroups: [], rules: [], setAdding() {}, setChoosing() {}, load() {}, closeRemove() {}, setCancelBookings() {}, confirmRemove() {}, ...view})},
  });
  return LiveRecordingTab({context: {}, runtime: {text: {t}}});
}
const all = (node: any): any[] => !node || typeof node !== 'object' ? [] : Array.isArray(node) ? node.flatMap(all) : [node, ...all(node.props?.children)];

test('Recording shows tuners with their state, accounts with their profiles under them, and a rule\'s Pause and Remove', async () => {
  const set: boolean[] = [], removed: string[] = [];
  const nodes = all(await tab({tuners: [{key: 't', name: 'Synthetic', status: {text: '1 clash', tone: 'warning'}}],
    accountGroups: [{key: 'a', name: 'owner', help: 'h', allowed: true, locked: true, set() {}, profiles: [{key: 'p', name: 'Kids', help: 'no', on: false, locked: false, set: (value: boolean) => set.push(value)}]}],
    rules: [{key: 'r', title: 'News', facts: 'Owner', pauseLabel: 'Pause', togglePause() {}, remove: () => removed.push('r')}]}));
  const rows = nodes.filter(node => node.type === 'SettingsRow');
  assert.deepEqual(rows.map(row => row.props.label).slice(0, 4), ['Synthetic', 'owner', 'Kids', 'News']);
  assert.equal(rows[0].props.control.props.tone, 'warning');
  assert.equal(rows[1].props.control.props.disabled, true, 'the owner account cannot turn its own recording off');
  assert.equal(rows[2].props.dependent, true); rows[2].props.control.props.onCheckedChange(true); assert.deepEqual(set, [true]);
  rows[3].props.control.props.children[1].props.onPress(); assert.deepEqual(removed, ['r']);
});

test('without tuners or rules Recording says so, and the defaults only appear once they are read', async () => {
  const nodes = all(await tab({tuners: [], rules: []}));
  assert.equal(nodes.find(node => node.type === 'SectionCard' && node.props.label === 'web.dvrSettings.tuners').props.help, 'web.dvrSettings.noTuners');
  assert.ok(nodes.some(node => node.type === 'StorageNote' && node.props.children === 'web.recordingRules.noRules'));
  assert.ok(!nodes.some(node => node.type === 'FolderExplorer'));
});
