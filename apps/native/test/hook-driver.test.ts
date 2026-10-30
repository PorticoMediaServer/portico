import test from 'node:test';
import assert from 'node:assert/strict';
import {fabricHost, React} from './helpers/fabric-host.mjs';

function store(value: number) {
  const listeners = new Set<() => void>();
  return {listeners, getSnapshot: () => value, subscribe: (listener: () => void) => {
    listeners.add(listener); return () => { listeners.delete(listener); };
  }, change(next: number) { value = next; for (const listener of listeners) listener(); }};
}

test('Fabric replaces a store subscription and reacts only to the current store', async () => {
  const first = store(1), second = store(2), host = await fabricHost();
  let selected = first, shown = 0, renders = 0;
  function Consumer() { renders++; shown = React.useSyncExternalStore(selected.subscribe, selected.getSnapshot); return null; }
  try {
    await host.render(React.createElement(Consumer));
    assert.equal(first.listeners.size, 1);
    selected = second;
    await host.render(React.createElement(Consumer));
    assert.equal(first.listeners.size, 0);
    assert.equal(second.listeners.size, 1);
    const before = renders;
    first.change(9); await host.settle();
    assert.equal(renders, before);
    second.change(3); await host.settle();
    assert.equal(shown, 3);
  } finally { await host.unmount(); }
  assert.equal(second.listeners.size, 0);
});

test('Fabric preserves setter identity and commits layout before passive subscriptions', async () => {
  const host = await fabricHost(), source = store(1), events: string[] = [];
  let rendering = false, update: (value: number) => void = () => {}, firstSetter: unknown, shown = 0;
  const subscribe = (listener: () => void) => {
    assert.equal(rendering, false, 'an external store must not subscribe during render');
    events.push('subscribe');
    return source.subscribe(listener);
  };
  function Consumer() {
    rendering = true;
    try {
      const [value, setValue] = React.useState(0);
      update = setValue;
      if (firstSetter === undefined) firstSetter = setValue;
      else assert.equal(setValue, firstSetter, 'React keeps the same setter');
      // Deliberately declare the passive effect before the layout effect.
      React.useEffect(() => {events.push(`passive:${value}`); return () => {events.push(`passive-cleanup:${value}`);};}, [value]);
      React.useLayoutEffect(() => {
        events.push(`layout:${value}`);
        // A publication between render and passive subscribe must still be observed.
        if (value === 0) source.change(2);
        return () => {events.push(`layout-cleanup:${value}`);};
      }, [value]);
      shown = React.useSyncExternalStore(subscribe, source.getSnapshot);
      return null;
    } finally {rendering = false;}
  }
  try {
    await host.render(React.createElement(Consumer));
    assert.equal(shown, 2);
    assert.ok(events.indexOf('layout:0') >= 0 && events.indexOf('layout:0') < events.indexOf('passive:0'));
    assert.ok(events.indexOf('layout:0') < events.indexOf('subscribe'));
    update(1); await host.settle();
    assert.ok(events.indexOf('layout:1') >= 0 && events.indexOf('layout:1') < events.indexOf('passive:1'));
  } finally {await host.unmount();}
  assert.equal(source.listeners.size, 0);
  assert.ok(events.includes('layout-cleanup:1'));
  assert.ok(events.includes('passive-cleanup:1'));
});
