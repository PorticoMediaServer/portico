import test from 'node:test';
import assert from 'node:assert/strict';
import {getEventListeners} from 'node:events';
import {type HomeApi, validateHomeDocument} from '../src/home.ts';
import {HomeAuthorityRetired, HomeService, type HomeAuthorityObservation, type HomeObservationOptions} from '../src/home-service.ts';

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
}
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
function row(id = 'recent', card = 'a', changes: Record<string, unknown> = {}): any {
  return {id, viewerFence: 'fence', title: id, kind: 'recent', artworkShape: 'poster', endpoint: '/v1/home/rows/' + id,
    privacySensitivity: 'catalog', policyState: 'available', priority: 100, required: false, hideable: true,
    reorderable: true, critical: false, cursorCapable: true, entries: card ? [{id: card, kind: 'movie', title: card,
      libraryId: 'movies', available: true, navigation: {view: 'item', entityId: card}, playback: {itemId: card}}] : [],
    total: 50, start: 0, limit: 12, hasMore: true, nextCursor: 'cursor', revision: {catalog: 3, viewer: 1}, ...changes};
}
function document(card = 'a', changes: Record<string, unknown> = {}): any {
  return {serverId: 'server', viewerFence: 'fence', rows: [row('recent', card)], spotlight: [],
    layout: {revision: 2, rowOrder: ['recent'], hiddenRowIds: []}, revision: {catalog: 3, viewer: 1},
    generatedAt: '2026-10-28T12:00:00Z', ...changes};
}
type Request = {path: string; method?: string; body?: unknown; signal?: AbortSignal};
function harness() {
  const requests: Request[] = [], observations: HomeAuthorityObservation[] = [];
  let current = true, retired = 0, mismatches = 0;
  let handler = (_request: Request): unknown => document();
  let observer = (_value: HomeAuthorityObservation): 'accept' | 'retire' => 'accept';
  const observation: HomeObservationOptions = {serverId: 'server', isCurrent: () => current,
    observe: value => { observations.push(value); return observer(value); },
    retire: () => { retired++; current = false; }, mismatch: () => { mismatches++; }};
  const api: HomeApi = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
    const request = {path, method, body, signal}; requests.push(request); return await handler(request) as T;
  }};
  const service = new HomeService(api, {observation});
  const manifests = () => requests.filter(request => request.path === '/v1/home');
  return {service, requests, observations, manifests, setHandler: (next: typeof handler) => { handler = next; },
    setObserver: (next: typeof observer) => { observer = next; }, close: () => { current = false; },
    retired: () => retired, mismatches: () => mismatches};
}
const card = (service: HomeService) => service.getSnapshot().document?.rows[0]?.entries[0]?.id;
const lifetime = () => ({signal: new AbortController().signal, isCurrent: () => true});

// T7/T8: a manifest observation is synchronous authority work, never a display read.
test('authority is one manifest, zero shelves/publications, and preserves the exact revision object', async () => {
  const h = harness();
  h.setHandler(() => document('', {rows: [row('recent', '', {deferred: true})]}));
  const before = h.service.getSnapshot(); let publications = 0;
  h.service.subscribe(() => publications++);
  try {
    assert.equal(await h.service.authority({isCurrent: () => true}), 'answered');
    await tick();
    assert.equal(h.requests.length, 1);
    assert.equal(h.service.getSnapshot(), before);
    assert.equal(publications, 0);
    assert.deepEqual(h.observations[0]?.revision, {catalog: 3, viewer: 1});
    assert.equal(h.observations[0]?.generation, 0);
    assert.equal(h.observations[0]?.quiet, 0);
    assert.ok(Number.isFinite(h.observations[0]?.at));
    assert.deepEqual(Object.keys(h.observations[0]!).sort(), ['at', 'generation', 'quiet', 'revision', 'serverId', 'viewerFence']);
  } finally { h.service.dispose(); }
});

test('refresh reports not-answered before ready, adopted on unchanged merge, and preserves original errors', async () => {
  const h = harness();
  try {
    assert.equal(await h.service.refresh(), 'not-answered'); assert.equal(h.requests.length, 0);
    await h.service.load(); const before = h.service.getSnapshot();
    assert.equal(await h.service.refresh(), 'adopted'); assert.equal(h.service.getSnapshot(), before);
    const error = Object.assign(new Error('busy'), {status: 503, retryAfterSeconds: 7200});
    h.setHandler(() => { throw error; });
    await assert.rejects(h.service.refresh(), caught => caught === error);
    await assert.rejects(h.service.authority({isCurrent: () => true}), caught => caught === error);
    assert.equal(h.service.getSnapshot(), before);
  } finally { h.service.dispose(); }
});

test('already-aborted and closed owners start no read, no observer, and no settle', async () => {
  const h = harness(); const stop = new AbortController(); stop.abort(); let settles = 0;
  try {
    await h.service.load(); const count = h.requests.length;
    assert.equal(await h.service.refresh({signal: stop.signal, settle: async () => { settles++; }}), 'superseded');
    assert.equal(await h.service.authority({signal: stop.signal, isCurrent: () => true}), 'superseded');
    assert.equal(await h.service.authority({isCurrent: () => false}), 'superseded');
    await h.service.revalidate({signal: stop.signal});
    h.close();
    await h.service.load(); await h.service.retry(); await h.service.load({clear: true});
    assert.equal(await h.service.refresh(), 'superseded');
    assert.equal(await h.service.authority({isCurrent: () => true}), 'superseded');
    await assert.rejects(h.service.saveLayout([], [], () => 'key'), {name: 'AbortError'});
    await assert.rejects(h.service.resetLayout(() => 'key'), {name: 'AbortError'});
    assert.equal(h.requests.length, count); assert.equal(h.observations.length, 1); assert.equal(settles, 0);
  } finally { h.service.dispose(); }
});

test('load checks its real owned lifetime again after loading publication', async () => {
  for (const dispose of [false, true]) {
    const h = harness();
    h.service.subscribe(() => { if (h.service.getSnapshot().phase === 'loading') { if (dispose) h.service.dispose(); else h.close(); } });
    await h.service.load();
    assert.equal(h.requests.length, 0); assert.equal(h.observations.length, 0);
    h.service.dispose();
  }
});

test('wrong current requested server retires before observer or baseline; malformed and obsolete answers infer nothing', async () => {
  for (const authority of [false, true]) {
    const h = harness(); h.setHandler(() => document('wrong', {serverId: 'other'}));
    try {
      await assert.rejects(authority ? h.service.authority({isCurrent: () => true}) : h.service.load(), HomeAuthorityRetired);
      assert.equal(h.retired(), 1); assert.equal(h.observations.length, 0);
      assert.notEqual(h.service.getSnapshot().phase, 'ready');
      await h.service.retry(); assert.equal(h.requests.length, 1);
    } finally { h.service.dispose(); }
  }
  const h = harness();
  try {
    h.setHandler(() => ({serverId: 'other'}));
    await assert.rejects(h.service.authority({isCurrent: () => true}));
    assert.equal(h.retired(), 0); assert.equal(h.observations.length, 0);
    const answer = deferred<unknown>(); let current = true;
    h.setHandler(() => answer.promise);
    const pending = h.service.authority({isCurrent: () => current}); current = false;
    answer.resolve(document('wrong', {serverId: 'other'}));
    assert.equal(await pending, 'superseded'); assert.equal(h.retired(), 0); assert.equal(h.observations.length, 0);
    h.setHandler(() => document('right'));
    assert.equal(await h.service.authority({isCurrent: () => true}), 'answered');
  } finally { h.service.dispose(); }
});

test('valid fence replacement retires synchronously before display settle', async () => {
  const h = harness(); let settles = 0;
  try {
    await h.service.load(); const before = h.service.getSnapshot();
    h.setHandler(() => document('new', {viewerFence: 'new-fence', rows: [row('recent', 'new', {viewerFence: 'new-fence'})]}));
    await assert.rejects(h.service.refresh({settle: async () => { settles++; }}), HomeAuthorityRetired);
    assert.equal(h.retired(), 1); assert.equal(settles, 0); assert.equal(h.service.getSnapshot(), before);
  } finally { h.service.dispose(); }
});

// H-11: a library added or removed changes the viewer fence only. Home adopts the new
// document; the authority (and every shell keyed on it) stays. A privacy change retires.
test('a privacy fence keeps authority across a viewer-fence-only change and retires on its own change', async () => {
  const h = harness();
  try {
    h.setHandler(() => document('a', {privacyFence: 'privacy'}));
    await h.service.load();
    h.setHandler(() => document('more', {viewerFence: 'more-libraries', privacyFence: 'privacy', rows: [row('recent', 'more', {viewerFence: 'more-libraries'})]}));
    assert.equal(await h.service.refresh(), 'adopted');
    assert.equal(h.retired(), 0); assert.equal(card(h.service), 'more');
    assert.equal(h.service.getSnapshot().document?.viewerFence, 'more-libraries');
    assert.equal(h.observations.at(-1)?.privacyFence, 'privacy');
    h.setHandler(() => document('other', {viewerFence: 'more-libraries', privacyFence: 'another-viewer', rows: [row('recent', 'other', {viewerFence: 'more-libraries'})]}));
    await assert.rejects(h.service.refresh(), HomeAuthorityRetired);
    assert.equal(h.retired(), 1); assert.equal(card(h.service), 'more');
  } finally { h.service.dispose(); }
});

test('older servers without a privacy fence keep retiring on the viewer fence', () => {
  const old = validateHomeDocument(document());
  assert.equal('privacyFence' in old, false);
  assert.equal(validateHomeDocument(document('a', {privacyFence: 'privacy'})).privacyFence, 'privacy');
  assert.throws(() => validateHomeDocument(document('a', {privacyFence: ''})));
  assert.throws(() => validateHomeDocument(document('a', {privacyFence: 7})));
});

test('either borrower can cancel independently; all borrowed cancellation aborts transport even if ignored', async () => {
  for (const cancelDisplay of [false, true]) {
    const h = harness();
    try {
      await h.service.load(); const before = h.service.getSnapshot(); const answer = deferred<unknown>();
      h.setHandler(() => answer.promise);
      const displayStop = new AbortController(), authorityStop = new AbortController();
      const display = h.service.refresh({signal: displayStop.signal});
      const authority = h.service.authority({signal: authorityStop.signal, isCurrent: () => true});
      assert.equal(h.manifests().length, 2);
      (cancelDisplay ? displayStop : authorityStop).abort();
      assert.equal(h.manifests()[1]!.signal?.aborted, false);
      answer.resolve(document('next'));
      assert.equal(await display, cancelDisplay ? 'superseded' : 'adopted');
      assert.equal(await authority, cancelDisplay ? 'answered' : 'superseded');
      assert.equal(h.observations.length, 2);
      if (cancelDisplay) assert.equal(h.service.getSnapshot(), before); else assert.equal(card(h.service), 'next');
    } finally { h.service.dispose(); }
  }
  const h = harness();
  try {
    const answer = deferred<unknown>(), stop = new AbortController(); h.setHandler(() => answer.promise);
    const authority = h.service.authority({signal: stop.signal, isCurrent: () => true}); stop.abort();
    assert.equal(await authority, 'superseded'); assert.equal(h.requests[0]!.signal?.aborted, true);
    h.setHandler(() => document('next'));
    assert.equal(await h.service.authority({isCurrent: () => true}), 'answered', 'new demand waits for aborted-flight release');
    answer.resolve(document('late', {serverId: 'other'})); await tick();
    assert.equal(h.retired(), 0); assert.equal(h.observations.length, 1);
  } finally { h.service.dispose(); }
});

test('network ownership releases before typing settle; authority neither waits for nor supersedes display', async () => {
  const h = harness(); const typing = deferred<void>(); let entered = 0;
  try {
    await h.service.load(); h.setHandler(() => document('display'));
    const display = h.service.refresh({settle: () => { entered++; return typing.promise; }});
    await tick(); assert.equal(entered, 1); assert.equal(card(h.service), 'a');
    h.setHandler(() => document('authority'));
    assert.equal(await h.service.authority({isCurrent: () => true}), 'answered');
    assert.equal(h.manifests().length, 3); assert.equal(card(h.service), 'a');
    typing.resolve(); assert.equal(await display, 'adopted'); assert.equal(card(h.service), 'display');
  } finally { typing.resolve(); h.service.dispose(); }
});

test('new display supersedes a never-ending old settle; cancellation and dispose settle promptly', async () => {
  for (const action of ['refresh', 'abort', 'dispose'] as const) {
    const h = harness(); const typing = deferred<void>(), stop = new AbortController();
    try {
      await h.service.load(); h.setHandler(() => document('old'));
      const old = h.service.refresh({signal: stop.signal, settle: () => typing.promise}); await tick();
      if (action === 'refresh') { h.service.withoutEntry('a'); h.setHandler(() => document('new')); assert.equal(await h.service.refresh(), 'adopted'); }
      else if (action === 'abort') stop.abort(); else h.service.dispose();
      assert.equal(await old, 'superseded');
      if (action === 'refresh') assert.equal(card(h.service), 'new');
    } finally { typing.resolve(); h.service.dispose(); }
  }
});

// T9: read A is never the answer to an acknowledged write, regardless of editor scope.
test('acknowledged save/reset waits for read A release and shares exactly one postwrite read B', async () => {
  for (const scoped of [false, true]) for (const reset of [false, true]) {
    const h = harness(); const a = deferred<unknown>(), b = deferred<unknown>();
    try {
      await h.service.load(); let reads = 0;
      h.setHandler(request => request.path.startsWith('/v1/home/layout') ? {revision: 3, rowOrder: ['recent'], hiddenRowIds: []} : ++reads === 1 ? a.promise : b.promise);
      const old = h.service.refresh(); const oldAuthority = h.service.authority({isCurrent: () => true});
      const write = reset ? h.service.resetLayout(() => 'reset-key', 2, scoped ? lifetime() : undefined)
        : h.service.saveLayout(['recent'], [], () => 'save-key', 2, scoped ? lifetime() : undefined);
      await tick();
      const authority = h.service.authority({isCurrent: () => true});
      assert.equal(reads, 1, 'B waits for the old network owner');
      a.resolve(document('prewrite')); await tick();
      assert.equal(await old, 'superseded'); assert.equal(await oldAuthority, 'superseded');
      assert.equal(card(h.service), 'a'); assert.equal(h.observations.length, 1, 'prewrite proof cannot establish authority after ACK');
      assert.equal(reads, 2);
      b.resolve(document('postwrite', {layout: {revision: 3, rowOrder: ['recent'], hiddenRowIds: []}}));
      await write; assert.equal(await authority, 'answered');
      assert.equal(card(h.service), 'postwrite'); assert.equal(h.service.getSnapshot().document?.layout.revision, 3);
      assert.equal(reads, 2); assert.equal(h.observations.length, 2);
      const mutation = h.requests.find(request => request.path.startsWith('/v1/home/layout'))!;
      assert.equal(mutation.method, reset ? 'POST' : 'PUT');
    } finally { a.resolve(document()); b.resolve(document()); h.service.dispose(); }
  }
});

test('new explicit load/clear advances serial, coalesces load calls, and rejects old manifest proof', async () => {
  for (const clear of [false, true]) {
    const h = harness(), a = deferred<unknown>(), b = deferred<unknown>();
    try {
      await h.service.load(); let reads = 0;
      h.setHandler(() => ++reads === 1 ? a.promise : b.promise);
      const old = h.service.authority({isCurrent: () => true});
      const load = h.service.load({clear}); assert.equal(h.service.load(), load);
      a.resolve(document('wrong', {serverId: 'other'})); await tick();
      assert.equal(await old, 'superseded'); assert.equal(h.retired(), 0); assert.equal(reads, 2);
      b.resolve(document('loaded')); await load;
      assert.equal(card(h.service), 'loaded'); assert.equal(h.observations.length, 2);
    } finally { a.resolve(document()); b.resolve(document()); h.service.dispose(); }
  }
});

test('both afterLayout branches lose publication and hydration to a newer display refresh', async () => {
  for (const scoped of [false, true]) for (const first of ['layout', 'refresh'] as const) {
    const h = harness(), a = deferred<unknown>(), typing = deferred<void>();
    try {
      await h.service.load(); let reads = 0, settles = 0;
      h.setHandler(request => request.path.startsWith('/v1/home/layout') ? {revision: 3, rowOrder: [], hiddenRowIds: []}
        : ++reads === 1 ? a.promise : document('new'));
      if (first === 'layout') {
        const write = h.service.saveLayout([], [], () => 'key', 2, scoped ? lifetime() : undefined); await tick();
        const display = h.service.refresh();
        a.resolve(document('new')); await write; assert.equal(await display, 'adopted'); assert.equal(card(h.service), 'new');
      } else {
        const old = h.service.refresh({settle: () => { settles++; return typing.promise; }});
        a.resolve(document('old')); await tick(); assert.equal(settles, 1);
        await h.service.saveLayout([], [], () => 'key', 2, scoped ? lifetime() : undefined);
        assert.equal(await old, 'superseded'); assert.equal(card(h.service), 'new');
      }
      assert.equal(h.requests.some(request => request.path.includes('/rows/')), false);
    } finally { a.resolve(document()); typing.resolve(); h.service.dispose(); }
  }
});

test('unacknowledged layout write leaves an in-flight display/authority answer usable', async () => {
  const h = harness(), a = deferred<unknown>(); const error = Object.assign(new Error('conflict'), {status: 409});
  try {
    await h.service.load(); h.setHandler(request => { if (request.path.startsWith('/v1/home/layout')) throw error; return a.promise; });
    const display = h.service.refresh(); const authority = h.service.authority({isCurrent: () => true});
    await assert.rejects(h.service.saveLayout([], [], () => 'key'), caught => caught === error);
    a.resolve(document('current'));
    assert.equal(await display, 'adopted'); assert.equal(await authority, 'answered'); assert.equal(card(h.service), 'current');
    assert.equal(h.manifests().length, 2);
  } finally { a.resolve(document()); h.service.dispose(); }
});

test('ordinary postlayout read failure falls back only for the persistent owner', async () => {
  for (const scoped of [false, true]) {
    const h = harness(); const error = new Error('read failed');
    try {
      await h.service.load(); let reads = 0;
      h.setHandler(request => { if (request.path.startsWith('/v1/home/layout')) return {revision: 3, rowOrder: [], hiddenRowIds: []};
        if (++reads === 1) throw error; return document('fallback'); });
      const write = h.service.saveLayout([], [], () => 'key', 2, scoped ? lifetime() : undefined);
      if (scoped) { await assert.rejects(write, caught => caught === error); assert.equal(reads, 1); assert.equal(card(h.service), 'a'); }
      else { await write; assert.equal(reads, 2); assert.equal(card(h.service), 'fallback'); }
    } finally { h.service.dispose(); }
  }
});

// Each full-read producer must propagate the terminal classification, without fallback.
test('all full producers veto late ready/hydration/fallback when observer retires reentrantly', async () => {
  for (const producer of ['load', 'retry', 'refresh', 'revalidate', 'layout', 'scoped-layout', 'reset', 'scoped-reset', 'more', 'rollback'] as const) {
    for (const retirement of ['verdict', 'close', 'dispose'] as const) {
      const h = harness(); let rollback = () => {}, settles = 0, reads = 0;
      try {
        if (producer !== 'load' && producer !== 'retry') await h.service.load();
        if (producer === 'rollback') {
          rollback = h.service.withoutEntry('a');
          h.setHandler(() => document('replacement')); await h.service.refresh();
        }
        const before = h.service.getSnapshot();
        h.setObserver(() => { if (retirement === 'close') h.close(); if (retirement === 'dispose') h.service.dispose(); return retirement === 'verdict' ? 'retire' : 'accept'; });
        h.setHandler(request => {
          if (request.path.startsWith('/v1/home/layout')) return {revision: 3, rowOrder: [], hiddenRowIds: []};
          if (request.path.includes('/rows/')) throw Object.assign(new Error('stale'), {status: 409, retry: 'after_refresh'});
          reads++; return document('', {rows: [row('recent', '', {deferred: true})]});
        });
        let work: Promise<unknown>;
        switch (producer) {
          case 'load': work = h.service.load(); break;
          case 'retry': work = h.service.retry(); break;
          case 'refresh': work = h.service.refresh({settle: async () => { settles++; }}); break;
          case 'revalidate': work = h.service.revalidate({settle: async () => { settles++; }}); break;
          case 'layout': case 'scoped-layout': work = h.service.saveLayout([], [], () => 'key', 2, producer === 'scoped-layout' ? lifetime() : undefined); break;
          case 'reset': case 'scoped-reset': work = h.service.resetLayout(() => 'key', 2, producer === 'scoped-reset' ? lifetime() : undefined); break;
          case 'more': work = h.service.more('recent'); break;
          case 'rollback': rollback(); work = tick(); break;
        }
        if (producer === 'rollback') await work; else await assert.rejects(work, HomeAuthorityRetired, producer + '/' + retirement);
        assert.equal(h.retired(), 1, producer + '/' + retirement); assert.equal(reads, 1);
        assert.equal(settles, 0); assert.notEqual(h.service.getSnapshot().document?.rows[0]?.deferred, true);
        if (producer !== 'load' && producer !== 'retry' && producer !== 'more') assert.equal(h.service.getSnapshot().document, before.document);
        const count = h.requests.length;
        await h.service.load(); await h.service.retry(); await h.service.revalidate();
        h.service.setRowDemand(undefined); rollback(); await tick();
        assert.equal(h.requests.length, count, 'retired owner starts no fallback or later work');
      } finally { h.service.dispose(); }
    }
  }
});

test('current row and actual child mismatch owe manifest once; malformed and superseded rows infer nothing', async () => {
  for (const page of [false, true]) for (const shape of ['parent', 'child', 'malformed', 'superseded'] as const) {
    const h = harness(), result = deferred<unknown>();
    const id = shape === 'child' ? 'for_you' : 'recent';
    try {
      h.setHandler(() => document('', {rows: [row(id, page ? 'a' : '', {deferred: !page})]}));
      h.service.setRowDemand([]); await h.service.load();
      h.setHandler(request => request.path === '/v1/home' ? document('replacement') : result.promise);
      const work = page ? h.service.more(id) : Promise.resolve(h.service.setRowDemand([id]));
      if (shape === 'superseded') await h.service.refresh();
      result.resolve(shape === 'malformed' ? {viewerFence: 'other'} : shape === 'child'
        ? row('for_you', '', {children: [row('for_you:child', 'child', {family: 'for_you', viewerFence: 'other'})]})
        : row(id, 'wrong', {viewerFence: 'other'}));
      const outcome = await work; await tick();
      assert.equal(h.mismatches(), shape === 'malformed' || shape === 'superseded' ? 0 : 1, String(page) + '/' + shape);
      assert.equal(h.retired(), 0, 'row proof only owes manifest; never directly replaces authority');
      if (page && (shape === 'parent' || shape === 'child')) assert.equal((outcome as {reason: string}).reason, 'viewer_changed');
      assert.notEqual(card(h.service), 'wrong');
    } finally { result.resolve(row()); h.service.dispose(); }
  }
});

test('current stale-page recovery preserves revalidated/stale receipts, never applied manifest cards', async () => {
  for (const failure of [false, true]) {
    const h = harness();
    try {
      await h.service.load();
      h.setHandler(request => { if (request.path.includes('/rows/')) throw Object.assign(new Error('stale'), {retry: 'after_refresh', status: 409});
        if (failure) throw new Error('recovery failed'); return document('recovered'); });
      const outcome = await h.service.more('recent');
      assert.equal(outcome.status, failure ? 'stale' : 'revalidated'); assert.equal(outcome.applied, false);
      assert.equal(card(h.service), failure ? 'a' : 'recovered');
    } finally { h.service.dispose(); }
  }
});

test('rollback restores only the exact optimistic document, otherwise uses observed revalidation', async () => {
  const h = harness();
  try {
    await h.service.load(); const before = h.service.getSnapshot().document;
    const restore = h.service.withoutEntry('a'); restore(); assert.equal(h.service.getSnapshot().document, before);
    const refresh = h.service.withoutEntry('a'); h.setHandler(() => document('b')); await h.service.refresh();
    h.setHandler(() => document('c')); refresh(); await tick();
    assert.equal(card(h.service), 'c'); assert.equal(h.observations.length, 3);
  } finally { h.service.dispose(); }
});

test('omitted options keep independent quiet requests, void results, native TV20, errors and row demand', async () => {
  for (const initialLimit of [undefined, 20]) {
    const requests: Request[] = []; const answers: ReturnType<typeof deferred<unknown>>[] = [];
    const api: HomeApi = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
      requests.push({path, method, body, signal}); const next = deferred<unknown>(); answers.push(next); return await next.promise as T;
    }};
    const service = new HomeService(api, {initialLimit});
    try {
      const load: Promise<void> = service.load(); assert.equal(service.load(), load);
      const prefix = '/v1/home' + (initialLimit ? '?limit=20' : ''); assert.equal(requests[0]?.path, prefix);
      answers[0]!.resolve(document()); assert.equal(await load, undefined);
      const a: Promise<void> = service.revalidate(), b: Promise<void> = service.revalidate();
      assert.equal(requests.length, 3, 'legacy revalidate is never coalesced');
      answers[2]!.resolve(document('b')); await b; answers[1]!.resolve(document('a')); await a;
      assert.equal(card(service), 'b');
      const failure = service.load({clear: true}); assert.equal(service.getSnapshot().document, undefined);
      const error = new Error('load failed'); answers[3]!.reject(error); assert.equal(await failure, undefined);
      assert.equal(service.getSnapshot().phase, 'error'); assert.equal(service.getSnapshot().error, error);
      const retry = service.retry(); answers[4]!.resolve(document('retry')); await retry;
      assert.equal(card(service), 'retry');
      assert.equal(await service.authority({isCurrent: () => true}), 'superseded'); assert.equal(requests.length, 5);
      assert.deepEqual(validateHomeDocument(document()).revision, {catalog: 3, viewer: 1});
    } finally { service.dispose(); }
  }
});

test('same-document refresh/revalidate callers share display adoption through settlement without false supersession', async () => {
  for (const joinDuringSettle of [false, true]) for (const cancel of ['none', 'first', 'second'] as const) {
    const h = harness(), response = deferred<unknown>(), typing = deferred<void>();
    const firstStop = new AbortController(), secondStop = new AbortController(); let firstSettles = 0, secondSettles = 0, publishes = 0;
    try {
      await h.service.load(); h.service.subscribe(() => publishes++);
      h.setHandler(() => response.promise);
      const first = h.service.refresh({signal: firstStop.signal, settle: () => { firstSettles++; return typing.promise; }});
      if (joinDuringSettle) { response.resolve(document('shared')); await tick(); assert.equal(firstSettles, 1); }
      const second = h.service.refresh({signal: secondStop.signal, settle: async () => { secondSettles++; }});
      const compatibility: Promise<void> = h.service.revalidate();
      if (cancel === 'first') firstStop.abort(); if (cancel === 'second') secondStop.abort();
      response.resolve(document('shared')); await tick();
      assert.equal(h.manifests().length, 2);
      assert.equal(h.manifests()[1]?.signal?.aborted, false, 'another display borrower retains network ownership until classification');
      if (cancel !== 'first') assert.equal(card(h.service), 'a', 'the live settle still gates adoption');
      typing.resolve();
      assert.equal(await first, cancel === 'first' ? 'superseded' : 'adopted');
      assert.equal(await second, cancel === 'second' ? 'superseded' : 'adopted');
      assert.equal(await compatibility, undefined);
      assert.equal(card(h.service), 'shared'); assert.equal(publishes, 1);
      assert.equal(firstSettles, cancel === 'first' && !joinDuringSettle ? 0 : 1);
      assert.equal(secondSettles, cancel === 'second' ? 0 : 1);
      assert.equal(getEventListeners(firstStop.signal, 'abort').length, 0);
      assert.equal(getEventListeners(secondStop.signal, 'abort').length, 0);
      assert.equal(h.manifests().length, 2, 'both live tickets completed from adoption; no follow-up caused by false supersession');
    } finally { response.resolve(document()); typing.resolve(); h.service.dispose(); }
  }
});

test('only-live authority borrower may accept a shared answer after display scope/document supersession', async () => {
  const h = harness(), response = deferred<unknown>(); let settles = 0;
  try {
    await h.service.load(); h.setHandler(() => response.promise);
    const display = h.service.refresh({settle: async () => { settles++; }});
    const authority = h.service.authority({isCurrent: () => true});
    h.service.withoutEntry('a'); response.resolve(document('authority'));
    assert.equal(await display, 'superseded'); assert.equal(await authority, 'answered');
    assert.equal(h.observations.length, 2); assert.equal(settles, 0); assert.equal(card(h.service), undefined);
  } finally { response.resolve(document()); h.service.dispose(); }
});

test('layout ACK invalidates prewrite proof even if the editor retires before required reread', async () => {
  for (const reset of [false, true]) {
    const h = harness(), response = deferred<unknown>(), ack = deferred<unknown>(), stop = new AbortController();
    try {
      await h.service.load(); h.setHandler(request => request.path.startsWith('/v1/home/layout') ? ack.promise : response.promise);
      const display = h.service.refresh();
      const write = reset ? h.service.resetLayout(() => 'key', 2, {signal: stop.signal, isCurrent: () => true})
        : h.service.saveLayout([], [], () => 'key', 2, {signal: stop.signal, isCurrent: () => true});
      const rejected = assert.rejects(write, {name: 'AbortError'});
      stop.abort(); ack.resolve({revision: 3, rowOrder: [], hiddenRowIds: []}); await rejected;
      response.resolve(document('prewrite')); assert.equal(await display, 'superseded');
      assert.equal(h.observations.length, 1); assert.equal(card(h.service), 'a');
      assert.equal(h.manifests().length, 2, 'retired editor never falls through to persistent load');
    } finally { response.resolve(document()); ack.resolve({revision: 3, rowOrder: [], hiddenRowIds: []}); h.service.dispose(); }
  }
});

test('cancelled layout-read borrower leaves independent authority borrow alive', async () => {
  const h = harness(), response = deferred<unknown>(), stop = new AbortController();
  try {
    await h.service.load();
    h.setHandler(request => request.path.startsWith('/v1/home/layout') ? {revision: 3, rowOrder: [], hiddenRowIds: []} : response.promise);
    const write = h.service.saveLayout([], [], () => 'key', 2, {signal: stop.signal, isCurrent: () => true}); await tick();
    const authority = h.service.authority({isCurrent: () => true});
    const rejected = assert.rejects(write, {name: 'AbortError'}); stop.abort(); await rejected;
    assert.equal(h.manifests()[1]?.signal?.aborted, false);
    response.resolve(document('proof')); assert.equal(await authority, 'answered'); assert.equal(card(h.service), 'a');
    assert.equal(getEventListeners(stop.signal, 'abort').length, 0);
  } finally { response.resolve(document()); h.service.dispose(); }
});

test('opt-in loadLayout admission refuses retired/preaborted owners without inventing manifest proof', async () => {
  const h = harness(), stop = new AbortController(); stop.abort();
  try {
    await assert.rejects(h.service.loadLayout(stop.signal), {name: 'AbortError'}); assert.equal(h.requests.length, 0);
    h.setHandler(() => ({revision: 2, rowOrder: [], hiddenRowIds: [], rows: []}));
    assert.equal((await h.service.loadLayout()).revision, 2); assert.equal(h.observations.length, 0);
    h.close(); await assert.rejects(h.service.loadLayout(), {name: 'AbortError'}); assert.equal(h.requests.length, 1);
  } finally { h.service.dispose(); }
});

test('opt-in row demand retains two workers and undemanded shelves until actually requested', async () => {
  const h = harness(), shelves = [deferred<unknown>(), deferred<unknown>(), deferred<unknown>()];
  try {
    h.setHandler(() => document('', {rows: ['a', 'b', 'c'].map(id => row(id, '', {deferred: true}))}));
    h.service.setRowDemand([]); await h.service.load(); assert.equal(h.requests.length, 1);
    h.setHandler(request => shelves[['a', 'b', 'c'].findIndex(id => request.path.startsWith('/v1/home/rows/' + id))]!.promise);
    h.service.setRowDemand(['a', 'b', 'c']); assert.equal(h.requests.length, 3);
    shelves[0]!.resolve(row('a', 'a')); await tick(); assert.equal(h.requests.length, 4);
    shelves[1]!.resolve(row('b', 'b')); shelves[2]!.resolve(row('c', 'c')); await tick();
    h.service.setRowDemand(['c', 'b', 'a']); assert.equal(h.requests.length, 4);
    assert.deepEqual(h.service.getSnapshot().document?.rows.map(row => row.entries[0]?.id), ['a', 'b', 'c']);
  } finally { for (const shelf of shelves) shelf.resolve(row()); h.service.dispose(); }
});

test('default 15-second read timeout and configured timeout remain exact with and without observation', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  for (const observed of [false, true]) for (const timeoutMs of [undefined, 37]) {
    let signal: AbortSignal | undefined;
    const api: HomeApi = {request: async <T>(_path: string, _method?: string, _body?: unknown, owned?: AbortSignal) => {
      signal = owned; return await new Promise<T>(() => {});
    }};
    const service = new HomeService(api, {timeoutMs, observation: observed ? {
      serverId: 'server', isCurrent: () => true, observe: () => 'accept', retire: () => {}, mismatch: () => {},
    } : undefined});
    try {
      const load = service.load(); t.mock.timers.tick((timeoutMs ?? 15_000) - 1); await tick();
      assert.equal(service.getSnapshot().phase, 'loading'); assert.equal(signal?.aborted, false);
      t.mock.timers.tick(1); await load;
      assert.equal(service.getSnapshot().phase, 'error'); assert.equal(signal?.aborted, true);
      assert.equal((service.getSnapshot().error as {code: string}).code, 'request_timeout');
      assert.equal(getEventListeners(signal!, 'abort').length, 0);
    } finally { service.dispose(); }
  }
});

test('replacing the shown document cancels an obsolete display settle even without another refresh', async () => {
  const h = harness(), typing = deferred<void>();
  try {
    await h.service.load(); h.setHandler(() => document('pending'));
    const display = h.service.refresh({settle: () => typing.promise}); await tick();
    h.service.withoutEntry('a'); assert.equal(await display, 'superseded'); assert.equal(card(h.service), undefined);
  } finally { typing.resolve(); h.service.dispose(); }
});

test('postwrite persistent load fallback cannot coalesce with a prewrite explicit load still in progress', async () => {
  const h = harness(), a = deferred<unknown>(), b = deferred<unknown>();
  try {
    let reads = 0;
    h.setHandler(request => request.path.startsWith('/v1/home/layout') ? {revision: 3, rowOrder: [], hiddenRowIds: []}
      : ++reads === 1 ? a.promise : b.promise);
    const load = h.service.load();
    const write = h.service.saveLayout([], [], () => 'key', 2); await tick();
    assert.equal(reads, 2, 'ACK barrier replaces the stale persistent load, even when transport ignores cancellation');
    b.resolve(document('postwrite', {layout: {revision: 3, rowOrder: [], hiddenRowIds: []}})); await write; await load;
    assert.equal(card(h.service), 'postwrite'); assert.equal(h.observations.length, 1);
    a.resolve(document('prewrite')); await tick(); assert.equal(card(h.service), 'postwrite');
  } finally { a.resolve(document()); b.resolve(document()); h.service.dispose(); }
});
