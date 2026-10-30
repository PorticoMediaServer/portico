import test from 'node:test';
import assert from 'node:assert/strict';
import {leaseLock, type LeaseEnvironment} from '../src/platform/tab-lease.ts';

/** One browser's local storage shared by several simulated tabs, on a virtual clock. */
function browser() {
  const store = new Map<string, string>();
  let now = 1_000_000;
  let timers: {at: number; run: () => void; id: number}[] = [];
  let nextId = 1;
  const tabs: {listeners: Set<() => void>}[] = [];
  let seed = 1;
  function tab(): LeaseEnvironment & {leave: () => void; leaving: Set<() => void>} {
    const self = {listeners: new Set<() => void>()};
    tabs.push(self);
    const leaving = new Set<() => void>();
    // A write in one tab is heard by every other tab (the `storage` event), never by itself.
    const changed = () => { for (const other of tabs) if (other !== self) for (const l of other.listeners) l(); };
    return {
      read: key => store.get(key) ?? null,
      write: (key, value) => { store.set(key, value); changed(); },
      remove: key => { store.delete(key); changed(); },
      onChange: (_key, listener) => { self.listeners.add(listener); return () => self.listeners.delete(listener); },
      onLeave: listener => { leaving.add(listener); return () => leaving.delete(listener); },
      now: () => now,
      random: () => { seed = (seed * 16807) % 2147483647; return seed / 2147483647; },
      setTimer: (run, ms) => { const id = nextId++; timers.push({at: now + ms, run, id}); return id; },
      clearTimer: id => { timers = timers.filter(t => t.id !== id); },
      leave: () => { for (const l of [...leaving]) l(); },
      leaving,
    };
  }
  async function advance(ms: number) {
    const end = now + ms;
    for (;;) {
      await new Promise(resolve => setImmediate(resolve));
      timers.sort((a, b) => a.at - b.at);
      const next = timers[0];
      if (!next || next.at > end) break;
      timers.shift();
      now = next.at;
      next.run();
    }
    now = end;
    await new Promise(resolve => setImmediate(resolve));
  }
  return {tab, advance, store};
}

test('tabs on a plain-HTTP page take turns holding one lease', async () => {
  const b = browser();
  const a = leaseLock(b.tab()), c = leaseLock(b.tab());
  const running: string[] = [];
  const finish: Record<string, () => void> = {};
  const work = (name: string) => () => new Promise<void>(resolve => { running.push(name); finish[name] = () => { running.splice(running.indexOf(name), 1); resolve(); }; });
  const first = a('events.viewer', new AbortController().signal, work('a'));
  const second = c('events.viewer', new AbortController().signal, work('c'));
  await b.advance(10_000);
  assert.deepEqual(running, ['a'], 'one tab holds the stream; the other waits');
  finish.a();
  await first;
  await b.advance(200);
  assert.deepEqual(running, ['c'], 'a release hands over straight away');
  finish.c();
  await second;
  assert.equal(b.store.size, 0, 'nothing is left behind');
});

test('a lease that runs out is taken over, and the old holder is told', async () => {
  const b = browser();
  const frozenTab = b.tab();
  const frozen = leaseLock(frozenTab, {renewMs: 2_000, ttlMs: 8_000});
  let lost: AbortSignal | undefined;
  void frozen('events.viewer', new AbortController().signal, held => new Promise<void>(resolve => { lost = held; held?.addEventListener('abort', () => resolve()); }));
  await b.advance(500);
  assert.ok(lost && !lost.aborted);
  // The holder's renewals stop (a frozen tab): pretend its timers never ran by writing an old lease.
  const key = [...b.store.keys()][0];
  const stale = JSON.parse(b.store.get(key)!);
  b.store.set(key, JSON.stringify({...stale, until: 0}));
  const other = leaseLock(b.tab());
  let took = false;
  void other('events.viewer', new AbortController().signal, () => { took = true; return new Promise<void>(() => {}); });
  await b.advance(5_000);
  assert.ok(took, 'a lapsed lease is taken');
  assert.ok(lost!.aborted, 'the tab that lost it stops its stream at its next renewal');
});

test('a waiting tab gives up when its caller does', async () => {
  const b = browser();
  void leaseLock(b.tab())('events.viewer', new AbortController().signal, () => new Promise<void>(() => {}));
  await b.advance(500);
  const stop = new AbortController();
  const waiting = leaseLock(b.tab())('events.viewer', stop.signal, async () => { throw new Error('must not run'); });
  await b.advance(3_000);
  stop.abort();
  await assert.rejects(waiting, (error: Error) => error.name === 'AbortError');
});

test('leaving the page releases a held lease at once', async () => {
  const b = browser();
  const tab = b.tab();
  void leaseLock(tab)('events.viewer', new AbortController().signal, () => new Promise<void>(() => {}));
  await b.advance(500);
  assert.equal(b.store.size, 1);
  tab.leave();
  assert.equal(b.store.size, 0);
});
