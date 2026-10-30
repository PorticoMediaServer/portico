import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {NativePendingChanges} from '../src-next/shell/pending-changes.ts';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const scope = {serverId: 'server', origin: 'http://server', authority: 'local', accountId: 'account', profileId: 'profile', familyId: 'family'};
const theme = {isTV: false, type: {body: {}, bodyStrong: {}, caption: {}}, colors: {text: 'white', textSecondary: 'gray', raisedHigh: 'black'}};
const row = {key: 'original-key', category: 'settings', status: 'review', createdMs: 1, canKeep: false,
  secretFields: [{path: 'settings.remote.password', labelKey: 'next.remoteSources.password', maxLength: 512}], destination: {kind: 'settings', server: true}};

async function presentation() {
  const driver = hooks();
  const module = await screenModule(new URL('../src-next/shell/PendingChanges.tsx', import.meta.url), {
    react: driver.react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id, dateTime: () => 'created-time'}},
    '../kit/controls': {Button: 'Button', TextField: 'Field'}, '../kit/overlays': {BottomSheet: 'Sheet'},
    '../kit/states': {StateView: 'State'}, '../kit/theme': {useTheme: () => theme},
  });
  return {...module, render: driver.render};
}

test('the native row displays fixed labels and only approved missing secret fields; cancel clears input', async () => {
  const f = await presentation(), submitted: unknown[] = [];
  const props = {row, busy: false, isCurrent: () => true, checkAgain() {}, review() {}, keep() {}, reenter: (values: unknown) => submitted.push(values)};
  const render = () => f.render(() => f.PendingChangeRow(props));
  let tree = render();
  assert.equal(nodes(tree).some(node => node.props.children?.includes('recovery.keepAsIs')), false);
  assert.equal(JSON.stringify(tree).includes('original-key'), false);
  nodes(tree).find(node => node.props.children?.includes('recovery.reenter')).props.onPress();
  tree = render();
  const field = nodes(tree).find(node => node.type === 'Field');
  assert.equal(field.props.label, 'next.remoteSources.password'); assert.equal(field.props.secureTextEntry, true);
  field.props.onChangeText('memory-only');
  tree = render(); assert.equal(nodes(tree).find(node => node.type === 'Field').props.value, 'memory-only');
  nodes(tree).find(node => node.props.children?.includes('action.cancel')).props.onPress();
  tree = render(); assert.equal(nodes(tree).some(node => node.type === 'Field'), false);
  nodes(tree).find(node => node.props.children?.includes('recovery.reenter')).props.onPress();
  tree = render(); assert.equal(nodes(tree).find(node => node.type === 'Field').props.value, '');
  assert.deepEqual(submitted, []);
});

test('native Save passes only the missing-field map then clears input; consumption is explicitly guarded', async () => {
  const f = await presentation(), submitted: unknown[] = [];
  const props = {row: {...row, canKeep: true}, busy: false, isCurrent: () => true, checkAgain() {}, review() {}, keep() {}, reenter: (values: unknown) => submitted.push(values)};
  const render = () => f.render(() => f.PendingChangeRow(props));
  let tree = render();
  assert.equal(nodes(tree).some(node => node.props.children?.includes('recovery.keepAsIs')), true);
  nodes(tree).find(node => node.props.children?.includes('recovery.reenter')).props.onPress();
  tree = render(); nodes(tree).find(node => node.type === 'Field').props.onChangeText('memory-only');
  tree = render(); nodes(tree).find(node => node.props.children?.includes('action.save')).props.onPress();
  assert.deepEqual(submitted, [{'settings.remote.password': 'memory-only'}]);
  tree = render(); assert.equal(nodes(tree).some(node => node.type === 'Field'), false);
});

test('even a batched close and reopen replaces the input subtree', async () => {
  const f = await presentation();
  const model: any = {dispose() {}};
  const owner = new NativePendingChanges(model, () => scope, () => () => {});
  const render = () => f.render(() => f.PendingChanges({owner, runtime: {}, openDestination() {}}));
  owner.open(); const first = render();
  owner.close(); owner.open(); const next = render();
  assert.notEqual(first.props.key, next.props.key);
  owner.dispose();
});

test('cancelled and replaced native forms reject their stale Save, Cancel and field callbacks', async () => {
  const f = await presentation(), submitted: unknown[] = [];
  let current = true;
  const props = {row, busy: false, isCurrent: () => current, checkAgain() {}, review() {}, keep() {}, reenter: (values: unknown) => submitted.push(values)};
  const render = () => f.render(() => f.PendingChangeRow(props));
  let tree = render();
  nodes(tree).find(node => node.props.children?.includes('recovery.reenter')).props.onPress();
  tree = render(); const oldField = nodes(tree).find(node => node.type === 'Field');
  oldField.props.onChangeText('old-secret');
  tree = render();
  const oldSave = nodes(tree).find(node => node.props.children?.includes('action.save')).props.onPress;
  const oldCancel = nodes(tree).find(node => node.props.children?.includes('action.cancel')).props.onPress;
  oldCancel(); oldSave(); assert.deepEqual(submitted, []);
  tree = render(); nodes(tree).find(node => node.props.children?.includes('recovery.reenter')).props.onPress();
  tree = render(); oldField.props.onChangeText('stale-secret'); oldSave(); oldCancel();
  tree = render(); assert.equal(nodes(tree).find(node => node.type === 'Field').props.value, '');
  nodes(tree).find(node => node.type === 'Field').props.onChangeText('new-secret');
  tree = render(); const save = nodes(tree).find(node => node.props.children?.includes('action.save')).props.onPress;
  current = false; save(); assert.deepEqual(submitted, []);
});

test('mounted native actions keep original identity and closed review cannot navigate a successor', async () => {
  const f = await presentation(), calls: unknown[] = [], destinations: unknown[] = [];
  let finish!: (destination: unknown) => void, disposed = 0;
  const model: any = {subscribe: () => () => {}, snapshot: () => ({revision: 1, rows: [row]}), dispose: () => {disposed++;},
    checkAgain: async (key: string) => {calls.push(['check', key]);},
    reenter: async (key: string, values: unknown, signal: AbortSignal) => {calls.push(['reenter', key, values, signal]);},
    review: (key: string, signal: AbortSignal) => {calls.push(['review', key, signal]); return new Promise(resolve => {finish = resolve;});},
    keepAsIs: async (key: string) => {calls.push(['keep', key]);},
  };
  const owner = new NativePendingChanges(model, () => scope, () => () => {}); owner.open();
  const render = () => f.render(() => f.PendingChangesOpening({owner, runtime: {error() {throw new Error('unexpected_failure');}}, openDestination: (destination: unknown) => destinations.push(destination)}));
  let tree = render(), nativeRow = nodes(tree).find(node => node.type === f.PendingChangeRow);
  nativeRow.props.checkAgain(); await Promise.resolve(); await Promise.resolve();
  assert.deepEqual(calls[0], ['check', 'original-key']);
  tree = render(); nativeRow = nodes(tree).find(node => node.type === f.PendingChangeRow);
  nativeRow.props.reenter({'settings.remote.password': 'memory-only'}); await Promise.resolve(); await Promise.resolve();
  assert.deepEqual(calls[1], ['reenter', 'original-key', {'settings.remote.password': 'memory-only'}, owner.signal()]);
  tree = render(); nativeRow = nodes(tree).find(node => node.type === f.PendingChangeRow);
  nativeRow.props.review();
  const staleClose = tree.props.onOpenChange;
  const signal = owner.signal()!; owner.close(); finish({kind: 'settings', server: true});
  await Promise.resolve(); await Promise.resolve();
  assert.equal(signal.aborted, true); assert.deepEqual(destinations, []); assert.equal(disposed, 0);
  assert.equal(f.render(() => f.PendingChanges({owner, runtime: {}, openDestination() {}})), null);
  owner.open(); staleClose(false); assert.ok(owner.getSnapshot());
  owner.dispose();
});
