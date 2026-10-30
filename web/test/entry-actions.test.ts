import test from 'node:test';
import assert from 'node:assert/strict';
import {addToDestinationEffect, deleteTitleEffect, entryActions} from '../src/shell/entry-actions.ts';

const target = {libraryId: 'library', itemId: 'item', kind: 'movie' as const, title: 'Film'};
const ports = {api: {request: async () => ({})}, serverId: 'server', viewerId: 'viewer', isCurrent: () => true, requestId: async () => 'operation'};

test('the shell entry-action store opens one surface and closes it', () => {
  const seen: (string | null)[] = [];
  const off = entryActions.subscribe(() => seen.push(entryActions.getSnapshot()?.kind ?? null));
  entryActions.openDelete(target, null, ports);
  assert.equal(entryActions.getSnapshot()?.kind, 'delete');
  entryActions.close();
  assert.equal(entryActions.getSnapshot(), null);
  off();
  assert.deepEqual(seen, ['delete', null]);
});

test('a delete effect refuses a retired screen and opens for a current one', async () => {
  entryActions.close();
  await assert.rejects(deleteTitleEffect({...ports, isCurrent: () => false})(target, null as never), {name: 'AbortError'});
  assert.equal(entryActions.getSnapshot(), null);
  await deleteTitleEffect(ports)(target, null as never);
  assert.equal(entryActions.getSnapshot()?.kind, 'delete');
  entryActions.close();
});

test('a playlist destination effect opens the shared picker only for a current screen', async () => {
  entryActions.close();
  await assert.rejects(addToDestinationEffect({...ports, isCurrent: () => false}, 'playlist')(target, null as never), {name: 'AbortError'});
  assert.equal(entryActions.getSnapshot(), null);
  await addToDestinationEffect(ports, 'playlist')(target, null as never);
  assert.equal(entryActions.getSnapshot()?.kind, 'picker');
  entryActions.close();
});
