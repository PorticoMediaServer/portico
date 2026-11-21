// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {createTheme} from '../src-next/kit/theme.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {isFirstScan, observeFirstScan} from '../src-next/screens/library/first-scan.ts';

const react = {...React, default: React};
const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : node?.props ? [node, ...descendants(node.props.children)] : [];
const text = (node: any): string => Array.isArray(node) ? node.map(text).join('') : typeof node === 'string' ? node : node?.props ? text(node.props.children) : '';
const tick = () => new Promise<void>(resolve => setImmediate(resolve));

// ---- Library, first scan (states.html #15) ----

const job = (id: string, status = 'running') => ({id, status, libraryId: 'films', actions: []});
const managed = (lastScan: unknown) => ({phase: 'ready', data: {library: {id: 'films', lastScan}}});
const history = (...items: any[]) => ({phase: 'ready', data: {items, nextCursor: ''}});

test('a first scan is the owner library whose one complete job is its own queued or running scan', () => {
  const snapshot = (selected: any, jobs: any) => ({selected, jobs}) as any;
  assert.equal(isFirstScan(snapshot(managed(job('a')), history(job('a'))), 'films'), true);
  assert.equal(isFirstScan(snapshot(managed(job('a', 'queued')), history(job('a', 'queued'))), 'films'), true);
  assert.equal(isFirstScan(snapshot(managed(job('a', 'succeeded')), history(job('a', 'succeeded'))), 'films'), false, 'a finished scan is not a first scan');
  assert.equal(isFirstScan(snapshot(managed(job('b')), history(job('a'), job('b'))), 'films'), false, 'earlier history means it was scanned before');
  assert.equal(isFirstScan(snapshot(managed(job('a')), {phase: 'ready', data: {items: [job('a')], nextCursor: 'more'}}), 'films'), false, 'an incomplete history proves nothing');
  assert.equal(isFirstScan(snapshot(managed(job('a')), history(job('a'))), 'other'), false, 'another library');
  assert.equal(isFirstScan(snapshot(managed(null), history()), 'films'), false);
  assert.equal(isFirstScan(snapshot({phase: 'loading', data: null}, history(job('a'))), 'films'), false);
});

test('a failed management read is simply not a first scan, and stopping silences the observer', async () => {
  const seen: boolean[] = [];
  const api = {request: async () => {throw Object.assign(new Error('refused'), {code: 'access_refused'});}} as any;
  const stop = observeFirstScan({api, scope: {serverId: 's', viewerId: 'v'}}, 'films', value => seen.push(value));
  await tick(); await tick();
  assert.ok(seen.every(value => value === false), 'never a first scan');
  stop(); seen.length = 0; await tick();
  assert.deepEqual(seen, []);
});

test('the first-scan read is made once while the titles page is empty, follows the answer and is released', async () => {
  const {useFirstScanning} = await screenModule(new URL('../src-next/pages/shared/library/useFirstScanning.ts', import.meta.url), {react});
  const observed: {id: string; onChange: (value: boolean) => void; stopped: boolean}[] = [];
  const port = {observe: (id: string, onChange: (value: boolean) => void) => {const entry = {id, onChange, stopped: false}; observed.push(entry); return () => {entry.stopped = true;};}, openActivity: () => {}};
  let enabled = true, scanning: boolean | undefined;
  const host = createFabricHookHost(() => {scanning = useFirstScanning(port, 'films', enabled);});
  await host.mount();
  try {
    assert.equal(observed.length, 1);
    assert.equal(observed[0]!.id, 'films');
    assert.equal(scanning, false, 'not a first scan until the read says so');
    observed[0]!.onChange(true); await host.rerender();
    assert.equal(scanning, true);
    enabled = false; await host.rerender();
    assert.equal(scanning, false, 'a genres page, or a page with titles, is never a first scan');
    assert.equal(observed[0]!.stopped, true);
    assert.equal(observed.length, 1, 'nothing is read while disabled');
  } finally {await host.unmount();}
  const member = {current: [] as string[]};
  const hostMember = createFabricHookHost(() => {member.current.push(String(useFirstScanning(undefined, 'films', true)));});
  await hostMember.mount();
  try {assert.deepEqual(member.current, ['false'], 'a member has no port and reads nothing');} finally {await hostMember.unmount();}
});
