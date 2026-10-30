import test from 'node:test';
import assert from 'node:assert/strict';
import {EventsClient} from '../src/playback-v1/events.ts';
import {FreshnessHub, dependsOn, listedTitlesWithPersonalState, readsPersonalState, titleWithPersonalState, titlesWithPersonalState} from '../src/freshness-hub.ts';
import {DocumentPool, serviceDocument} from '../src/freshness-pool.ts';

/** A clock the test moves by hand; timers fire in order as it advances. */
function clock() {
  let now = 0, next = 1;
  const timers = new Map<number, {at: number; fn: () => void}>();
  return {
    env: {setTimer: (fn: () => void, ms: number) => { const id = next++; timers.set(id, {at: now + Math.max(0, ms), fn}); return id; }, clearTimer: (id: unknown) => { timers.delete(id as number); }, now: () => now, random: () => 0, contentEveryMs: 0},
    pending: () => timers.size,
    async advance(ms: number) {
      const end = now + ms;
      for (;;) {
        let due: [number, {at: number; fn: () => void}] | undefined;
        for (const entry of timers) if (entry[1].at <= end && (!due || entry[1].at < due[1].at)) due = entry;
        if (!due) break;
        timers.delete(due[0]); now = due[1].at; due[1].fn();
        for (let i = 0; i < 8; i++) await Promise.resolve();
      }
      now = end;
      for (let i = 0; i < 8; i++) await Promise.resolve();
    },
  };
}
const library = (id: string, content: string, order = '1') => ({id: 'e' + content + order + id, type: 'library.changed', resource: {kind: 'library', id}, data: {content, contentOrder: order}});
const personal = (id: string, value: string, observation = '1') => ({id: 'p' + value + observation + id, type: 'personal.changed', resource: {kind: 'library', id}, data: {personal: value, observation}});
const progress = (id: string, value: string, observation: string) => ({id: 'o' + observation + id, type: 'progress.changed', resource: {kind: 'library', id}, data: {personal: value, observation}});

test('a document is read again only when something it depends on moved', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let films = 0, shows = 0;
  const a = hub.document({dependencies: [dependsOn.content('films')], revalidate: async () => { films++; }});
  const b = hub.document({dependencies: [dependsOn.content('shows')], revalidate: async () => { shows++; }});
  a.setOpen(true); b.setOpen(true);
  await c.advance(60_000);
  assert.equal(films + shows, 0, 'nothing moved, so nothing is read however long the screen stays open');
  hub.accept(library('films', '2'));
  assert.equal(a.getState().stale, true);
  await c.advance(1000);
  assert.deepEqual([films, shows], [1, 0]);
  assert.equal(a.getState().stale, false);
  // The feed replays a hint after a reconnect: the same counter is not a change.
  hub.accept({...library('films', '2'), id: 'replayed'});
  await c.advance(1000);
  assert.equal(films, 1);
  // Only the order moved: a document that depends on content alone is left alone.
  hub.accept(library('films', '2', '9'));
  await c.advance(1000);
  assert.equal(films, 1);
});

test('a library being scanned is read again at a steady pace, not on every hint', async () => {
  const c = clock(), hub = new FreshnessHub({...c.env, contentEveryMs: 5000});
  let reads = 0;
  const home = hub.document({dependencies: [dependsOn.content()], revalidate: async () => { reads++; }});
  home.setOpen(true);
  // A lone change after a quiet spell is read as soon as the hints are gathered.
  hub.accept(library('films', '1'));
  await c.advance(1000);
  assert.equal(reads, 1);
  // A scan moves the library twice a second for ten seconds: one read per five seconds, not twenty.
  for (let i = 2; i <= 21; i++) { hub.accept(library('films', String(i))); await c.advance(500); }
  assert.equal(reads, 3, 'the hints inside one spacing are answered together');
  await c.advance(5000);
  assert.equal(reads, 4, 'what moved during the last spacing is still read once it ends');
  await c.advance(60_000);
  assert.equal(reads, 4, 'nothing moved after that');
});

test('hints that arrive together are answered by one read, and every library moves the any-library dependency', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let reads = 0;
  const home = hub.document({dependencies: titlesWithPersonalState(), revalidate: async () => { reads++; }});
  home.setOpen(true);
  hub.accept(library('films', '2')); hub.accept(library('shows', '5')); hub.accept(personal('films', '3'));
  await c.advance(1000);
  assert.equal(reads, 1);
  assert.equal(hub.version(dependsOn.content()), 2);
  assert.equal(hub.version(dependsOn.content('films')), 1);
});

test('a document that is not on screen remembers it is out of date and catches up when shown', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let reads = 0;
  const doc = hub.document({dependencies: [dependsOn.personal('films')], revalidate: async () => { reads++; }});
  hub.accept(personal('films', '2'));
  await c.advance(10_000);
  assert.equal(reads, 0, 'a covered screen reads nothing');
  doc.setOpen(true);
  await c.advance(1000);
  assert.equal(reads, 1);
  doc.setOpen(true);
  await c.advance(10_000);
  assert.equal(reads, 1, 'coming back with nothing new reads nothing');
});

test('nothing is read in the background, and what moved meanwhile is read once on return', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let reads = 0;
  const doc = hub.document({dependencies: [dependsOn.content('films')], revalidate: async () => { reads++; }});
  doc.setOpen(true);
  hub.setActive(false);
  hub.accept(library('films', '2')); hub.accept(library('films', '3'));
  await c.advance(120_000);
  assert.equal(reads, 0);
  hub.setActive(true);
  await c.advance(1000);
  assert.equal(reads, 1);
});

test('a playback position moving elsewhere is answered at a slower pace than a real change', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let reads = 0, progressOnly: boolean[] = [];
  const doc = hub.document({dependencies: titlesWithPersonalState('films'), revalidate: async context => { reads++; progressOnly.push(context.progressOnly); }});
  doc.setOpen(true);
  hub.accept(progress('films', '1', '2'));
  await c.advance(1000);
  assert.equal(reads, 0, 'a position hint waits longer than a content hint');
  await c.advance(3000);
  assert.equal(reads, 1);
  for (let i = 3; i < 9; i++) { hub.accept(progress('films', '1', String(i))); await c.advance(2000); }
  assert.equal(reads, 1, 'a stream of position hints does not become a stream of reads');
  await c.advance(15_000);
  assert.equal(reads, 2);
  // A watched tick is a real change and is not held back by the pace.
  hub.accept(personal('films', '2', '9'));
  await c.advance(1000);
  assert.equal(reads, 3);
  assert.deepEqual(progressOnly, [true, true, false]);
});

test('a failed quiet read keeps trying, a little later each time, and only a sustained failure is reported', async () => {
  const c = clock(), hub = new FreshnessHub({...c.env, backoffMs: [2000, 5000, 10_000], sustainedMs: 15_000});
  let attempts = 0, fail = true;
  const seen: string[] = [];
  hub.subscribeReachability(() => seen.push(hub.getReachability()));
  const doc = hub.document({dependencies: [dependsOn.content('films')], revalidate: async () => { attempts++; if (fail) throw Object.assign(new Error('down'), {status: 503}); }});
  doc.setOpen(true);
  hub.accept(library('films', '2'));
  await c.advance(500);
  assert.equal(attempts, 1);
  assert.equal(doc.getState().failures, 1);
  assert.equal(doc.getState().stale, true, 'the read is still owed');
  assert.equal(hub.getReachability(), 'reachable', 'one failure says nothing');
  await c.advance(2000);
  assert.equal(attempts, 2);
  await c.advance(5000);
  assert.equal(attempts, 3);
  assert.equal(hub.getReachability(), 'reachable');
  await c.advance(10_000);
  assert.equal(attempts, 4);
  assert.equal(hub.getReachability(), 'unreachable', 'failing for longer than the limit raises the quiet state');
  fail = false;
  await c.advance(10_000);
  assert.equal(attempts, 5);
  assert.equal(hub.getReachability(), 'reachable', 'the first success takes it down again');
  assert.deepEqual(seen, ['unreachable', 'reachable']);
  assert.equal(doc.getState().failures, 0);
  await c.advance(120_000);
  assert.equal(attempts, 5, 'a document that is current is left alone');
});

test('a refusal the server will repeat is an answer, not a failure to retry', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let attempts = 0;
  const doc = hub.document({dependencies: [dependsOn.content('films')], revalidate: async () => { attempts++; throw Object.assign(new Error('gone'), {status: 404}); }});
  doc.setOpen(true);
  hub.accept(library('films', '2'));
  await c.advance(120_000);
  assert.equal(attempts, 1);
  assert.equal(hub.getReachability(), 'reachable');
});

test('a read waits for a pause in input before it changes the screen', async () => {
  const c = clock(), hub = new FreshnessHub({...c.env, settleMs: 300, settleLimitMs: 5000, gatherMs: 100});
  let reads = 0;
  const doc = hub.document({dependencies: [dependsOn.content('films')], revalidate: async () => { reads++; }});
  doc.setOpen(true);
  hub.accept(library('films', '2'));
  // The person holds a direction key down: an input every 100 ms.
  for (let i = 0; i < 20; i++) { hub.touch(); await c.advance(100); }
  assert.equal(reads, 0);
  await c.advance(400);
  assert.equal(reads, 1);
  // Input that never stops does not hold a read for ever.
  hub.accept(library('films', '3'));
  for (let i = 0; i < 70; i++) { hub.touch(); await c.advance(100); }
  assert.equal(reads, 2);
});

test('the feed is the source: events move dependencies, a resync makes everything out of date, a lost feed is reported after a while', async () => {
  const c = clock(), hub = new FreshnessHub({...c.env, sustainedMs: 10_000});
  const events = new EventsClient({http: {send: async () => ({status: 200, headers: {}, body: {}})}});
  hub.attach(events);
  let reads = 0, other = 0;
  const doc = hub.document({dependencies: [dependsOn.libraries], revalidate: async () => { reads++; }});
  const inbox = hub.document({dependencies: [dependsOn.event('notification.changed')], revalidate: async () => { other++; }});
  doc.setOpen(true); inbox.setOpen(true);
  events.deliver({id: '1', type: 'libraries.changed', resource: {kind: 'catalogue', id: 'libraries'}, revision: '4', data: {directory: '4'}});
  await c.advance(1000);
  assert.deepEqual([reads, other], [1, 0]);
  events.deliver({id: '2', type: 'notification.changed', resource: {kind: 'inbox', id: 'inbox'}, revision: '8'});
  await c.advance(1000);
  assert.deepEqual([reads, other], [1, 1]);
  events.deliver({id: '3', type: 'stream.resync'});
  await c.advance(1000);
  assert.deepEqual([reads, other], [2, 2]);
  // A watcher that is not a document hears the same movement.
  let heard = 0;
  const off = hub.watch([dependsOn.settings('server')], () => heard++);
  events.deliver({id: '4', type: 'settings.changed', resource: {kind: 'settings', id: 'server'}, revision: '3'});
  events.deliver({id: '5', type: 'settings.changed', resource: {kind: 'settings', id: 'preferences'}, revision: '3'});
  assert.equal(heard, 1);
  off();
  hub.dispose();
});

test('the last known document is shown at once and read again only if it moved while away', async () => {
  const c = clock(), hub = new FreshnessHub(c.env), pool = new DocumentPool(hub, {max: 2});
  let made = 0, disposed = 0, quiet = 0;
  const make = () => {
    made++;
    const listeners = new Set<() => void>();
    let phase = 'ready';
    const service = {
      getSnapshot: () => ({phase}), subscribe: (l: () => void) => { listeners.add(l); return () => { listeners.delete(l); }; },
      revalidate: async () => { quiet++; }, dispose: () => { disposed++; }, set: (p: string) => { phase = p; for (const l of [...listeners]) l(); },
    };
    return serviceDocument(service, [dependsOn.content('films')]);
  };
  const first = pool.acquire('library:films', make);
  assert.equal(first.created, true);
  first.setOpen(true);
  first.memo.place = 'row 40';
  first.release();
  // Back to the same screen: the same service, with what it had, and no read.
  const again = pool.acquire('library:films', make);
  assert.equal(again.created, false);
  assert.equal(again.service, first.service);
  assert.equal(again.memo.place, 'row 40');
  again.setOpen(true);
  await c.advance(60_000);
  assert.deepEqual([made, quiet], [1, 0]);
  again.release();
  // Something moved while the screen was closed: shown at once, then read once, quietly.
  hub.accept(library('films', '2'));
  await c.advance(60_000);
  assert.equal(quiet, 0);
  const third = pool.acquire('library:films', make);
  third.setOpen(true);
  await c.advance(1000);
  assert.equal(quiet, 1);
  third.release();
  // The pool is bounded: the least recently used document nobody is showing goes first.
  pool.acquire('a', make).release(); pool.acquire('b', make).release();
  assert.equal(pool.size, 2);
  assert.equal(disposed, 1);
  pool.dispose();
  assert.equal(disposed, 3);
});

test('a first load that failed with nothing to show is tried again by itself', async () => {
  const c = clock(), hub = new FreshnessHub({...c.env, backoffMs: [2000, 4000]});
  const pool = new DocumentPool(hub);
  let retries = 0, up = false;
  const listeners = new Set<() => void>();
  let snapshot: {phase: string; error?: unknown} = {phase: 'loading'};
  const set = (next: {phase: string; error?: unknown}) => { snapshot = next; for (const l of [...listeners]) l(); };
  const service = {
    getSnapshot: () => snapshot, subscribe: (l: () => void) => { listeners.add(l); return () => { listeners.delete(l); }; },
    revalidate: async () => {},
    retry: async () => { retries++; set({phase: 'loading'}); set(up ? {phase: 'ready'} : {phase: 'error', error: Object.assign(new Error('down'), {status: 503})}); },
  };
  const held = pool.acquire('home', () => serviceDocument(service, [dependsOn.libraries]));
  held.setOpen(true);
  set({phase: 'error', error: Object.assign(new Error('down'), {status: 503})});
  await c.advance(1000);
  assert.equal(retries, 1);
  await c.advance(2000);
  assert.equal(retries, 2);
  up = true;
  await c.advance(4000);
  assert.equal(retries, 3);
  assert.equal(snapshot.phase, 'ready');
  await c.advance(120_000);
  assert.equal(retries, 3);
});

test('a hint that arrives while the first load is still on its way is answered once there is something to show', async () => {
  const c = clock(), hub = new FreshnessHub(c.env), pool = new DocumentPool(hub);
  let quiet = 0, retries = 0;
  const listeners = new Set<() => void>();
  let snapshot: {phase: string} = {phase: 'loading'};
  const service = {
    getSnapshot: () => snapshot, subscribe: (l: () => void) => { listeners.add(l); return () => { listeners.delete(l); }; },
    revalidate: async () => { quiet++; }, retry: async () => { retries++; },
  };
  const held = pool.acquire('library', () => serviceDocument(service, [dependsOn.content('films')]));
  held.setOpen(true);
  hub.accept(library('films', '2'));
  await c.advance(5000);
  assert.deepEqual([quiet, retries], [0, 0], 'nothing to keep yet, and a load in flight is not retried');
  assert.equal(held.document.getState().stale, true, 'the hint is still owed');
  snapshot = {phase: 'ready'};
  for (const l of [...listeners]) l();
  await c.advance(1000);
  assert.equal(quiet, 1);
  assert.equal(held.document.getState().stale, false);
});

test('History observations refresh history dependencies without moving personal membership', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let history = 0, personalReads = 0;
  const historyDoc = hub.document({dependencies: [dependsOn.progress(), dependsOn.personal()], revalidate: async () => { history++; }});
  const personalDoc = hub.document({dependencies: [dependsOn.personal()], revalidate: async () => { personalReads++; }});
  historyDoc.setOpen(true); personalDoc.setOpen(true);
  hub.accept({id: '1', type: 'progress.changed', resource: {kind: 'personal', id: 'history'}, revision: '5', data: {observation: '5'}});
  await c.advance(4000);
  assert.deepEqual([history, personalReads], [1, 0]);
  hub.accept({id: '2', type: 'personal.changed', resource: {kind: 'personal', id: 'history'}, revision: '6'});
  await c.advance(1000);
  assert.deepEqual([history, personalReads], [2, 1]);
  historyDoc.dispose(); personalDoc.dispose(); hub.dispose();
});

test('a list the server sends no hint for is read again when this device\'s clock says to look', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let recordings = 0, films = 0;
  const dvr = hub.document({dependencies: [dependsOn.clock('dvr'), dependsOn.event('notification.changed')], revalidate: async () => { recordings++; }});
  const other = hub.document({dependencies: [dependsOn.content('films')], revalidate: async () => { films++; }});
  dvr.setOpen(true); other.setOpen(true);
  hub.note(dependsOn.clock('dvr'));
  await c.advance(5_000);
  assert.equal(recordings, 1, 'read once, on the hub\'s usual terms');
  assert.equal(films, 0, 'nothing else is disturbed');
  // The one hint the server does send for it (a recording finished) reads it again too.
  hub.accept({id: 'n1', type: 'notification.changed', resource: {kind: 'inbox', id: 'inbox'}});
  await c.advance(5_000);
  assert.equal(recordings, 2);
  // A screen that is not open only remembers, and catches up once when shown again.
  dvr.setOpen(false);
  hub.note(dependsOn.clock('dvr')); hub.note(dependsOn.clock('dvr'));
  await c.advance(60_000);
  assert.equal(recordings, 2);
  dvr.setOpen(true);
  await c.advance(5_000);
  assert.equal(recordings, 3);
  hub.dispose();
});

test('a title page moves for a hint that names it or names nothing, not for another title', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let reads = 0, lists = 0;
  const page = hub.document({dependencies: titleWithPersonalState('shows', ['episode-1', 'show-1']), revalidate: async () => { reads++; }});
  const list = hub.document({dependencies: titlesWithPersonalState('shows'), revalidate: async () => { lists++; }});
  page.setOpen(true); list.setOpen(true);
  hub.accept({...personal('shows', '2'), data: {personal: '2', observation: '1', titles: ['episode-9', 'season-9', 'show-9']}});
  await c.advance(1000);
  assert.deepEqual([reads, lists], [0, 1], 'another show moved: the list reads, the page does not');
  hub.accept({...personal('shows', '3'), data: {personal: '3', observation: '1', titles: ['episode-2', 'season-1', 'show-1']}});
  await c.advance(1000);
  assert.deepEqual([reads, lists], [1, 2], 'an episode of this show moved: the show page reads');
  hub.accept(personal('shows', '4'));
  await c.advance(1000);
  assert.deepEqual([reads, lists], [2, 3], 'a hint that names nothing moves every title page of the library');
  hub.accept({...personal('films', '5'), data: {personal: '5', observation: '1'}});
  await c.advance(1000);
  assert.equal(reads, 2, 'another library does not move it');
});

test('a list of titles moves only for the titles it holds, unless its query reads personal state', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let held = ['show-1'], reads = 0, across = 0, played = 0;
  hub.document({dependencies: () => listedTitlesWithPersonalState('shows', held, false), revalidate: async () => { reads++; }}).setOpen(true);
  hub.document({dependencies: () => listedTitlesWithPersonalState(undefined, held, false), revalidate: async () => { across++; }}).setOpen(true);
  hub.document({dependencies: () => listedTitlesWithPersonalState('shows', held, readsPersonalState(['playState'])), revalidate: async () => { played++; }}).setOpen(true);
  hub.accept({...personal('shows', '2'), data: {personal: '2', observation: '1', titles: ['episode-9', 'season-9', 'show-9']}});
  await c.advance(1000);
  assert.deepEqual([reads, across, played], [0, 0, 1], 'a title not in the list moves only the list filtered by played state');
  held = ['show-1', 'show-9'];
  hub.accept({...personal('shows', '3'), data: {personal: '3', observation: '1', titles: ['episode-8', 'season-9', 'show-9']}});
  await c.advance(1000);
  assert.deepEqual([reads, across, played], [1, 1, 2], 'a title scrolled into view moves the lists');
  hub.accept({...personal('films', '4'), data: {personal: '4', observation: '1'}});
  await c.advance(1000);
  assert.deepEqual([reads, across, played], [1, 2, 2], 'a hint naming no title moves the list across libraries only for another library');
  assert.equal(readsPersonalState(['genre', 'title']), false);
  assert.equal(readsPersonalState(['genre', 'lastPlayedAt']), true);
});

test('a list hint for the viewer moves only the screens that depend on that list', async () => {
  const c = clock(), hub = new FreshnessHub(c.env);
  let guide = 0, downloads = 0;
  hub.document({dependencies: [dependsOn.list('guide'), dependsOn.list('myRecordings')], revalidate: async () => { guide++; }}).setOpen(true);
  hub.document({dependencies: [dependsOn.list('myDownloads')], revalidate: async () => { downloads++; }}).setOpen(true);
  hub.accept({id: 'r1', type: 'list.changed', resource: {kind: 'list', id: 'myRecordings'}, revision: '1'});
  await c.advance(1000);
  assert.deepEqual([guide, downloads], [1, 0]);
  hub.accept({id: 'd1', type: 'list.changed', resource: {kind: 'list', id: 'myDownloads'}, revision: '2'});
  await c.advance(1000);
  assert.deepEqual([guide, downloads], [1, 1]);
});
