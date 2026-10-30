import test from 'node:test';
import assert from 'node:assert/strict';
import { LyricsService, parseLyricsView, activeLyricLine, lyricSeekPosition, type LyricRevision, type LyricsChooseContext } from '../src/lyrics.ts';
import { readLyricsResponse } from '../src/lyrics-http.ts';
import { HttpLocalApi } from '../src/index.ts';
const scope = { serverId: 'server', viewerId: 'viewer-profile-session' }, target = { itemId: 'song', libraryId: 'music', sessionId: 'session', sessionGeneration: 2 };
const source = { id: 'asset', version: 'b'.repeat(64), startSeconds: 10, duration: 90, sourceDuration: 100, local: true };
const wireSource = { id: 'asset', version: 'b'.repeat(64), startMs: 10000, durationMs: 90000, sourceDurationMs: 100000, local: true };
const document = { format: 'lrc' as const, text: '[00:12]First\n[00:15]Next', lines: [{ atMs: 12000, text: 'First' }, { atMs: 15000, text: 'Next' }], embeddedOffsetMs: 0 };
const revision = { id: 'lyric', revision: 1, scope: 'private' as const, canManage: true, deleted: false, language: 'en', offsetMs: 500, format: 'lrc' as const, digest: 'c'.repeat(64), provenance: { origin: 'upload' as const, providerId: '', label: 'Uploaded text', rights: '' }, createdAt: '2026-09-06T00:00:00Z', document };
function view() { return { scope: { ...target, serverId: 'server', viewerFence: 'a'.repeat(64) }, sources: [wireSource], source: wireSource, resources: [revision], selection: { revision: 1, resource: revision }, canShare: false, providers: { lrclib: false, configured: true, revision: 1 } }; }
function deferred<T>() { let resolve!: (v: T) => void; let reject!: (e: unknown) => void; const promise = new Promise<T>((r, j) => { resolve = r; reject = j; }); return { promise, resolve, reject }; }
function api(request: (path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<unknown>) { return { request: request as <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T> }; }
test('uses accepted source time, positive delay, backward seeks and clipped seek actions', () => {
    assert.equal(activeLyricLine(document, 2.49, 500, 10), -1);
    assert.equal(activeLyricLine(document, 2.5, 500, 10), 0);
    assert.equal(activeLyricLine(document, 5.5, 500, 10), 1);
    assert.equal(activeLyricLine(document, 2.5, 500, 10), 0);
    assert.equal(lyricSeekPosition(document.lines[0], 500, source), 2.5);
    assert.equal(lyricSeekPosition({ atMs: 5000, text: 'outside' }, 0, source), null);
    assert.equal(lyricSeekPosition({ atMs: null, text: 'plain' }, 0, source), null);
    assert.equal(activeLyricLine({ ...document, format: 'text' }, 2, 0), -1);
});
test('validates song, viewer fence, playback generation, source and ordered timeline', () => {
    assert.equal(parseLyricsView(view(), scope, target).selection.resource?.id, 'lyric');
    for (const field of ['serverId', 'libraryId', 'itemId', 'sessionId', 'sessionGeneration', 'viewerFence']) {
        const raw = structuredClone(view());
        (raw.scope as Record<string, unknown>)[field] = 'wrong';
        assert.throws(() => parseLyricsView(raw, scope, target));
    }
    const wrong = structuredClone(view());
    wrong.selection.resource.document.lines[1].atMs = 12000;
    assert.equal(parseLyricsView(wrong, scope, target).selection.resource?.document!.lines.length, 2);
    wrong.selection.resource.document.lines[1].atMs = 11999;
    assert.throws(() => parseLyricsView(wrong, scope, target));
    assert.throws(() => parseLyricsView(view(), scope, { ...target, sourceId: 'different' }));
});
test('late transport responses cannot overwrite a newer view or poison its viewer fence', async () => {
    const first = deferred<unknown>(), second = deferred<unknown>();
    let n = 0;
    const service = new LyricsService(api(() => ++n === 1 ? first.promise : second.promise), scope, target);
    const a = service.refresh(), b = service.refresh();
    const newest = view();
    newest.scope.viewerFence = 'd'.repeat(64);
    second.resolve(newest);
    await b;
    first.resolve(view());
    await a;
    assert.equal(service.getSnapshot().data?.scope.viewerFence, 'd'.repeat(64));
    assert.equal(service.getSnapshot().error, '');
    service.dispose();
});
test('cancel clears protected text and fences a response even if abort is ignored', async () => {
    const pending = deferred<unknown>();
    const service = new LyricsService(api(() => pending.promise), scope, target);
    const work = service.refresh();
    service.cancel();
    pending.resolve(view());
    await work;
    assert.equal(service.getSnapshot().data, null);
    assert.equal(service.getSnapshot().busy, false);
    await service.upload({ scope: 'private', language: 'en', text: 'old', format: 'text' });
    assert.equal(service.getSnapshot().data, null);
    service.dispose();
});
test('choose Off and replacements carry exact source and CAS intent', async () => {
    const requests: {
        path: string;
        method?: string;
        body?: unknown;
    }[] = [];
    const service = new LyricsService(api(async (path, method, body) => { requests.push({ path, method, body }); return view(); }), scope, target);
    await service.refresh();
    await service.choose(null);
    assert.deepEqual(requests[1].body, { sourceId: 'asset', sourceVersion: source.version, sessionId: 'session', expectedSelectionRevision: 1, resourceId: '', revision: 0 });
    await service.upload({ resourceId: 'lyric', expectedRevision: 1, scope: 'private', language: 'en', format: 'text', text: 'Replacement' });
    assert.equal((requests[3].body as {
        expectedRevision: number;
    }).expectedRevision, 1);
    assert.equal((requests[3].body as {
        sourceVersion: string;
    }).sourceVersion, source.version);
    service.dispose();
});
test('authorization loss clears private view, preview and candidates', async () => {
    let denied = false;
    const service = new LyricsService(api(async () => { if (denied)
        throw Object.assign(new Error(), { status: 403 }); return view(); }), scope, target);
    await service.refresh();
    denied = true;
    await service.refresh();
    assert.equal(service.getSnapshot().data, null);
    assert.match(service.getSnapshot().error, /no longer access/);
    service.dispose();
});
test('provider search clears older results before a failed new search', async () => {
    const candidate = { id: 'candidate', language: 'und', format: 'text', provenance: revision.provenance, preview: 'Preview' };
    let fail = false;
    const service = new LyricsService(api(async (path) => { if (!path.endsWith('/search'))
        return view(); if (fail)
        throw new Error(); return { candidates: [candidate], warnings: [] }; }), scope, target);
    await service.refresh();
    await service.search('local', '', 'und');
    assert.equal(service.getSnapshot().candidates.length, 1);
    fail = true;
    await service.search('local', '', 'und');
    assert.equal(service.getSnapshot().candidates.length, 0);
    service.dispose();
});
test('bounded transport rejects excess bytes, HTML and invalid UTF8; preserves authorization status', async () => {
    const signal = new AbortController().signal;
    await assert.rejects(readLyricsResponse(new Response('{}', { headers: { 'Content-Type': 'text/html' } }), signal));
    await assert.rejects(readLyricsResponse(new Response('{}', { headers: { 'Content-Type': 'application/json', 'Content-Length': '99999999' } }), signal));
    await assert.rejects(readLyricsResponse(new Response(new Uint8Array([0xff]), { headers: { 'Content-Type': 'application/json' } }), signal));
    await assert.rejects(readLyricsResponse(new Response(JSON.stringify({ error: { code: 'unauthorized' } }), { status: 403, headers: { 'Content-Type': 'application/json' } }), signal), e => (e as {
        status: number;
    }).status === 403);
});
test('HttpLocalApi captures bearer and rejects a response after token replacement', async () => {
    const pending = deferred<Response>();
    let authorization = '';
    const client = new HttpLocalApi('https://example.test', 'token-a', async (_url, init) => { authorization = (init?.headers as Record<string, string>).Authorization; return pending.promise; });
    const request = client.requestLyrics('/v1/items/song/lyrics');
    client.setAccessToken('token-b');
    pending.resolve(new Response('{}', { headers: { 'Content-Type': 'application/json' } }));
    await assert.rejects(request, /old session/);
    assert.equal(authorization, 'Bearer token-a');
    await assert.rejects(client.requestLyrics('https://provider.test/lyrics'));
});

test('equal timestamps select the last line in document order, also after backward seeks', () => {
 const simultaneous = { ...document, lines: [
  { atMs: 12000, text: 'First voice' }, { atMs: 12000, text: 'Second voice' },
  { atMs: 12000, text: 'Translation' }, { atMs: 15000, text: 'Next' }
 ] };
 assert.equal(activeLyricLine(simultaneous, 2.499, 500, 10), -1);
 assert.equal(activeLyricLine(simultaneous, 2.5, 500, 10), 2);
 assert.equal(activeLyricLine(simultaneous, 5.5, 500, 10), 3);
 assert.equal(activeLyricLine(simultaneous, 2.5, 500, 10), 2);
});

// A song with no lyrics answers 404: the empty state ("No lyrics"), not "could not be loaded".
test('a 404 lyrics read is the empty state, not an error', async () => {
    const service = new LyricsService(api(async () => { throw Object.assign(new Error('not found'), { status: 404, code: 'not_found' }); }), scope, target);
    await service.refresh();
    const s = service.getSnapshot();
    assert.equal(s.error, '');
    assert.equal(s.data, null);
    assert.equal(s.busy, false);
});

async function choiceHarness() {
 const calls: { path: string; method?: string; body?: unknown; signal: AbortSignal; pending: ReturnType<typeof deferred<unknown>> }[] = [];
 const transport = api((path, method, body, signal) => {
  assert.ok(signal); const pending = deferred<unknown>(); calls.push({ path, method, body, signal, pending }); return pending.promise;
 });
 const service = new LyricsService(transport, scope, { ...target, sourceId: 'asset' });
 const loading = service.refresh(); calls[0]!.pending.resolve(view()); await loading;
 const controller = new AbortController(); let current = true;
 const context = (): LyricsChooseContext => ({ snapshot: service.getSnapshot(), signal: controller.signal, isCurrent: () => current });
 const selectedView = (selected: LyricRevision | null, selectionRevision = 2) => ({ ...view(), selection: { revision: selectionRevision, resource: selected } });
 return { service, transport, calls, controller, context, selectedView, retire: () => { current = false; } };
}
const turn = () => new Promise<void>(resolve => setImmediate(resolve));

for (const off of [false, true]) test(`scoped ${off ? 'Off' : 'resource'} uses exact offered source/CAS and validates PUT plus GET`, async t => {
 const h = await choiceHarness(); t.after(() => h.service.dispose());
 const shown = h.service.getSnapshot(), resource = off ? null : shown.data!.resources[0]!;
 const chosen = h.service.choose(resource, h.context());
 assert.equal(h.calls[1]!.path, '/v1/items/song/lyrics/selection'); assert.equal(h.calls[1]!.method, 'PUT');
 assert.deepEqual(h.calls[1]!.body, { sourceId: 'asset', sourceVersion: source.version, sessionId: 'session', expectedSelectionRevision: 1, resourceId: resource?.id ?? '', revision: resource?.revision ?? 0 });
 h.calls[1]!.pending.resolve({ revision: 2, resource: resource === null ? null : revision }); await turn();
 assert.equal(h.calls[2]!.path, '/v1/items/song/lyrics?sessionId=session&sourceId=asset');
 assert.equal(h.calls[2]!.signal, h.calls[1]!.signal, 'PUT and GET share the command controller');
 h.calls[2]!.pending.resolve(h.selectedView(resource === null ? null : revision)); await chosen;
 assert.equal(h.service.getSnapshot().data?.selection.revision, 2);
 assert.equal(h.service.getSnapshot().data?.selection.resource?.id ?? null, resource?.id ?? null);
 assert.equal(h.service.getSnapshot().busy, false); assert.equal(h.service.getSnapshot().preview, null);
});

test('scoped choice does not require resource management permission', async t => {
 const h = await choiceHarness(); t.after(() => h.service.dispose());
 const refreshed = h.service.refresh(); const read = view(); read.resources[0] = { ...revision, canManage: false }; h.calls[1]!.pending.resolve(read); await refreshed;
 const row = h.service.getSnapshot().data!.resources[0]!; const chosen = h.service.choose(row, h.context());
 assert.equal(h.calls[2]!.method, 'PUT'); h.calls[2]!.pending.resolve({ revision: 2, resource: { ...revision, canManage: false } }); await turn();
 h.calls[3]!.pending.resolve(h.selectedView(revision)); await chosen;
});

test('scoped admission refuses old/forged snapshot, row, source and nontrue or aborted context without changing service', async t => {
 const h = await choiceHarness(); t.after(() => h.service.dispose()); const shown = h.service.getSnapshot();
 const dead = new AbortController(); dead.abort(); let evaluated = 0;
 const cases = [
  { resource: shown.data!.resources[0]!, context: { snapshot: shown, signal: dead.signal, isCurrent: () => { evaluated++; return true; } } },
  { resource: shown.data!.resources[0]!, context: { ...h.context(), isCurrent: () => false } },
  { resource: shown.data!.resources[0]!, context: { ...h.context(), isCurrent: (() => 1) as unknown as () => boolean } },
  { resource: shown.data!.resources[0]!, context: { ...h.context(), snapshot: { ...shown } } },
  { resource: { ...shown.data!.resources[0]! }, context: h.context() },
  { resource: { ...shown.data!.resources[0]!, id: 'not-offered' }, context: h.context() },
 ];
 for (const input of cases) { await assert.rejects(h.service.choose(input.resource, input.context)); assert.strictEqual(h.service.getSnapshot(), shown); assert.equal(h.calls.length, 1); }
 assert.equal(evaluated, 0);
 // Constructor's established session guard must not be relaxed by the new command port.
 assert.throws(() => new LyricsService(api(async () => view()), scope, { ...target, sessionGeneration: 0 }));
});

test('busy and concurrent scoped choice refuse without touching the first command', async t => {
 const h = await choiceHarness(); t.after(() => h.service.dispose()); const ctx = h.context();
 const first = h.service.choose(null, ctx), rejected = assert.rejects(first);
 const busy = h.service.getSnapshot();
 await assert.rejects(h.service.choose(null, ctx)); await assert.rejects(h.service.choose(null, h.context()));
 assert.strictEqual(h.service.getSnapshot(), busy); assert.equal(h.calls.length, 2); assert.equal(h.calls[1]!.signal.aborted, false);
 h.controller.abort(); await rejected;
});

for (const replace of [false, true]) test(`busy subscriber ${replace ? 'refreshes' : 'cancels caller'} before PUT and old work does not dispatch`, async t => {
 const h = await choiceHarness(); t.after(() => h.service.dispose()); let replacement: Promise<void> | undefined, entered = false;
 h.service.subscribe(() => { if (!entered && h.service.getSnapshot().busy) { entered = true; if (replace) replacement = h.service.refresh(); else h.controller.abort(); } });
 await assert.rejects(h.service.choose(null, h.context()));
 assert.equal(h.calls.filter(call => call.method === 'PUT').length, 0);
 if (replace) { assert.ok(replacement); assert.equal(h.calls[1]!.method, 'GET'); h.calls[1]!.pending.resolve(view()); await replacement; }
 else assert.equal(h.service.getSnapshot().busy, false);
});

test('TRUE-returning admission currentness may install a refresh but cannot dispatch the old choice', async t => {
 const h = await choiceHarness(); t.after(() => h.service.dispose()); let replacement: Promise<void> | undefined;
 const shown = h.service.getSnapshot();
 await assert.rejects(h.service.choose(null, { snapshot: shown, signal: h.controller.signal, isCurrent: () => { replacement ??= h.service.refresh(); return true; } }));
 assert.equal(h.calls.length, 2); assert.equal(h.calls[1]!.method, 'GET');
 h.calls[1]!.pending.resolve(view()); await replacement; assert.equal(h.service.getSnapshot().busy, false);
});

for (const phase of ['PUT', 'GET'] as const) for (const reason of ['abort', 'currentness', 'cancel', 'dispose'] as const) {
 test(`${reason} while ${phase} is held settles original command and ignored late completion cannot publish`, async t => {
  const h = await choiceHarness(); t.after(() => h.service.dispose());
  const chosen = h.service.choose(null, h.context()), rejected = assert.rejects(chosen);
  if (phase === 'GET') { h.calls[1]!.pending.resolve({ revision: 2, resource: null }); await turn(); }
  const held = h.calls.at(-1)!;
  if (reason === 'abort') h.controller.abort();
  else if (reason === 'cancel') h.service.cancel();
  else if (reason === 'dispose') h.service.dispose();
  else { h.retire(); held.pending.resolve(phase === 'PUT' ? { revision: 2, resource: null } : h.selectedView(null)); }
  await rejected; const after = h.service.getSnapshot(); const count = h.calls.length;
  held.pending.resolve(phase === 'PUT' ? { revision: 2, resource: null } : h.selectedView(null)); await turn();
  assert.equal(h.calls.length, count); assert.strictEqual(h.service.getSnapshot(), after);
  if (reason === 'cancel') assert.equal(after.data, null);
  else if (reason !== 'dispose') assert.equal(after.busy, false);
 });
}

test('retirement after PUT receipt parsing forbids the follow-up GET', async t => {
 const h = await choiceHarness(); t.after(() => h.service.dispose()); const chosen = h.service.choose(null, h.context());
 const rejected = assert.rejects(chosen);
 h.calls[1]!.pending.resolve({ revision: 2, get resource() { h.controller.abort(); return null; } });
 await rejected; assert.equal(h.calls.length, 2); assert.equal(h.service.getSnapshot().data?.selection.revision, 1);
});

test('one total 22s deadline covers PUT and GET and retired timers cannot affect the next command', async t => {
 t.mock.timers.enable({ apis: ['setTimeout'] });
 const h = await choiceHarness(); t.after(() => h.service.dispose());
 const chosen = h.service.choose(null, h.context()), rejected = assert.rejects(chosen, /timed out/);
 t.mock.timers.tick(21000); h.calls[1]!.pending.resolve({ revision: 2, resource: null }); await turn();
 assert.equal(h.calls.length, 3); t.mock.timers.tick(999); assert.equal(h.service.getSnapshot().busy, true);
 t.mock.timers.tick(1); await rejected; assert.equal(h.calls[2]!.signal.aborted, true); assert.equal(h.service.getSnapshot().busy, false);
 h.calls[2]!.pending.reject(new Error('late timeout error')); await turn(); assert.equal(h.service.getSnapshot().error, '');
});

test('refresh and another drawer survive original cancellation and late transport error', async t => {
 const h = await choiceHarness(); t.after(() => h.service.dispose());
 const old = h.service.choose(null, h.context()), rejected = assert.rejects(old);
 h.calls[1]!.pending.resolve({ revision: 2, resource: null }); await turn();
 const refresh = h.service.refresh(); await rejected;
 assert.equal(h.calls[3]!.signal.aborted, false); assert.equal(h.service.getSnapshot().busy, true);
 h.calls[2]!.pending.reject(new Error('late old GET error')); await turn();
 assert.equal(h.service.getSnapshot().busy, true); assert.equal(h.calls[3]!.signal.aborted, false);
 h.calls[3]!.pending.resolve(h.selectedView(revision, 7)); await refresh;
 const other = new AbortController(), shown = h.service.getSnapshot();
 const second = h.service.choose(null, { snapshot: shown, signal: other.signal, isCurrent: () => true });
 assert.equal((h.calls[4]!.body as { expectedSelectionRevision: number }).expectedSelectionRevision, 7);
 h.controller.abort(); assert.equal(h.calls[4]!.signal.aborted, false);
 h.calls[4]!.pending.resolve({ revision: 8, resource: null }); await turn(); h.calls[5]!.pending.resolve(h.selectedView(null, 8)); await second;
 assert.equal(h.service.getSnapshot().data?.selection.revision, 8);
});

test('transport abort reentry installs a successor whose busy state and controller the old command cannot clear', async t => {
 const h = await choiceHarness(); t.after(() => h.service.dispose());
 const old = h.service.choose(null, h.context()), rejected = assert.rejects(old); let successor: Promise<void> | undefined;
 h.calls[1]!.signal.addEventListener('abort', () => { successor = h.service.refresh(); }, { once: true });
 h.controller.abort(); await rejected;
 assert.ok(successor); assert.equal(h.calls[2]!.method, 'GET'); assert.equal(h.calls[2]!.signal.aborted, false);
 h.calls[1]!.pending.resolve({ revision: 2, resource: null }); await turn();
 assert.equal(h.calls.length, 3); assert.equal(h.service.getSnapshot().busy, true);
 h.calls[2]!.pending.resolve(view()); await successor; assert.equal(h.service.getSnapshot().busy, false);
});

for (const defect of ['cas', 'wrong-resource', 'unexpected-off', 'fence', 'source', 'version', 'session', 'policy', 'new-selection', 'invalid-document'] as const) {
 test(`scoped choice refuses ${defect} receipt/readback without adopting latest state`, async t => {
  const h = await choiceHarness(); t.after(() => h.service.dispose()); const shown = h.service.getSnapshot(), row = shown.data!.resources[0]!;
  const chosen = h.service.choose(row, h.context()), rejected = assert.rejects(chosen);
  const receipt = { revision: defect === 'cas' ? 3 : 2, resource: defect === 'unexpected-off' ? null : defect === 'wrong-resource' ? { ...revision, id: 'other' } : revision };
  h.calls[1]!.pending.resolve(receipt); await turn();
  if (!['cas', 'wrong-resource', 'unexpected-off'].includes(defect)) {
   const raw: Record<string, unknown> = h.selectedView(revision);
   if (defect === 'fence') raw.scope = { ...view().scope, viewerFence: 'f'.repeat(64) };
   if (defect === 'source') raw.source = { ...wireSource, id: 'other' };
   if (defect === 'version') { raw.source = { ...wireSource, version: 'f'.repeat(64) }; raw.sources = [raw.source]; }
   if (defect === 'session') raw.scope = { ...view().scope, sessionGeneration: 3 };
   if (defect === 'policy') raw.librarySelection = { revision: 1, resource: { ...revision, scope: 'library' } };
   if (defect === 'new-selection') raw.selection = { revision: 3, resource: null };
   if (defect === 'invalid-document') raw.selection = { revision: 2, resource: { ...revision, document: { ...document, lines: [{ atMs: 99999999, text: 'outside' }] } } };
   h.calls[2]!.pending.resolve(raw);
  }
  await rejected; assert.strictEqual(h.service.getSnapshot().data, shown.data); assert.equal(h.service.getSnapshot().busy, false);
 });
}

test('scoped denial preserves the existing protected-data clearing policy', async t => {
 const h = await choiceHarness(); t.after(() => h.service.dispose());
 const chosen = h.service.choose(null, h.context()), rejected = assert.rejects(chosen);
 h.calls[1]!.pending.reject(Object.assign(new Error('denied'), { status: 403 })); await rejected;
 assert.equal(h.service.getSnapshot().data, null); assert.equal(h.service.getSnapshot().preview, null); assert.match(h.service.getSnapshot().error, /no longer access/);
});

test('scoped transport captures requestLyrics once and keeps the original receiver for PUT and GET', async () => {
 let reads = 0, preferred = 0, fallback = 0;
 const original = { request: async <T>() => { fallback++; return view() as T; }, get requestLyrics() {
  reads++; return async function<T>(this: unknown, _path: string, method?: string) { assert.strictEqual(this, original); preferred++; return (method === 'PUT' ? { revision: 2, resource: null } : { ...view(), selection: { revision: preferred > 1 ? 2 : 1, resource: preferred > 1 ? null : revision } }) as T; };
 } };
 const service = new LyricsService(original, scope, target); await service.refresh(); reads = 0;
 await service.choose(null, { snapshot: service.getSnapshot(), signal: new AbortController().signal, isCurrent: () => true });
 assert.equal(reads, 1); assert.equal(preferred, 3); assert.equal(fallback, 0); service.dispose();
});

test('scoped real HttpLocalApi carries original signals/token through PUT and GET and rejects token replacement', async () => {
 for (const replaceAt of ['none', 'PUT', 'GET'] as const) {
  const calls: { method: string; signal: AbortSignal; authorization: string; pending: ReturnType<typeof deferred<Response>> }[] = [];
  const client = new HttpLocalApi('https://example.test', 'token-a', async (_url, init) => {
   assert.ok(init?.signal); const pending = deferred<Response>(); calls.push({ method: init.method ?? 'GET', signal: init.signal, authorization: (init.headers as Record<string, string>).Authorization, pending }); return pending.promise;
  });
  const service = new LyricsService(client, scope, target), json = (raw: unknown) => new Response(JSON.stringify(raw), { headers: { 'Content-Type': 'application/json' } });
  const refresh = service.refresh(); calls[0]!.pending.resolve(json(view())); await refresh;
  const controller = new AbortController(), chosen = service.choose(null, { snapshot: service.getSnapshot(), signal: controller.signal, isCurrent: () => true });
  const result = replaceAt === 'none' ? chosen : assert.rejects(chosen, /old session/);
  if (replaceAt === 'PUT') client.setAccessToken('token-b');
  calls[1]!.pending.resolve(json({ revision: 2, resource: null })); await turn();
  if (replaceAt !== 'PUT') { assert.equal(calls[2]!.method, 'GET'); if (replaceAt === 'GET') client.setAccessToken('token-b'); calls[2]!.pending.resolve(json({ ...view(), selection: { revision: 2, resource: null } })); }
  await result; assert.ok(calls.every(call => call.authorization === 'Bearer token-a'));
  assert.ok(calls.every(call => call.signal instanceof AbortSignal));
  assert.equal(service.getSnapshot().data?.selection.revision, replaceAt === 'none' ? 2 : 1); service.dispose();
 }
});
