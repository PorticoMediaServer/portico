import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsChannelActionsSource} from '../src/server-admin/channel-actions-source.ts';
import type {SettingsLibraryChannelsSource} from '../src/server-admin/channels-source.ts';
import {parseLibraryChannel} from '../src/library-channels.ts';

const channel = (id: string, revision: number) => parseLibraryChannel({
  config: {protocolVersion: '1.0', id, name: id, description: '', enabled: true, position: 0, timezone: 'UTC', seed: 'seed', defaultRuleId: 'r1', viewerAccess: 'owner-only', logoItemId: '', templateId: '',
    quality: {mode: 'automatic', maxBitrate: 8000000, maxHeight: 1080, allowLossy: true, allowHdrToSdr: true},
    overlay: {enabled: false, corner: 'br', sizePercent: 10, insetPercent: 5, treatment: 'none'},
    rules: [{id: 'r1', name: 'Rule', query: {libraryIds: ['library'], kinds: ['movie'], recentDays: 0, limit: 10, order: 'title'}, mode: 'sequential', episodeMode: 'none', exhaustion: 'loop', deduplicationWindow: 0, maxConsecutive: 1}], blocks: []},
  revision, state: 'ok', healthCode: '', generation: 'g1', generatedThrough: '', candidateCount: 0, unresolvedDurationCount: 0, replacementBoundary: '',
});
function setup() {
  const list = [channel('a', 1), channel('b', 2), channel('c', 3), channel('d', 4)], snapshot = {phase: 'ready', channels: list};
  const sent: {channels: {id: string; expectedRevision: number}[]; requestId: string}[] = [];
  const reader = {getSnapshot: () => snapshot, load: async () => {}} as unknown as SettingsLibraryChannelsSource;
  const actions = new SettingsChannelActionsSource({request: async <T>(path: string, _method?: string, body?: unknown): Promise<T> => {
    assert.equal(path, '/v1/admin/library-channels/reorder');sent.push(body as typeof sent[number]);
    return {serverId: 'server', protocolVersion: '1.0', saved: true} as T;
  }}, 'server', reader, () => true, error => {throw error;}, () => 'order-' + sent.length);
  return {list, sent, actions};
}

test('moving a channel several places is one reorder carrying every revision', async () => {
  const {list, sent, actions} = setup();
  try {
    assert.equal(await actions.moveTo(list[3], 0), true);
    assert.equal(sent.length, 1);
    assert.deepEqual(sent[0].channels, [{id: 'd', expectedRevision: 4}, {id: 'a', expectedRevision: 1}, {id: 'b', expectedRevision: 2}, {id: 'c', expectedRevision: 3}]);
  } finally {actions.dispose();}
});

test('an adjacent move is unchanged, and impossible targets send nothing', async () => {
  const {list, sent, actions} = setup();
  try {
    assert.equal(await actions.perform(list[1], 'move', 1), true);
    assert.deepEqual(sent[0].channels.map(row => row.id), ['a', 'c', 'b', 'd']);
    for (const target of [-1, 4, 1.5, 0 / 0, 1]) assert.equal(await actions.moveTo(list[1], target), false, String(target));
    assert.equal(await actions.perform(list[0], 'move', -1), false);
    assert.equal(sent.length, 1);
  } finally {actions.dispose();}
});
