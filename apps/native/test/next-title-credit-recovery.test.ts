import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {NativeTitleCredits} from '../src-next/screens/watch/title-credits.ts';
import {NativeTitleMemory} from '../src-next/screens/watch/title-memory.ts';
import {NativeTitlePages} from '../src-next/screens/watch/title-pages.ts';
import * as titleRelated from '../src-next/screens/watch/title-related.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const credit = (id: string) => ({id, personId: id, name: id, role: 'cast'});
const detail = (revision: number) => ({item: {id: 'movie'}, revision: {catalog: revision, viewer: 1},
  metadata: {credits: [credit(`preview-${revision}`)], creditTotals: {cast: 2, crew: 0}}});

/** Controller orchestration against explicit canonical service ports. The credit
 * owner, revision checks, cursor handling and title memory/pages are real code. */
async function fixture() {
  let host = {revision: 1}, refreshes = 0, held: Promise<void> | undefined, failed = false;
  const hostListeners = new Set<() => void>();
  const reads: {id: string; group: string; cursor?: string; signal: AbortSignal}[] = [];
  class Detail {
    state: any = {phase: 'idle'}; listeners = new Set<() => void>();
    getSnapshot = () => this.state;
    subscribe = (listener: () => void) => {this.listeners.add(listener); return () => this.listeners.delete(listener);};
    publish = (state: any) => {this.state = state; for (const listener of this.listeners) listener();};
    select = async () => {this.publish({phase: 'ready', data: detail(1)});};
    refresh = async () => {
      refreshes++; this.publish({...this.state, phase: 'loading'}); await held;
      if (failed) {this.publish({...this.state, phase: 'error', error: {code: 'unavailable'}}); return;}
      this.publish({phase: 'ready', data: detail(2)});
    };
    dispose() {}
  }
  class Workspace {
    getSnapshot = () => ({phase: 'idle'});
    subscribe = () => () => {};
    dispose() {}
  }
  const {NativeTitleController} = await screenModule(new URL('../src-next/screens/watch/title-controller.ts', import.meta.url), {
    './title-related': titleRelated,
    react: {default: {}},
    '../../../../../packages/client-core/src/detail': {DetailService: Detail, readCreditPage: async (_api: unknown, id: string, group: string, options: {cursor?: string}, signal: AbortSignal) => {
      reads.push({id, group, ...options, signal});
      return {group, revision: {catalog: 2, viewer: 1}, credits: group === 'cast' ? [credit('preview-2'), credit('cast-2')] : [], nextCursor: ''};
    }},
    '../../../../../packages/client-core/src/show-workspace': {ShowWorkspaceService: Workspace},
    '../../../../../packages/client-core/src/container-state': {},
    '../../../../../packages/player-core/src': {sameFence: (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)},
    '../../shell/model': {sameAuthority: (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)},
    './title-memory': {NativeTitleMemory}, './title-pages': {NativeTitlePages}, './title-credits': {NativeTitleCredits},
    './title-episode': {NativeTitleEpisodeDetails: class {dispose() {}}},
  });
  const controller = new NativeTitleController({entry: {id: 'movie', libraryId: 'movies', kind: 'movie'}, api: {}, scope: {}, authority,
    current: () => authority, subscribeCurrent: () => () => {}, requestId: async () => 'id', playback: {start() {}},
    host: () => host, subscribeHost: (listener: () => void) => {hostListeners.add(listener); return () => hostListeners.delete(listener);}});
  return {controller, reads, refreshes: () => refreshes, hold: (promise: Promise<void>) => {held = promise;}, fail: (value: boolean) => {failed = value;},
    leave: () => {host = {revision: host.revision + 1}; for (const listener of hostListeners) listener();}};
}

test('mismatched credits stay out of the row; Retry refreshes Detail before reading its replacement owner', async () => {
  const d = await fixture();
  try {
    await d.controller.open(); const old = d.controller.getSnapshot().credits;
    await d.controller.moreCredits(old);
    const stale = d.controller.getSnapshot().credits;
    assert.equal(stale.error.code, 'stale_continuation');
    assert.deepEqual(stale.credits.map((value: any) => value.id), ['preview-1']);
    assert.equal(d.refreshes(), 0);
    await d.controller.moreCredits(stale);
    const fresh = d.controller.getSnapshot().credits;
    assert.equal(d.refreshes(), 1);
    assert.equal(fresh.error, undefined);
    assert.deepEqual(fresh.credits.map((value: any) => value.id), ['preview-2', 'cast-2']);
    assert.deepEqual(d.reads.map(({id, group, cursor}) => [id, group, cursor]), [['movie', 'cast', undefined], ['movie', 'cast', undefined]]);
    await d.controller.moreCredits(stale);
    assert.equal(d.reads.length, 2, 'old rendered Retry cannot advance the new owner');
  } finally {d.controller.dispose();}
});

test('a failed Detail refresh leaves a usable Retry, and concurrent Retry cannot start a second refresh', async () => {
  const d = await fixture();
  try {
    await d.controller.open(); await d.controller.moreCredits(d.controller.getSnapshot().credits);
    d.fail(true); const stale = d.controller.getSnapshot().credits;
    await d.controller.moreCredits(stale);
    assert.equal(d.controller.getSnapshot().credits, stale, 'the original error/cursor remains retryable');
    assert.equal(d.reads.length, 1);
    d.fail(false); let release!: () => void; d.hold(new Promise<void>(resolve => {release = resolve;}));
    const pending = d.controller.moreCredits(stale); await Promise.resolve();
    await d.controller.moreCredits(stale);
    assert.equal(d.refreshes(), 2, 'only one refresh after the failed first attempt');
    release(); await pending;
    assert.equal(d.reads.length, 2);
    assert.equal(d.controller.getSnapshot().credits.error, undefined);
  } finally {d.controller.dispose();}
});

test('a credit Retry whose route leaves while Detail refreshes cannot start a successor page read', async () => {
  const d = await fixture();
  try {
    await d.controller.open(); await d.controller.moreCredits(d.controller.getSnapshot().credits);
    let release!: () => void; d.hold(new Promise<void>(resolve => {release = resolve;}));
    const pending = d.controller.moreCredits(d.controller.getSnapshot().credits);
    await Promise.resolve(); d.leave(); release(); await pending;
    assert.equal(d.reads.length, 1, 'the old host cannot consume the refreshed owner');
    assert.deepEqual(d.controller.getSnapshot().credits.credits.map((value: any) => value.id), ['preview-2']);
  } finally {d.controller.dispose();}
});
