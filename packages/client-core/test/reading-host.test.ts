import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createReadingHost, connectReadingHost, type ReadingHostSession, type ReadingHostSearchAll} from '../src/reading-host.ts';
import {parsePreferenceSnapshot} from '../src/preferences.ts';
import {ReadingError, type ReadingManifest} from '../src/reading.ts';
const preferences = parsePreferenceSnapshot(JSON.parse(readFileSync(new URL('../../../server/testdata/contracts/get-bootstrap.json', import.meta.url), 'utf8')).response.preferences);
function pair(format: 'pdf' | 'epub' | 'cbz' = 'pdf', beforeConnect?: (host: ReturnType<typeof createReadingHost>) => void, onSearchAll?: ReadingHostSearchAll) {
  const life = new AbortController(), sent: string[] = [], calls: string[] = [];
  const payload = Uint8Array.from({length: 170003}, (_, i) => i % 251);
  const manifest: ReadingManifest = Object.freeze({sessionId: 'session', sourceId: 'asset', itemId: 'item', revision: 'a'.repeat(64), size: payload.length, format, metadata: {title: 'Actual title'},
    ...(format === 'epub' ? {resources: [{path: 'EPUB/chapter.xhtml', mime: 'application/xhtml+xml', size: payload.length}], spine: ['EPUB/chapter.xhtml']} : format === 'cbz' ? {pageNames: ['page.png']} : {})});
  let input: ((message: string) => void) | undefined, back = 0, settings = 0, current = true;
  const facade: ReadingHostSession = {manifest, signal: life.signal,
    async headPDF() {calls.push('head');},
    async readRange({start, endExclusive}, signal) {assert.equal(signal?.aborted, false); calls.push('range'); return payload.slice(start, endExclusive);},
    async readAsset() {calls.push('asset'); return payload.slice();},
    async page(index) {calls.push('page'); return {index, name: 'page.png', mime: 'image/png', bytes: payload.slice()};},
    async progress() {return {progress: null};}, async saveProgress() {calls.push('save'); return {progress: null, saved: false};},
  };
  const host = createReadingHost({session: facade, channel: 'captured_unique_channel', preferences: () => preferences, initialProgress: {progress: null}, current: () => current,
    send(message) {sent.push(message); input?.(message);}, onBack() {back++;}, onSettings() {settings++;}, ...(onSearchAll ? {onSearchAll} : {})});
  beforeConnect?.(host);
  const connection = connectReadingHost({send: host.receive, subscribe(listener) {input = listener; return () => {input = undefined;};}});
  return {connection, host, life, calls, payload, sent, facade, deliver(message: string) {input?.(message);}, get back() {return back;}, get settings() {return settings;}, retire() {current = false; host.dispose();}};
}
test('trusted bridge delivers actual source bytes in bounded chunks, metadata omission and confirmed preferences', async () => {
  for (const format of ['pdf', 'epub', 'cbz'] as const) {
    const f = pair(format), guest = await f.connection.ready;
    assert.equal(guest.session.manifest.metadata.author, undefined); assert.ok(Object.isFrozen(guest.session.manifest.metadata));
    let bytes: Uint8Array;
    if (format === 'pdf') {await guest.session.headPDF(); bytes = await guest.session.readRange({start: 100, endExclusive: 65000}); assert.deepEqual(bytes, f.payload.slice(100, 65000));}
    else if (format === 'epub') {bytes = await guest.session.readAsset('EPUB/chapter.xhtml'); assert.deepEqual(bytes, f.payload);}
    else {const page = await guest.session.page(0, {width: 320}); bytes = page.bytes; assert.equal(page.name, 'page.png'); assert.deepEqual(bytes, f.payload);}
    bytes[0] = 255; assert.equal(f.payload[0], 0);
    const chunks = f.sent.map(s => JSON.parse(s)).filter(m => m.kind === 'reply' && m.value?.bytes);
    assert.ok(chunks.length >= 2); assert.ok(chunks.every(m => m.value.bytes.length <= 65536));
    let confirmations = 0; const off = guest.preferences.subscribe(() => confirmations++);
    f.host.confirm(); assert.equal(confirmations, 1); assert.deepEqual(guest.preferences.getSnapshot(), preferences); off();
    await guest.openSettings(); await guest.back(); assert.equal(f.settings, 1); assert.equal(f.back, 1);
    const saved = await guest.session.saveProgress({expectedRevision: 0, location: format === 'epub' ? {format, progression: 0, cfi: 'epubcfi(/6/2!/4/1:0)'} : {format, progression: 0, pageIndex: 0}});
    assert.equal(saved.progress, null); assert.equal(saved.saved, false);
    guest.dispose(); f.host.dispose();
  }
});
test('range bounds and wrong channel fail before any facade I/O; retirement aborts the structural source', async () => {
  const f = pair(), guest = await f.connection.ready;
  await assert.rejects(guest.session.readRange({start: 0, endExclusive: 65537}), (error: unknown) => error instanceof ReadingError && error.code === 'invalid_range');
  f.host.receive(JSON.stringify({v: 1, channel: 'another_channel_name', kind: 'request', id: 100, method: 'head'}));
  assert.deepEqual(f.calls, []);
  f.retire(); assert.equal(guest.session.signal.aborted, true);
  await assert.rejects(guest.session.headPDF(), /cancelled/);
  guest.dispose();
});
test('caller cancellation aborts the actual in-flight facade callback and releases its transfer', async () => {
  const f = pair('epub'); let aborted = false;
  f.facade.readAsset = async (_path, signal) => new Promise((_resolve, reject) => {
    signal!.addEventListener('abort', () => {aborted = true; reject(new ReadingError('cancelled'));}, {once: true});
  });
  const guest = await f.connection.ready, caller = new AbortController();
  const pending = guest.session.readAsset('EPUB/chapter.xhtml', caller.signal);
  await new Promise<void>(resolve => setImmediate(resolve)); caller.abort();
  await assert.rejects(pending, /cancelled/); assert.equal(aborted, true);
  guest.dispose(); f.host.dispose();
});

test('a retained reader hidden before the handshake reports inactive to its first guest subscriber', async t => {
  const f = pair('pdf', host => host.setActive(false)), guest = await f.connection.ready;
  t.after(() => {guest.dispose(); f.host.dispose();});
  const states: boolean[] = [], off = guest.onActive(active => states.push(active));
  assert.deepEqual(states, [false]);
  f.host.setActive(true); assert.deepEqual(states, [false, true]);
  off(); f.host.setActive(false); assert.deepEqual(states, [false, true]);
  assert.deepEqual(f.calls, [], 'activity handoff starts no source, progress or Settings request');
});

test('activity received before a guest subscribes is replayed without accepting another channel or reviving a retired guest', async t => {
  const f = pair(), guest = await f.connection.ready;
  t.after(() => {guest.dispose(); f.host.dispose();});
  f.host.setActive(false); f.host.setActive(true); f.host.setActive(false);
  f.deliver(JSON.stringify({v: 1, channel: 'another_channel_name', kind: 'active', active: true}));
  const states: boolean[] = []; guest.onActive(active => states.push(active));
  assert.deepEqual(states, [false]);
  f.host.setActive(true); assert.deepEqual(states, [false, true]);
  f.retire(); f.host.setActive(false); guest.onActive(active => states.push(active));
  assert.equal(guest.session.signal.aborted, true); assert.deepEqual(states, [false, true]);
});

const nextTurn = () => new Promise<void>(resolve => setImmediate(resolve));
const searchPacket = (id: number, query: string, extra: Record<string, unknown> = {}) => JSON.stringify({
  v: 1, channel: 'captured_unique_channel', kind: 'request', id, method: 'search-all', args: {query}, ...extra,
});

test('search-all is optional and accepts only the original literal query after native acceptance', async t => {
  const old = pair('epub'), oldGuest = await old.connection.ready;
  t.after(() => {oldGuest.dispose(); old.host.dispose();});
  assert.equal(oldGuest.searchAll, undefined);
  const queries: string[] = [];
  const f = pair('epub', undefined, async (query, signal, current) => {
    assert.equal(signal.aborted, false); assert.equal(current(), true); queries.push(query); return true;
  }), guest = await f.connection.ready;
  t.after(() => {guest.dispose(); f.host.dispose();});
  const query = '"literal: ' + 'a'.repeat(1000) + '"';
  await guest.searchAll!(query);
  assert.deepEqual(queries, [query]);
  assert.deepEqual(f.calls, [], 'navigation does not read bytes, results or progress');
  const replies = f.sent.map(message => JSON.parse(message)).filter(message => message.kind === 'reply');
  assert.equal(replies.length, 1); assert.equal(replies[0].value, true);
  assert.equal(f.sent.some(message => message.includes(query)), false, 'the acknowledgement does not echo search words');
  for (const invalid of ['a'.repeat(1025), '\uD800', '?!.,']) await assert.rejects(guest.searchAll!(invalid), /invalid_request/);
  assert.equal(queries.length, 1);
});

test('search-all requires started/active host, exact packet/args, channel and fresh request id', async t => {
  let calls = 0;
  const f = pair('epub', host => {host.receive(searchPacket(1, 'before ready'));}, () => {calls++; return true;}), guest = await f.connection.ready;
  t.after(() => {guest.dispose(); f.host.dispose();});
  assert.equal(calls, 0);
  f.host.receive(searchPacket(1, 'wrong channel', {channel: 'another_valid_channel'}));
  f.host.receive(searchPacket(2, 'extra packet', {url: 'https://untrusted.example'}));
  f.host.receive(searchPacket(3, 'extra args', {args: {query: 'words', location: 'epubcfi(/fake)'}}));
  f.host.receive(searchPacket(4, 'bad argument', {args: {query: 42}}));
  await nextTurn(); assert.equal(calls, 0);
  f.host.setActive(false);
  await assert.rejects(guest.searchAll!('inactive words'), /cancelled/);
  f.host.receive(searchPacket(5, 'raw inactive words'));
  await nextTurn(); assert.equal(calls, 0);
  f.host.setActive(true);
  f.host.receive(searchPacket(6, 'accepted words'));
  await nextTurn(); assert.equal(calls, 1);
  f.host.receive(searchPacket(6, 'replayed words'));
  await nextTurn(); assert.equal(calls, 1);
});

test('search-all refuses rejected navigation and cannot acknowledge cancellation or inactivity', async t => {
  const refused = pair('epub', undefined, () => false), refusedGuest = await refused.connection.ready;
  t.after(() => {refusedGuest.dispose(); refused.host.dispose();});
  await assert.rejects(refusedGuest.searchAll!('words'), /cancelled/);
  assert.equal(refused.sent.some(message => JSON.parse(message).value === true), false);
  for (const retirement of ['caller', 'inactive', 'host', 'session'] as const) {
    let complete!: (accepted: boolean) => void, observed!: AbortSignal, isCurrent!: () => boolean;
    const f = pair('epub', undefined, (_query, signal, current) => {
      observed = signal; isCurrent = current; return new Promise(resolve => {complete = resolve;});
    }), guest = await f.connection.ready, caller = new AbortController();
    const pending = guest.searchAll!('held words', caller.signal);
    const rejected = assert.rejects(pending, error=>error instanceof ReadingError&&error.code==='cancelled');
    if (retirement === 'caller') caller.abort();
    else if (retirement === 'inactive') f.host.setActive(false);
    else if (retirement === 'session') f.life.abort();
    else f.retire();
    assert.equal(observed.aborted, true); assert.equal(isCurrent(), false);
    complete(true); await rejected; await nextTurn();
    assert.equal(f.sent.some(message => JSON.parse(message).value === true), false);
    guest.dispose(); f.host.dispose();
  }
});

test('search-all uses the existing bounded request deadline and refuses late acceptance', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let complete!: (accepted: boolean) => void, signal!: AbortSignal;
  const f = pair('epub', undefined, (_query, currentSignal) => {signal = currentSignal; return new Promise(resolve => {complete = resolve;});});
  const guest = await f.connection.ready;
  try {
    const pending = guest.searchAll!('deadline words');
    const rejected = assert.rejects(pending, /cancelled/);
    t.mock.timers.tick(30000); await rejected;
    assert.equal(signal.aborted, true);
    complete(true); await nextTurn();
    assert.equal(f.sent.some(message => JSON.parse(message).value === true), false);
  } finally {guest.dispose(); f.host.dispose(); t.mock.timers.reset();}
});


test('original host retirement distinguishes deliberate cancellation from a typed Reading failure',async()=>{
 for(const reason of [undefined,new DOMException('aborted','AbortError'),new ReadingError('source_changed'),new ReadingError('not_permitted',403)]){
  let complete!:(accepted:boolean)=>void,signal!:AbortSignal;
  const f=pair('epub',undefined,(_query,original)=>{signal=original;return new Promise(resolve=>{complete=resolve;});});
  const guest=await f.connection.ready;const pending=guest.searchAll!('original words');
  const expected=reason instanceof ReadingError?reason.code:'cancelled';
  const rejected=assert.rejects(pending,error=>error instanceof ReadingError&&error.code===expected);
  if(reason===undefined)f.host.dispose();else f.life.abort(reason);
  assert.equal(signal.aborted,true);complete(true);await rejected;await nextTurn();
  assert.equal(f.sent.some(message=>JSON.parse(message).value===true),false);
  const retired=f.sent.map(message=>JSON.parse(message)).find(message=>message.kind==='retired');assert.equal(retired.error.code,expected);
  guest.dispose();f.host.dispose();
 }
});
