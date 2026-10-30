import test from 'node:test';
import assert from 'node:assert/strict';
import {parseOptions} from '../../../packages/client-core/src/playback-v1/types.ts';
import {NativeVersionChooserController} from '../src-next/screens/watch/version-destination.ts';
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const offered = parseOptions({itemId: 'movie', resume: {positionMs: 4500, partIndex: 0}, versions: [{id: 'v1'}, {id: 'v2'}]});
function fixture() {
  let host = true, source = true, selection: any, input: any, release!: () => void, calls = 0, disposed = 0;
  const pending = new Promise<void>(resolve => {release = resolve;});
  const destination = new NativeVersionChooserController({capture: {entry: {id: 'movie', kind: 'movie', title: 'Actual movie', playback: {itemId: 'movie', startSeconds: 17}},
    authority, routeRevision: 2, expectedFence: undefined, current: () => host}, options: offered,
    rows: [{id: 'v1', heading: 'First', detail: ''}, {id: 'v2', heading: 'Second', detail: ''}],
    hostCurrent: () => host, sourceCurrent: () => source,
    prepare: async () => {throw new Error('disc title preparation is not configured in this destination fixture');},
    reread: async () => {throw new Error('disc title reread is not configured in this destination fixture');},
    preview: async () => {throw new Error('disc title preview is not configured in this destination fixture');},
    start: async (next, choice) => {input = next; selection = choice; calls++; await pending;}, onDispose: () => {disposed++;}});
  return {destination, release, calls: () => calls, selection: () => selection, input: () => input, disposed: () => disposed,
    host: (value: boolean) => {host = value;}, source: (value: boolean) => {source = value;}};
}

test('one pending choice retains its pure host lifetime through its own accepted output and fresh resume', async () => {
  const d = fixture();
  try {
    await d.destination.choose('unknown'); assert.equal(d.calls(), 0);
    const pending = d.destination.choose('v2'); assert.equal(d.calls(), 1);
    assert.equal(d.input().target.startSeconds, 4.5); assert.equal(d.selection().versionId, 'v2');
    assert.equal(d.selection().options, offered); assert.equal(d.selection().expectedFence, undefined);
    d.source(false); assert.equal(d.selection().lifetime.isCurrent(), true); assert.equal(d.destination.isCurrent(), true);
    await d.destination.choose('v1'); assert.equal(d.calls(), 1);
    d.release(); await pending; assert.equal(d.destination.isCurrent(), false);
  } finally {d.release(); d.destination.dispose();}
});

for (const closing of ['dispose', 'host'] as const) test(`real chooser lifetime retires while its command is pending: ${closing}`, async () => {
  const d = fixture(), pending = d.destination.choose('v2');
  try {
    if (closing === 'dispose') d.destination.dispose(); else d.host(false);
    assert.equal(d.selection().lifetime.isCurrent(), false); assert.equal(d.destination.isCurrent(), false);
    if (closing === 'dispose') assert.equal(d.selection().lifetime.signal.aborted, true);
    d.release(); await pending;
    d.destination.dispose(); d.destination.dispose(); assert.equal(d.disposed(), 1);
  } finally {d.release(); d.destination.dispose();}
});

test('a source replaced before selection never dispatches the retained version choice', async () => {
  const d = fixture();
  try {d.source(false); await d.destination.choose('v2'); assert.equal(d.calls(), 0);}
  finally {d.release(); d.destination.dispose();}
});
