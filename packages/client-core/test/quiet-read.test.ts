import test from 'node:test';
import assert from 'node:assert/strict';
import {FreshnessHub, dependsOn} from '../src/freshness-hub.ts';
import {QuietRead} from '../src/server-admin/quiet-read.ts';

/** A hub on a clock the test moves: timers fire in order as time is advanced. */
function world() {
  let now = 0, seq = 0;
  const timers: {at: number; id: number; fn: () => void}[] = [];
  const hub = new FreshnessHub({now: () => now, random: () => 0, setTimer: (fn, ms) => { const t = {at: now + ms, id: ++seq, fn}; timers.push(t); return t; }, clearTimer: t => { const i = timers.indexOf(t as never); if (i >= 0) timers.splice(i, 1); }});
  const advance = async (ms: number) => {
    const until = now + ms;
    for (;;) {
      await new Promise(resolve => setImmediate(resolve));
      const due = timers.filter(t => t.at <= until).sort((a, b) => a.at - b.at || a.id - b.id)[0];
      if (!due) break;
      timers.splice(timers.indexOf(due), 1);
      now = due.at;
      due.fn();
    }
    now = until;
    await new Promise(resolve => setImmediate(resolve));
  };
  return {hub, advance};
}
const offline = () => new TypeError('Failed to fetch');
const event = (type: string) => ({id: String(Math.random()), type, at: '', resource: {kind: 'x', id: ''}, revision: '1'}) as never;

test('a panel that is open is read again when what it was built from moves, without blanking or spinning', async () => {
  const {hub, advance} = world();
  let list = ['one'], reads = 0;
  const read = new QuietRead<string[]>(hub);
  const shown: string[] = [];
  read.subscribe(() => { const s = read.getSnapshot(); shown.push(`${s.loading ? 'loading' : 'idle'}:${s.data?.join(',') ?? '-'}`); });
  read.configure(async () => { reads++; return list; }, [dependsOn.event('operation.updated')]);
  read.setOpen(true);
  read.reload(false);
  await advance(0);
  assert.deepEqual(read.getSnapshot().data, ['one']);
  shown.length = 0;
  list = ['one', 'two'];
  hub.accept(event('operation.updated'));
  await advance(1000);
  assert.deepEqual(read.getSnapshot().data, ['one', 'two']);
  assert.deepEqual(shown, ['idle:one,two'], 'one change on screen: the new list, never a loading state or an empty one');
  // Something unrelated moves: nothing is read.
  const before = reads;
  hub.accept(event('library.scan.updated'));
  await advance(1000);
  assert.equal(reads, before);
  // The feed could not say everything that happened: the panel catches up.
  // It read a moment ago, so the catch-up waits out the hub's content spacing.
  hub.resync();
  await advance(5000);
  assert.equal(reads, before + 1);
  read.dispose();
  // A read that named nothing to follow (a document opened to be edited) is left as it was read.
  const once = new QuietRead<string[]>(hub);
  let onceReads = 0;
  once.configure(async () => { onceReads++; return ['doc']; });
  once.setOpen(true);
  once.reload(false);
  await advance(0);
  hub.resync();
  await advance(1000);
  assert.equal(onceReads, 1);
  once.dispose();
});

test('a read nobody asked for that fails says nothing, keeps what is shown, and is tried again', async () => {
  const {hub, advance} = world();
  let fail = false, reads = 0;
  const read = new QuietRead<number>(hub);
  read.configure(async () => { reads++; if (fail) throw offline(); return reads; }, [], 30_000);
  read.setOpen(true);
  read.reload(false);
  await advance(0);
  assert.equal(read.getSnapshot().data, 1);
  fail = true;
  await advance(31_000);
  assert.equal(read.getSnapshot().data, 1);
  assert.equal(read.getSnapshot().error, undefined, 'a quiet failure is not an error on the panel');
  assert.ok(reads >= 2);
  fail = false;
  await advance(60_000);
  assert.equal(read.getSnapshot().error, undefined);
  assert.ok((read.getSnapshot().data ?? 0) > 1, 'it caught up by itself');
  read.dispose();
});

test('a panel with nothing on the feed to tell it looks again at a calm pace, only while it is open', async () => {
  const {hub, advance} = world();
  let reads = 0;
  const read = new QuietRead<number>(hub);
  read.configure(async () => ++reads, [], 30_000);
  read.setOpen(true);
  read.reload(false);
  await advance(0);
  await advance(95_000);
  assert.equal(reads, 4, 'the first read, then one each half minute');
  read.setOpen(false);
  await advance(120_000);
  assert.equal(reads, 4);
  // In the background nothing runs either.
  read.setOpen(true);
  hub.setActive(false);
  await advance(120_000);
  assert.equal(reads, 4);
  read.dispose();
});

test('a first load gets quiet attempts before its error is shown, and goes on trying behind it', async () => {
  const {hub, advance} = world();
  let fail = true, reads = 0;
  const read = new QuietRead<string>(hub);
  read.configure(async () => { reads++; if (fail) throw offline(); return 'here'; });
  read.setOpen(true);
  read.reload(false);
  await advance(1000);
  assert.equal(read.getSnapshot().loading, true, 'still loading as far as the owner can tell');
  assert.equal(read.getSnapshot().error, undefined);
  await advance(4000);
  assert.equal(read.getSnapshot().loading, false);
  assert.ok(read.getSnapshot().error instanceof TypeError, 'after a couple of attempts the panel says so');
  fail = false;
  await advance(60_000);
  assert.equal(read.getSnapshot().data, 'here');
  assert.equal(read.getSnapshot().error, undefined, 'the error goes when a later attempt succeeds');
  read.dispose();
});

test('a read the owner asked for answers at once, and a refusal is never retried in silence', async () => {
  const {hub, advance} = world();
  let failure: unknown = offline();
  const read = new QuietRead<string>(hub);
  read.configure(async () => { if (failure) throw failure; return 'ok'; });
  read.setOpen(true);
  read.reload();
  await advance(0);
  assert.ok(read.getSnapshot().error instanceof TypeError);
  assert.equal(read.getSnapshot().loading, false);
  // Not allowed is an answer, even for a first load nobody pressed anything for.
  failure = Object.assign(new Error('no'), {status: 403, code: 'forbidden'});
  read.reload(false);
  await advance(0);
  assert.equal((read.getSnapshot().error as {status: number}).status, 403);
  failure = undefined;
  read.reload();
  await advance(0);
  assert.equal(read.getSnapshot().data, 'ok');
  assert.equal(read.getSnapshot().error, undefined);
  read.dispose();
});

test('the owner\'s own read wins over a quiet one in flight', async () => {
  const {hub, advance} = world();
  const answers: ((v: string) => void)[] = [];
  const read = new QuietRead<string>(hub);
  read.configure(() => new Promise<string>(resolve => { answers.push(resolve); }), [dependsOn.settings('server')]);
  read.setOpen(true);
  read.reload(false);
  await advance(0);
  answers.shift()!('first');
  await advance(0);
  hub.accept({id: '1', type: 'settings.changed', at: '', resource: {kind: 'settings', id: 'server'}, revision: '2'} as never);
  await advance(1000);
  assert.equal(answers.length, 1, 'the quiet read is in flight');
  read.reload();
  await advance(0);
  answers.pop()!('asked');
  answers.shift()!('quiet, late');
  await advance(1000);
  assert.equal(read.getSnapshot().data, 'asked');
  read.dispose();
});
