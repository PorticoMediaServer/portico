import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime.ts';
import {downloadEntryIdentity} from '../src-next/screens/downloads/presentation.ts';
import {nativePublicationReadsAvailable} from '../platform/offline-reading.ts';

function events() {
  const listeners = new Set<() => void>();
  return {subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    emit: () => {for (const listener of [...listeners]) listener();}, listeners};
}
async function fixture(installed = true) {
  const navEvents = events(), rows = events(), authEvents = events();
  const viewer = {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile'};
  const authority = {...viewer, epoch: 'epoch'};
  const entry = {id: 'download', itemId: 'item', state: 'ready', owner: 'actual-owner',
    receipt: {receiptId: 'receipt', claims: {viewer, artifact: {sha256: 'artifact', bytes: 123}}}};
  let entries = [entry], nav = {route: {kind: 'downloads'}, revision: 1}, alive = true;
  const calls: {entry: unknown; lifetime: {signal: AbortSignal; isCurrent: () => boolean}; resolve: () => void}[] = [];
  const startOffline = (entry: unknown, lifetime: {signal: AbortSignal; isCurrent: () => boolean}) => new Promise<void>(resolve => {calls.push({entry, lifetime, resolve});});
  const context = {session: () => ({viewer}), authority: () => authority, current: () => alive,
    playback: {...(installed ? {startOffline} : {}), runtime: {startOffline: () => {throw new Error('runtime presence is not an installed adapter capability');}}},
    navigation: {getSnapshot: () => nav, subscribe: navEvents.subscribe}, subscribeAuthority: authEvents.subscribe};
  const device = {getSnapshot: () => ({entries}), subscribe: rows.subscribe, playable: () => ''};
  const module = await screenModule(new URL('../src-next/screens/downloads/playback.ts', import.meta.url), {
    react: {default: {}}, '../destinations/lifetime': {nativeDestinationLifetime}, './presentation': {downloadEntryIdentity},
    '../../../platform/offline-reading': {nativePublicationReadsAvailable},
  });
  return {module, context, device, entry, calls, navEvents, rows, authEvents,
    navigation: (route = {kind: 'downloads'}) => {nav = {route, revision: nav.revision + 1}; navEvents.emit();},
    replace: (next: typeof entries) => {entries = next; rows.emit();},
    retire: () => {alive = false; authEvents.emit();},
  };
}

test('installed local action captures the real row/Nav lifetime and Back never revives an interrupted gesture', async () => {
  const f = await fixture(), bound = f.module.createNativeOfflineEntryPlayback(f.context, f.device, () => true);
  try {
    await assert.rejects(bound.openOffline({...f.entry}), {code: 'cancelled'});
    const pending = bound.openOffline(f.entry);
    assert.equal(f.calls.length, 1); assert.equal(f.calls[0].entry, f.entry);
    assert.equal(f.calls[0].lifetime.isCurrent(), true);
    f.navigation({kind: 'title'});
    assert.equal(f.calls[0].lifetime.signal.aborted, true);
    f.navigation(); assert.equal(f.calls[0].lifetime.isCurrent(), false);
    f.calls[0].resolve(); await assert.rejects(pending, {code: 'cancelled'});
    const next = bound.openOffline(f.entry);
    assert.equal(f.calls[1].lifetime.isCurrent(), true);
    f.retire(); assert.equal(f.calls[1].lifetime.signal.aborted, true);
    f.calls[1].resolve(); await assert.rejects(next, {code: 'cancelled'});
    await assert.rejects(bound.openOffline(f.entry), {code: 'cancelled'});
  } finally {bound.dispose();}
  assert.equal(f.navEvents.listeners.size, 0); assert.equal(f.rows.listeners.size, 0); assert.equal(f.authEvents.listeners.size, 0);
});

test('artifact replacement, lease retirement and supersession cancel pending starts but settled admission outlives its row owner', async () => {
  const f = await fixture(); let borrowed = true;
  const bound = f.module.createNativeOfflineEntryPlayback(f.context, f.device, () => borrowed);
  try {
    const first = bound.openOffline(f.entry), second = bound.openOffline(f.entry);
    assert.equal(f.calls[0].lifetime.signal.aborted, true);
    assert.equal(f.calls[1].lifetime.isCurrent(), true);
    f.replace([{...f.entry, receipt: {...f.entry.receipt, receiptId: 'replacement'}}]);
    assert.equal(f.calls[1].lifetime.signal.aborted, true);
    for (const call of f.calls) call.resolve();
    await Promise.all([assert.rejects(first, {code: 'cancelled'}), assert.rejects(second, {code: 'cancelled'})]);
    f.replace([f.entry]);
    const accepted = bound.openOffline(f.entry); f.calls[2].resolve(); await accepted;
    borrowed = false; bound.dispose();
    assert.equal(f.calls[2].lifetime.signal.aborted, false, 'settled source ownership belongs to the player, not the screen');
    assert.equal(f.calls[2].lifetime.isCurrent(), false);
    await assert.rejects(bound.openOffline(f.entry), {code: 'cancelled'});
  } finally {bound.dispose();}
});

test('missing installed adapter method stays unavailable even if a runtime method exists', async () => {
  const f = await fixture(false), bound = f.module.createNativeOfflineEntryPlayback(f.context, f.device, () => true);
  assert.equal(bound.openOffline, undefined); assert.equal(f.navEvents.listeners.size, 0);
  bound.dispose();
});

test('disposing one borrowed document cancels only its pending gesture', async () => {
  const f = await fixture();
  const first = f.module.createNativeOfflineEntryPlayback(f.context, f.device, () => true);
  const second = f.module.createNativeOfflineEntryPlayback(f.context, f.device, () => true);
  try {
    const a = first.openOffline(f.entry), b = second.openOffline(f.entry);
    first.dispose(); assert.equal(f.calls[0].lifetime.signal.aborted, true);
    assert.equal(f.calls[1].lifetime.signal.aborted, false);
    for (const call of f.calls) call.resolve();
    await assert.rejects(a, {code: 'cancelled'}); await b;
  } finally {first.dispose(); second.dispose();}
});

test('Saved plays only a genuine single ready file; multi-file or unavailable cards keep the existing Downloads destination', async () => {
  const f = await fixture(), played: unknown[] = [], errors: unknown[] = []; let browsed = 0;
  const device = {...f.device, openOffline: async (entry: unknown) => {played.push(entry);}};
  const open = (entries: unknown[], candidate: object = device) => f.module.openNativeSavedDownload(candidate, {entries}, () => {browsed++;}, (error: unknown) => errors.push(error));
  open([f.entry]); assert.deepEqual(played, [f.entry]);
  open([f.entry, f.entry]); open([{...f.entry, state: 'downloading'}]); open([f.entry], f.device);
  open([f.entry], {...device, playable: () => 'actual receipt refusal'});
  assert.equal(browsed, 4); assert.deepEqual(played, [f.entry]);
  const failed = new Error('actual local failure'); open([f.entry], {...device, openOffline: async () => {throw failed;}});
  await new Promise<void>(resolve => setImmediate(resolve)); assert.deepEqual(errors, [failed]);
  const retired = Object.assign(new Error('cancelled'), {code: 'cancelled'});
  assert.doesNotThrow(() => open([f.entry], {...device, playable: () => {throw retired;}}));
  assert.equal(errors[1], retired, 'retired synchronous native access follows the existing error presenter');
});
