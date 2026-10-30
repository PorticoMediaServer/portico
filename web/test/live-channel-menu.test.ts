import test from 'node:test';
import assert from 'node:assert/strict';
import {legacyChannelRow} from '@core/guide/index.ts';
import type {GuideChannel} from '@core/channel-guide.ts';
import {guideChannelEditorDestination, guideChannelMenuEntries, type ChannelMenuActions} from '../src/screens/live/channel-menu.ts';
import {parseLiveScreenSearch} from '../src/screens/live/route-search.ts';
import {readChosenDestination, readDestination} from '../src/screens/read/navigation.ts';

const channel = (provenance: GuideChannel['provenance'], extra: Partial<GuideChannel> = {}) => legacyChannelRow({
  id: 'c1', sourceId: 's1', provenance, name: 'Northwind News', number: '101', group: 'News', generation: 'g1', programmes: [],
  tuneAvailable: true, recordAvailable: true, tuneUnavailableReason: '', recordUnavailableReason: '', favorite: false, hidden: false, preferenceRevision: 3, ...extra,
});
const calls: string[] = [];
const actions = (owner: boolean, busy = false): ChannelMenuActions => ({owner, busy,
  onFavorite: row => calls.push(`favorite:${row.id}`), onHidden: (row, hidden) => calls.push(`hidden:${row.id}:${hidden}`), onEdit: row => calls.push(`edit:${row.id}`)});
const summary = (entries: ReturnType<typeof guideChannelMenuEntries>) => entries.map(entry => 'separator' in entry || 'section' in entry ? entry.id : [entry.id, entry.label, entry.disabled ?? false]);

test('everyone gets Add to Favorites; owners also get Hide channel, and Edit channel on Library Channels', () => {
  assert.deepEqual(summary(guideChannelMenuEntries(channel('live-source'), actions(false))), [['favorite', 'Add to Favorites', false]]);
  assert.deepEqual(summary(guideChannelMenuEntries(channel('live-source', {favorite: true}), actions(true))),
    [['favorite', 'Remove from Favorites', false], ['hide', 'Hide channel', false]]);
  assert.deepEqual(summary(guideChannelMenuEntries(channel('library-channel'), actions(true))),
    [['favorite', 'Add to Favorites', false], ['edit', 'Edit channel', false]]);
  // A member never edits a Library Channel from the guide.
  assert.deepEqual(summary(guideChannelMenuEntries(channel('library-channel'), actions(false))), [['favorite', 'Add to Favorites', false]]);
});

test('a hidden channel is shown again, and a preference already in flight blocks the next one', () => {
  calls.length = 0;
  const hidden = channel('live-source', {serverHidden: true});
  const entries = guideChannelMenuEntries(hidden, actions(true));
  assert.deepEqual(summary(entries), [['favorite', 'Add to Favorites', false], ['hide', 'Show channel', false]]);
  for (const entry of entries) if ('onSelect' in entry) entry.onSelect();
  assert.deepEqual(calls, ['favorite:c1', 'hidden:c1:false']);
  assert.deepEqual(summary(guideChannelMenuEntries(hidden, actions(true, true))), [['favorite', 'Add to Favorites', true], ['hide', 'Show channel', true]]);
});

test('the guide keeps an owner’s Show hidden channels in its address, and nothing else is accepted', () => {
  assert.deepEqual(parseLiveScreenSearch({hidden: true}), {hidden: true});
  assert.deepEqual(parseLiveScreenSearch({hidden: false}), {});
  assert.throws(() => parseLiveScreenSearch({hidden: 'yes'}));
});

test('a shelf Read opens the server’s chosen version; the title page still names its own', () => {
  assert.deepEqual(readChosenDestination('book-1'), {to: '/read/$itemId', params: {itemId: 'book-1'}, search: {}});
  assert.deepEqual(readDestination('book-1', 'epub-2'), {to: '/read/$itemId', params: {itemId: 'book-1'}, search: {sourceId: 'epub-2'}});
});

import {setServerChannelHidden} from '@core/server-channel-visibility.ts';
import {settingsRoute} from '../src/kit/settings/search.ts';

test('Edit channel retains the exact Settings editor target only on the server Live page', () => {
  const destination = guideChannelEditorDestination('lc-1');
  assert.equal(destination.to, '/settings');
  assert.deepEqual(settingsRoute(destination.search, () => true), {scope: 'server', section: 'live', row: 'live.lib.channels', channelId: 'lc-1'});
  assert.equal(settingsRoute({scope: 'account', section: 'profile', channelId: 'lc-1'}, () => true).channelId, undefined);
});

test('profile hidden state never supplies the owner Show action', () => {
  assert.equal(summary(guideChannelMenuEntries(channel('live-source', {hidden: true}), actions(true)))[1]?.[1], 'Hide channel');
});

test('server hide uses the exact map revision and preserves inherited number and guide mapping; Undo is fenced', async () => {
  const calls: {path: string; method?: string; body?: any}[] = [];
  let revision = 4, hidden = false;
  const page = () => ({sourceId: 's1', generation: 'g1', revision, nextCursor: '', items: [{channelId: 'c1', name: 'One', sourceNumber: '1', number: '1', overrideNumber: '', group: '', guideChannelId: 'epg-one', hidden, position: 0, overridden: false}]});
  const api = {async request<T>(path: string, method?: string, body?: any): Promise<T> {
    calls.push({path, method, body});
    if (method === 'PUT') {assert.equal(body.expectedRevision, revision);hidden = body.overrides[0].hidden;revision++;}
    return {protocolVersion: '1.0', serverId: 'server', result: page()} as T;
  }};
  const args = [api, 'server', 's1', 'c1'] as const;
  const signal = new AbortController().signal;
  const receipt = await setServerChannelHidden(...args, true, () => 'operation', signal, () => true);
  assert.match(calls[0]!.path, /channel-map\?channelId=c1&limit=1$/);
  assert.deepEqual(calls[1]!.body, {expectedRevision: 4, operationId: 'operation', overrides: [{channelId: 'c1', number: '', guideChannelId: 'epg-one', hidden: true}]});
  assert.deepEqual(receipt, {revision: 5, hidden: true, previousHidden: false});
  revision++; // A concurrent settings edit defeats retained Undo.
  await assert.rejects(setServerChannelHidden(...args, false, () => 'undo', signal, () => true, receipt), {message: 'channel_map_changed'});
  assert.equal(calls.filter(call => call.method === 'PUT').length, 1);
  const fresh = {...receipt, revision};
  await setServerChannelHidden(...args, false, () => 'undo', signal, () => true, fresh);
  assert.equal(hidden, false);
});

test('a retired channel action cannot write after its exact lookup', async () => {
  let current = true, calls = 0;
  const api = {async request<T>(): Promise<T> {calls++;current = false;return {} as T;}};
  await assert.rejects(setServerChannelHidden(api, 'server', 's1', 'c1', true, () => 'operation', new AbortController().signal, () => current));
  assert.equal(calls, 1);
});
