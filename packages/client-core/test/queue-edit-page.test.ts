/** Actual V1QueuePlayer/QueueClient bounded edit reader vectors. Campaign source only; UNRUN. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '../src/index.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {V1QueuePlayer} from '../src/playback-v1/v1-queue-player.ts';
import {queueItemInput} from '../src/queue-controller.ts';
import type {V1Http, V1Request, V1Response} from '../src/playback-v1/http.ts';

async function fixture(alter?: (request: V1Request, response: V1Response) => Promise<V1Response | void>, count = 320) {
  const server = new FakePlaybackServer({itemKind: () => 'movie'}), inner = fakeHttp(server), calls: V1Request[] = [];
  const http: V1Http = {send: async request => {calls.push(request); const response = await inner.send(request as never); return (await alter?.(request, response) ?? response) as never;}};
  let serial = 0, keyHook: (() => void) | undefined;
  const api = new V1PlaybackApi(http, {key: () => `api-${++serial}`});
  const playback = new PlaybackService(api, () => `run-${++serial}`);
  const player = new V1QueuePlayer({http, api, playback, viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'}, key: () => {keyHook?.(); return `edit-key-${++serial}`;}});
  const stop = player.connect();
  await player.playEntries(Array.from({length: count}, (_, i) => queueItemInput(`item-${i}`)));
  const session = player.currentSession()!, view = player.getSnapshot().view!;
  const target = {sessionId: session.id, generation: session.presentation.generation, playbackRun: playback.getSnapshot().playbackRun,
    itemId: session.itemId!, versionId: session.versionId!, queueId: session.queue!.queueId, entryId: session.queue!.entryId};
  const signal = new AbortController().signal, context = {signal, isCurrent: () => true, readOutput: () => ({kind: 'local' as const, target})};
  return {player, playback, calls, stop, target, context, revision: view.queue.revision, setKeyHook: (hook: () => void) => {keyHook = hook;}};
}
async function distant(reader: ReturnType<V1QueuePlayer['captureEditWindow']>) {
  let page = await reader.read();
  for (let i = 0; i < 5; i++) page = await reader.next(page);
  return page;
}

test('later than 200 uses bounded pages, private receipts and same-range removal readback', async () => {
  const f = await fixture();
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, f.context), first = await reader.read();
    const second = await reader.next(first); assert.equal(second.canPrevious, true);
    const again = await reader.previous(second); assert.equal(again.start, first.start); assert.equal(again.canPrevious, false);
    await assert.rejects(reader.next({...again}));
    const fresh = f.player.captureEditWindow(f.target, f.revision, f.context), page = await distant(fresh);
    assert.equal(page.start, 250); assert.equal(page.items.length, 50);
    const sibling = await fresh.previous(page);
    const staleNav = f.player.captureEditWindow(f.target, f.revision, f.context), staleFirst = await staleNav.read(), staleSecond = await staleNav.next(staleFirst);
    await assert.rejects(staleNav.next(staleFirst), /navigation/); assert.equal(staleSecond.start, 50);
    const reader2 = f.player.captureEditWindow(f.target, f.revision, f.context), other = await distant(reader2);
    await assert.rejects(reader2.edit(sibling, {action: 'remove', entryId: sibling.items[1]!.entryId}));
    const receipt = await fresh.edit(sibling, {action: 'remove', entryId: sibling.items[1]!.entryId});
    assert.equal(receipt.start, 200); assert.equal(receipt.total, 319);
    await assert.rejects(fresh.edit(page, {action: 'remove', entryId: page.items[1]!.entryId}));
    assert.ok(f.calls.filter(call => call.path.includes('/entries?')).every(call => /limit=50(?:&|$)/.test(call.path)));
  } finally {f.stop();}
});

test('page edits reject current, unavailable and cross-page rows without dispatch', async () => {
  const f = await fixture();
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, f.context), page = await reader.read();
    const writes = () => f.calls.filter(call => call.method === 'DELETE' || call.path.includes(':move')).length;
    const before = writes(); await assert.rejects(reader.edit(page, {action: 'remove', entryId: f.target.entryId})); assert.equal(writes(), before);
    const next = f.player.captureEditWindow(f.target, f.revision, f.context), first = await next.read(), second = await next.next(first);
    await assert.rejects(next.edit(second, {action: 'reorder', entryIds: [first.items[49]!.entryId, second.items[0]!.entryId]})); assert.equal(writes(), before);
  } finally {f.stop();}
});

test('bad page metadata, duplicates and absent original facts never mint receipts', async () => {
  for (const fault of ['start', 'total', 'revision', 'cursor', 'duplicate', 'position', 'available'] as const) {
    const f = await fixture(async (request, response) => {
      if (!request.path.includes('/entries?')) return;
      const body: any = structuredClone(response.body), page = body.page, items = body.items;
      if (fault === 'start') delete page.start; if (fault === 'total') delete page.total; if (fault === 'revision') delete page.revision;
      if (fault === 'cursor') page.nextCursor = 'from:999'; if (fault === 'duplicate') items[1].entryId = items[0].entryId;
      if (fault === 'position') delete items[0].position; if (fault === 'available') delete items[0].available;
      return {...response, body};
    });
    try {const reader = f.player.captureEditWindow(f.target, f.revision, f.context); await assert.rejects(reader.read());}
    finally {f.stop();}
  }
});

test('aborted, reentrant and replaced caller cannot issue or publish page operations', async () => {
  const f = await fixture();
  try {
    const abort = new AbortController(); abort.abort(); assert.throws(() => f.player.captureEditWindow(f.target, f.revision, {...f.context, signal: abort.signal}));
    let current = true; const reader = f.player.captureEditWindow(f.target, f.revision, {...f.context, isCurrent: () => current});
    const page = await reader.read(); current = false; await assert.rejects(reader.next(page), {name: 'AbortError'});
    const controller = new AbortController(); let invoked = 0;
    assert.throws(() => f.player.captureEditWindow(f.target, f.revision, {...f.context, signal: controller.signal, isCurrent: () => {if (++invoked === 1) controller.abort(); return true;}}), {name: 'AbortError'});
  } finally {f.stop();}
});

test('one-page reorder uses the original occurrence IDs and verifies the reminted readback', async () => {
  const f = await fixture();
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, f.context), page = await distant(reader);
    const before = page.items.slice(3, 6).map(row => row.entryId), originalItem = page.items[3]!.itemId;
    const receipt = await reader.edit(page, {action: 'reorder', entryIds: [before[1]!, before[2]!, before[0]!]});
    assert.equal(receipt.start, page.start); assert.equal(receipt.total, page.total);
    const moved = receipt.items.slice(3, 6); assert.equal(moved[0]!.entryId, before[1]); assert.equal(moved[1]!.entryId, before[2]); assert.equal(moved[2]!.itemId, originalItem);
    assert.equal(f.player.currentSession()?.queue?.entryId, f.target.entryId);
  } finally {f.stop();}
});

test('conflict consumes all sibling grants and does not replay an ambiguous mutation', async () => {
  let conflict = false;
  const f = await fixture(async (request, response) => conflict && request.method === 'DELETE'
    ? {status: 412, headers: {}, body: {error: {code: 'conflict', retry: 'after_refresh'}}} as V1Response : response);
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, f.context), first = await reader.read(), second = await reader.next(first);
    conflict = true; await assert.rejects(reader.edit(second, {action: 'remove', entryId: second.items[1]!.entryId}));
    const writes = f.calls.filter(call => call.method === 'DELETE').length;
    await assert.rejects(reader.edit(first, {action: 'remove', entryId: first.items[1]!.entryId}));
    assert.equal(f.calls.filter(call => call.method === 'DELETE').length, writes);
  } finally {f.stop();}
});

async function bounded<T>(promise: Promise<T>, ms = 3000): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {return await Promise.race([promise, new Promise<never>((_, reject) => {timer = setTimeout(() => reject(new Error('fixture timed out')), ms);})]);}
  finally {if (timer) clearTimeout(timer);}
}
test('abort after a positional response prevents publication and commands', async () => {
  let hold = false, entered!: () => void, release!: () => void;
  const arrived = new Promise<void>(resolve => {entered = resolve;}), gate = new Promise<void>(resolve => {release = resolve;});
  const f = await fixture(async (request, response) => {if (hold && request.path.includes('/entries?')) {entered(); await gate;} return response;});
  const controller = new AbortController(); let pending: Promise<unknown> | undefined;
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, {...f.context, signal: controller.signal});
    hold = true; pending = reader.read(); const refused = assert.rejects(pending, {name: 'AbortError'}); await bounded(arrived);
    controller.abort(); release(); await bounded(refused);
    assert.equal(f.calls.filter(call => call.method === 'DELETE' || call.path.includes(':move')).length, 0);
  } finally {controller.abort(); release(); try {if (pending) await bounded(pending.then(() => undefined, () => undefined));} finally {f.stop();}}
});

test('host output must be original local evidence and reentrant output drift retires admission', async () => {
  const f = await fixture();
  try {
    assert.throws(() => f.player.captureEditWindow(f.target, f.revision, {...f.context, isCurrent: (() => undefined) as any}), {name: 'AbortError'});
    for (const kind of ['remote', 'unknown'] as const) assert.throws(() => f.player.captureEditWindow(f.target, f.revision, {...f.context, readOutput: () => ({kind})}), {name: 'AbortError'});
    assert.throws(() => f.player.captureEditWindow(f.target, f.revision, {...f.context, readOutput: () => {throw new Error('unavailable');}}), {name: 'AbortError'});
    assert.throws(() => f.player.captureEditWindow(f.target, f.revision, {...f.context, readOutput: () => ({kind: 'local', target: {...f.target, playbackRun: f.target.playbackRun + 1}})}), {name: 'AbortError'});
    const abort = new AbortController(); let calls = 0;
    assert.throws(() => f.player.captureEditWindow(f.target, f.revision, {...f.context, signal: abort.signal, readOutput: () => {calls++; abort.abort(); return {kind: 'local', target: f.target};}}), {name: 'AbortError'});
    assert.equal(calls, 1);
  } finally {f.stop();}
});

test('missing original header anchor position cannot pass as position zero', async () => {
  let malformed = false;
  const f = await fixture(async (request, response) => {
    if (!malformed || request.method !== 'GET' || request.path !== `/v1/queues/${f.target.queueId}`) return;
    const body: any = structuredClone(response.body); delete body.current.position; return {...response, body};
  });
  try {const reader = f.player.captureEditWindow(f.target, f.revision, f.context); malformed = true; await assert.rejects(reader.read());}
  finally {f.stop();}
});

test('a remote transition after a receipt retires it before another request', async () => {
  const f = await fixture(); let remote = false;
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, {...f.context, readOutput: () => remote ? {kind: 'remote' as const} : {kind: 'local' as const, target: f.target}});
    const page = await reader.read(), before = f.calls.length; remote = true;
    await assert.rejects(reader.next(page), {name: 'AbortError'}); assert.equal(f.calls.length, before);
  } finally {f.stop();}
});

test('a reentrant command-key callback cannot dispatch after output retirement', async () => {
  const f = await fixture(); let remote = false;
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, {...f.context, readOutput: () => remote ? {kind: 'remote' as const} : {kind: 'local' as const, target: f.target}});
    const page = await reader.read(), before = f.calls.length; f.setKeyHook(() => {remote = true;});
    await assert.rejects(reader.edit(page, {action: 'remove', entryId: page.items[1]!.entryId}), {name: 'AbortError'});
    assert.equal(f.calls.slice(before).some(call => call.method === 'DELETE'), false);
  } finally {f.stop();}
});

test('host output callback retirement and intent getters cannot send reads or commands', async () => {
  const f = await fixture(); let current = true;
  const context = {...f.context, isCurrent: () => current, readOutput: () => {current = false; return {kind: 'local' as const, target: f.target};}};
  try {
    const before = f.calls.length; assert.throws(() => f.player.captureEditWindow(f.target, f.revision, context), {name: 'AbortError'}); assert.equal(f.calls.length, before);
    current = true; let retireOutput = false;
    const reader = f.player.captureEditWindow(f.target, f.revision, {...f.context, isCurrent: () => current, readOutput: () => {if (retireOutput) current = false; return {kind: 'local' as const, target: f.target};}});
    const page = await reader.read(); retireOutput = true; const afterPage = f.calls.length;
    await assert.rejects(reader.next(page), {name: 'AbortError'}); assert.equal(f.calls.length, afterPage);
    current = true; const other = f.player.captureEditWindow(f.target, f.revision, {...f.context, isCurrent: () => current}), second = await other.read();
    const beforeIntent = f.calls.length, intent = {get action() {current = false; return 'remove' as const;}, entryId: second.items[1]!.entryId};
    await assert.rejects(other.edit(second, intent), {name: 'AbortError'}); assert.equal(f.calls.length, beforeIntent);
  } finally {f.stop();}
});

test('evicted receipts and actually unavailable rows cannot dispatch', async () => {
  let hide = false;
  const f = await fixture(async (request, response) => {
    if (!hide || !request.path.includes('/entries?')) return;
    const body: any = structuredClone(response.body); body.items[1] = {...body.items[1], available: false, itemId: '', kind: 'unavailable'}; return {...response, body};
  });
  try {
    const a = f.player.captureEditWindow(f.target, f.revision, f.context), first = await a.read(), second = await a.next(first); await a.next(second);
    const before = f.calls.length; await assert.rejects(a.edit(first, {action: 'remove', entryId: first.items[1]!.entryId}));
    assert.equal(f.calls.slice(before).some(call => call.method === 'DELETE'), false);
    hide = true; const b = f.player.captureEditWindow(f.target, f.revision, f.context), page = await b.read();
    const after = f.calls.length; await assert.rejects(b.edit(page, {action: 'remove', entryId: page.items[1]!.entryId}));
    assert.equal(f.calls.slice(after).some(call => call.method === 'DELETE'), false);
  } finally {f.stop();}
});

test('session, generation, run and anchor drift retire actual core readers', async () => {
  for (const drift of ['session', 'generation', 'run', 'anchor'] as const) {
    const f = await fixture();
    try {
      const reader = f.player.captureEditWindow(f.target, f.revision, f.context), page = await reader.read(), core = f.player as any, playback = f.playback as any;
      if (drift === 'session') core.session = {...core.session, id: 'replacement'};
      if (drift === 'generation') core.session = {...core.session, presentation: {...core.session.presentation, generation: f.target.generation + 1}};
      if (drift === 'run') playback.snapshot = {...playback.snapshot, playbackRun: f.target.playbackRun + 1};
      if (drift === 'anchor') core.header = {...core.header, current: {...core.header.current, position: core.header.current.position + 1}};
      const before = f.calls.length; await assert.rejects(reader.next(page), {name: 'AbortError'}); assert.equal(f.calls.length, before);
    } finally {f.stop();}
  }
});

test('malformed accepted readback is terminal and cannot replay the command', async () => {
  let sent = false;
  const f = await fixture(async (request, response) => {
    if (request.method === 'DELETE') sent = true;
    if (sent && request.path.includes('/entries?')) {const body: any = structuredClone(response.body); delete body.page.revision; return {...response, body};}
  });
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, f.context), page = await reader.read();
    await assert.rejects(reader.edit(page, {action: 'remove', entryId: page.items[1]!.entryId}));
    const writes = f.calls.filter(call => call.method === 'DELETE').length; assert.equal(writes, 1);
    await assert.rejects(reader.edit(page, {action: 'remove', entryId: page.items[1]!.entryId})); assert.equal(f.calls.filter(call => call.method === 'DELETE').length, writes);
  } finally {f.stop();}
});

test('removing the sole final-page entry can read back an empty page at the new total', async () => {
  const f = await fixture(undefined, 301);
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, f.context); let page = await reader.read();
    for (let i = 0; i < 6; i++) page = await reader.next(page);
    assert.equal(page.start, 300); assert.equal(page.items.length, 1);
    const result = await reader.edit(page, {action: 'remove', entryId: page.items[0]!.entryId});
    assert.equal(result.start, 300); assert.equal(result.total, 300); assert.equal(result.items.length, 0);
  } finally {f.stop();}
});

test('late positional read cannot publish after the original source is replaced', async () => {
  let hold = false, entered!: () => void, release!: () => void;
  const arrived = new Promise<void>(resolve => {entered = resolve;}), gate = new Promise<void>(resolve => {release = resolve;});
  const f = await fixture(async (request, response) => {if (hold && request.path.includes('/entries?')) {entered(); await gate;} return response;});
  const controller = new AbortController(); let pending: Promise<unknown> | undefined;
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, {...f.context, signal: controller.signal});
    hold = true; pending = reader.read(); const refused = assert.rejects(pending, {name: 'AbortError'}); await bounded(arrived);
    hold = false; await f.player.playItem('replacement'); release(); await bounded(refused);
    assert.notEqual(f.player.currentSession()?.itemId, f.target.itemId);
  } finally {controller.abort(); release(); try {if (pending) await bounded(pending.then(() => undefined, () => undefined));} finally {f.stop();}}
});

test('remote refusal retires synchronously before an immediate local re-admission attempt', async () => {
  const f = await fixture(); let remote = false;
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, {...f.context, readOutput: () => remote ? {kind: 'remote' as const} : {kind: 'local' as const, target: f.target}});
    const page = await reader.read(), before = f.calls.length; remote = true;
    const first = reader.next(page); remote = false; const second = reader.next(page);
    await Promise.all([assert.rejects(first, {name: 'AbortError'}), assert.rejects(second, {name: 'AbortError'})]);
    assert.equal(f.calls.length, before, 'neither synchronous refusal nor immediate retry sends IO');
  } finally {f.stop();}
});

test('a malformed receipt cannot reopen the reader during rejection cleanup', async () => {
  const f = await fixture();
  try {
    const reader = f.player.captureEditWindow(f.target, f.revision, f.context), page = await reader.read(), before = f.calls.length;
    const bad = reader.next({...page}), simultaneous = reader.next(page);
    await Promise.all([assert.rejects(bad), assert.rejects(simultaneous)]);
    assert.equal(f.calls.length, before, 'the in-flight failure cannot be bypassed by another read');
  } finally {f.stop();}
});
