import test from 'node:test';
import assert from 'node:assert/strict';
import {FreshnessHub, dependsOn} from '../src/freshness-hub.ts';
import {createViewerQueries, type ViewerQueries} from '../src/query/viewer-queries.ts';
import {createQueryHooks, type ReactHooks} from '../src/query/react.ts';
import {queryKeys} from '../src/query/keys.ts';

// query-core hands notifications to observers on its scheduler (a zero timeout), as React would see them.
const flush = async () => { for (let i = 0; i < 5; i++) await new Promise(resolve => setTimeout(resolve, 1)); };

/**
 * The few hook semantics the adapter relies on, as React gives them: memo and callback by deps,
 * effects after render when deps change, and an external store that re-renders when it notifies
 * and re-subscribes when its subscribe function changes.
 */
function host<P, R>(component: (hooks: ReactHooks, props: P) => R) {
  const slots: unknown[] = [];
  let effects: (() => void)[] = [];
  let props!: P, result!: R, renders = 0, slot = 0, live = true;
  const cleanups = new Map<number, () => void>();
  const same = (a?: readonly unknown[], b?: readonly unknown[]) => !!a && !!b && a.length === b.length && a.every((v, i) => Object.is(v, b[i]));
  const hooks: ReactHooks = {
    useMemo(factory, deps) {
      const i = slot++, held = slots[i] as {deps: readonly unknown[]; value: unknown} | undefined;
      if (held && same(held.deps, deps)) return held.value as never;
      const value = factory(); slots[i] = {deps, value}; return value;
    },
    useCallback(callback, deps) { return hooks.useMemo(() => callback, deps); },
    useEffect(effect, deps) {
      const i = slot++, held = slots[i] as {deps?: readonly unknown[]} | undefined;
      if (held && deps && same(held.deps, deps)) return;
      slots[i] = {deps};
      effects.push(() => { cleanups.get(i)?.(); const done = effect(); if (typeof done === 'function') cleanups.set(i, done); else cleanups.delete(i); });
    },
    useSyncExternalStore(subscribe, getSnapshot) {
      const i = slot++, held = slots[i] as {subscribe: typeof subscribe; stop: () => void} | undefined;
      if (!held || held.subscribe !== subscribe) {
        held?.stop();
        slots[i] = {subscribe, stop: subscribe(() => { if (live) render(); })};
      }
      return getSnapshot();
    },
  };
  function render(next: P = props) {
    props = next; slot = 0; renders++;
    result = component(hooks, props);
    const run = effects; effects = [];
    for (const effect of run) effect();
    return result;
  }
  return {
    render, get result() { return result; }, get renders() { return renders; },
    unmount() {
      live = false;
      for (const done of cleanups.values()) done();
      for (const held of slots) (held as {stop?: () => void} | undefined)?.stop?.();
    },
  };
}

const made: ViewerQueries[] = [];
test.afterEach(() => { for (const q of made.splice(0)) q.dispose(); });
function viewer(id: string, answers: Map<string, unknown>, calls: string[]) {
  const q = createViewerQueries({scope: {serverId: 'server', viewerId: id}, hub: new FreshnessHub({gatherMs: 0, settleMs: 0, random: () => 0}), api: {
    async request<T>(path: string): Promise<T> { calls.push(path); await Promise.resolve(); return answers.get(path) as T; },
  }});
  made.push(q);
  return q;
}
const title = (queries: ViewerQueries, id: string) => ({
  queryKey: queryKeys.itemDetail(id, 'none'),
  queryFn: ({signal}: {signal: AbortSignal}) => queries.api.request<{title: string}>(`/v1/items/${id}/detail`, 'GET', undefined, signal),
  meta: {dependencies: [dependsOn.content('films')]},
});

test('a screen gets its document, a second screen shares the read, and Back draws it at once', async () => {
  const calls: string[] = [], answers = new Map<string, unknown>([['/v1/items/a/detail', {title: 'Arrival'}]]);
  const queries = viewer('alex', answers, calls);
  const screen = (q: ViewerQueries) => host((hooks, id: string) => createQueryHooks(hooks).useViewerQuery(q, title(q, id)));
  const one = screen(queries), two = screen(queries);
  assert.equal(one.render('a').isPending, true);
  two.render('a');
  await flush();
  assert.equal(calls.length, 1, 'one read for both screens');
  assert.equal(one.result.data?.title, 'Arrival');
  assert.equal(two.result.data?.title, 'Arrival');
  one.unmount(); two.unmount();
  const back = screen(queries);
  const first = back.render('a');
  assert.equal(first.isPending, false, 'drawn on its first render');
  assert.equal(first.data?.title, 'Arrival');
  await flush();
  assert.equal(calls.length, 1, 'and nothing is read');
  back.unmount(); queries.dispose();
});

test('a key change shows the new document\'s state in the same render, never the previous document', async () => {
  const calls: string[] = [], answers = new Map<string, unknown>([['/v1/items/a/detail', {title: 'A'}], ['/v1/items/b/detail', {title: 'B'}]]);
  const queries = viewer('alex', answers, calls);
  const page = host((hooks, id: string) => createQueryHooks(hooks).useViewerQuery(queries, title(queries, id)));
  page.render('a'); await flush();
  assert.equal(page.result.data?.title, 'A');
  const moved = page.render('b');
  assert.equal(moved.data, undefined, 'never another title\'s page under this key');
  assert.equal(moved.isPending, true);
  await flush();
  assert.equal(page.result.data?.title, 'B');
  page.unmount(); queries.dispose();
});

test('privacy: a screen moved to another viewer\'s cache never shows the first viewer\'s document', async () => {
  const calls: string[] = [], answers = new Map<string, unknown>([['/v1/items/a/detail', {title: 'for Alex'}]]);
  const alex = viewer('alex', answers, calls);
  const page = host((hooks, q: ViewerQueries) => createQueryHooks(hooks).useViewerQuery(q, title(q, 'a')));
  page.render(alex); await flush();
  assert.equal(page.result.data?.title, 'for Alex');
  alex.dispose();
  assert.equal(page.render(alex).data, undefined, 'a screen of the ended viewer that renders again draws nothing');
  await flush();
  assert.equal(page.result.data, undefined, 'and the mounted screen is told its document is gone');
  assert.equal(calls.length, 1, 'nothing is read for the ended viewer');
  answers.set('/v1/items/a/detail', {title: 'for Kids'});
  const kids = viewer('kids', answers, calls);
  const next = page.render(kids);
  assert.equal(next.data, undefined, 'the next viewer starts empty');
  await flush();
  assert.equal(page.result.data?.title, 'for Kids');
  page.unmount(); kids.dispose();
});
