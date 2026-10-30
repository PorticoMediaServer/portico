// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {playbackUserMessage} from '../../../packages/client-core/src/playback-messages';
import {createTheme} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const text = (node: any): string => Array.isArray(node) ? node.map(text).join('') : typeof node === 'string' ? node : node?.props ? text(node.props.children) : '';
function failure(scope = 'account', streamLimit: any = {scope: 'account', playing: 3, limit: 2}, exhausted = () => false) {
  let live = true, listener: ((rows: readonly any[]) => void) | undefined;
  const calls = {retry: 0, cancel: 0, remote: 0, stopped: [] as any[]};
  const row = {sessionId: 'original-session', revision: '4', state: 'playing', positionMs: 0, kind: 'video', actions: ['stop'], device: {id: 'original-device', name: 'Original device'}};
  const port = {scope, code: 'stream_limit_reached', retryable: true, streamLimit,
    error: {code: 'stream_limit_reached', reason: scope === 'account' ? 'owner_account_cap' : 'owner_server_cap'},
    get automaticRecoveryExhausted() {return exhausted();}, isCurrent: () => live,
    retry: async () => {calls.retry++; live = false;}, cancel: () => {calls.cancel++; live = false;},
    remote: async () => {calls.remote++; return {items: [row], nextCursor: ''};},
    subscribeSessions: (next: typeof listener) => {listener = next; return () => {listener = undefined;};},
    stop: async (value: any) => {calls.stopped.push(value);}};
  return {port, row, calls, retire: () => {live = false;}, publish: (rows: readonly any[]) => listener?.(rows)};
}
async function fixture(input: any, width = 390) {
  let props = input, tree: any;
  const {TouchStartFailure} = await screenModule(new URL('../src-next/shell/TouchStartFailure.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Text: 'Text', View: 'View', FlatList: 'List', useWindowDimensions: () => ({width, height: 844})},
    '../../../../packages/client-core/src/playback-messages': {playbackUserMessage},
    '../kit/controls': {Button: 'Button'}, '../kit/Icon': {Icon: 'Icon'}, '../kit/overlays': {BottomSheet: 'Sheet'},
    '../kit/overlays/surface': {alpha: (value: string) => value}, '../kit/states': {CountdownRing: 'Countdown'},
    '../kit/theme': {useTheme: () => createTheme({isTV: false, width})},
  });
  const runtime = {text: defaultI18n, getSnapshot: () => ({serverName: 'Original server'})};
  const host = createFabricHookHost(() => {tree = TouchStartFailure({...props, runtime});});
  await act(() => host.mount());
  return {tree: () => tree,
    button: (id: string) => nodes(tree.props.footer).find(node => node.props.testID === id),
    list: () => nodes(tree.props.children).find(node => node.type === 'List'),
    set: async (next: any) => {await act(async () => {props = next; await host.rerender();});},
    close: () => host.unmount()};
}

for (const width of [390, 1024]) test(`touch account-capacity sheet at ${width} uses real slot facts and retained scoped controls`, async () => {
  const f = failure(), d = await fixture({failure: f.port}, width);
  try {
    assert.equal(d.tree().props.title, defaultI18n.t('next.states.streamLimit', {count: 3}));
    assert.equal(d.tree().props.description, defaultI18n.t('next.states.streamLimitBody', {count: 2}));
    assert.equal(d.button('touch-start-retry'), undefined, 'account recovery is the retained runtime gate, not manual new Play');
    assert.equal(f.calls.remote, 1);
    const list = d.list(), row = list.props.renderItem({item: list.props.data[0], index: 0});
    assert.match(text(row), /Something you can’t see on this profile/);
    const stop = nodes(row).find(node => node.props.testID === 'touch-start-stop-original-session');
    await act(() => stop.props.onPress());
    assert.equal(f.calls.stopped[0], f.row, 'the exact scoped offered object reaches Stop');
    assert.equal(f.calls.retry, 0);
  } finally {await d.close();}
});

test('server capacity keeps only the original manual retry, without reading another account list', async () => {
  const f = failure('server', undefined), d = await fixture({failure: f.port});
  try {
    assert.equal(f.calls.remote, 0); assert.equal(d.list(), undefined);
    await act(() => d.button('touch-start-retry').props.onPress());
    assert.equal(f.calls.retry, 1);
    await d.set({});
    assert.equal(f.calls.cancel, 0, 'intentional successful retry cleared its original port before new output');
  } finally {await d.close();}
});

test('retained Back and Stop callbacks cannot operate on a replacement opening', async () => {
  const old = failure(), d = await fixture({failure: old.port});
  try {
    const back = d.tree().props.onOpenChange, list = d.list();
    const stop = nodes(list.props.renderItem({item: old.row, index: 0})).find(node => node.type === 'Button');
    const next = failure(); old.retire();
    await d.set({failure: next.port});
    await act(() => {back(false, 'back'); stop.props.onPress();});
    assert.equal(next.calls.cancel, 0); assert.equal(next.calls.stopped.length, 0); assert.equal(old.calls.stopped.length, 0);
  } finally {await d.close();}
});

test('a controls refresh invalidates a previously drawn Stop without guessing capacity from row count', async () => {
  const f = failure(), d = await fixture({failure: f.port});
  try {
    const stop = nodes(d.list().props.renderItem({item: f.row, index: 0})).find(node => node.type === 'Button');
    await act(() => f.publish([]));
    await act(() => stop.props.onPress());
    assert.equal(f.calls.stopped.length, 0);
    assert.equal(d.tree().props.title, defaultI18n.t('next.states.streamLimit', {count: 3}));
  } finally {await d.close();}
});

test('Back and unmount cancel a live original wait, while an admitted output is left alone', async () => {
  const f = failure(), d = await fixture({failure: f.port});
  await act(() => d.tree().props.onOpenChange(false, 'back'));
  assert.equal(f.calls.cancel, 1); await d.close(); assert.equal(f.calls.cancel, 1);
  const accepted = failure(), a = await fixture({failure: accepted.port});
  accepted.retire(); await a.set({}); await a.close(); assert.equal(accepted.calls.cancel, 0);
});

test('conversion countdown invokes only the offered retryNow and cancellation ports', async () => {
  let live = true, retries = 0, cancelled = 0;
  const wait = {retryAt: Date.now() + 20000, attempt: 1, isCurrent: () => live,
    retryNow: () => {retries++;}, cancel: () => {cancelled++;live = false;}};
  const d = await fixture({wait});
  try {
    assert.equal(d.tree().props.description, defaultI18n.t('next.states.convertingBusyBody'));
    assert.ok(nodes(d.tree().props.children).some(node => node.type === 'Countdown'));
    await act(() => d.button('touch-start-retry').props.onPress());
    assert.equal(retries, 1);
    await act(() => d.button('touch-start-back').props.onPress()); assert.equal(cancelled, 1);
  } finally {await d.close();}
});

test('an exhausted account wait exposes its original explicit retry without a new server event or read', async t => {
  t.mock.timers.enable({apis: ['setInterval', 'Date'], now: 100000});
  const f = failure('account', {scope: 'account', playing: 3, limit: 2}, () => Date.now() >= 130000);
  const d = await fixture({failure: f.port});
  try {
    assert.equal(d.button('touch-start-retry'), undefined);
    assert.equal(f.calls.remote, 1);
    await act(() => {t.mock.timers.tick(30000);});
    assert.ok(d.button('touch-start-retry'));
    assert.ok(d.button('touch-start-back'));
    assert.equal(d.tree().props.title, defaultI18n.t('next.states.streamLimit', {count: 3}));
    assert.equal(d.tree().props.description, defaultI18n.t('next.states.streamLimitBody', {count: 2}));
    assert.equal(f.calls.remote, 1, 'the local display clock never refreshes or starts backend work');
    const stop = nodes(d.list().props.renderItem({item: f.row, index: 0})).find(node => node.type === 'Button');
    await act(() => stop.props.onPress());
    assert.equal(f.calls.stopped[0], f.row);assert.equal(f.calls.retry, 0);
    assert.ok(d.button('touch-start-retry'), 'explicit Stop does not replenish the automatic recovery budget');
    await act(() => d.button('touch-start-retry').props.onPress());
    assert.equal(f.calls.retry, 1);assert.equal(f.calls.cancel, 0);
  } finally {await d.close();t.mock.timers.reset();}
});
