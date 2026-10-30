// @fabric-react
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {fabricHost} from './helpers/fabric-host.mjs';

test('Fabric probes preserve setter identity and separate layout from passive replacement and cleanup', async () => {
  const events: string[] = [];
  let update!: React.Dispatch<React.SetStateAction<number>>;
  const probe = createFabricHookHost(() => {
    const [value, setValue] = React.useState(0);
    update = setValue;
    React.useEffect(() => {events.push(`passive:${value}`); return () => {events.push(`passive-clean:${value}`);};}, [value]);
    React.useLayoutEffect(() => {events.push(`layout:${value}`); return () => {events.push(`layout-clean:${value}`);};}, [value]);
  });
  await probe.mount();
  const originalSetter = update;
  assert.deepEqual(events, ['layout:0', 'passive:0']);
  events.length = 0;
  await act(() => update(1));
  assert.equal(update, originalSetter);
  assert.deepEqual(events, ['layout-clean:0', 'layout:1', 'passive-clean:0', 'passive:1']);
  events.length = 0;
  await probe.unmount();
  assert.deepEqual(events, ['layout-clean:1', 'passive-clean:1']);
});

test('Fabric catches a store publication between render and passive subscription', async () => {
  let version = 0, value = -1, removals = 0;
  const events: string[] = [];
  const subscribe = () => {events.push('subscribe'); return () => {removals++;};};
  const snapshot = () => version;
  const probe = createFabricHookHost(() => {
    value = React.useSyncExternalStore(subscribe, snapshot);
    events.push(`render:${value}`);
    React.useLayoutEffect(() => {events.push('layout'); version = 1;}, []);
  });
  await probe.mount();
  assert.ok(events.indexOf('render:0') < events.indexOf('layout'));
  assert.ok(events.indexOf('layout') < events.indexOf('subscribe'));
  assert.equal(value, 1, 'the subscription check recovers the publication it did not hear');
  await probe.unmount();
  assert.equal(removals, 1);
});

test('Fabric owns native callback ref cleanup, ref replacement and keyed host identity', async () => {
  const host = await fabricHost();
  const events: string[] = [], tags: number[] = [];
  const ref = (name: string) => (instance: {tag: number}) => {
    tags.push(instance.tag); events.push(`attach:${name}`);
    return () => {events.push(`cleanup:${name}`);};
  };
  const first = ref('first'), second = ref('second');
  try {
    await host.render(React.createElement('View', {key: 'a', ref: first}));
    await host.render(React.createElement('View', {key: 'a', ref: second}));
    assert.equal(tags[0], tags[1], 'changing the ref preserves the committed native host');
    await host.render(React.createElement('View', {key: 'b', ref: second}));
    assert.notEqual(tags[1], tags[2], 'a new key mounts a new native host');
  } finally {await host.unmount();}
  assert.deepEqual(events, ['attach:first', 'cleanup:first', 'attach:second', 'cleanup:second', 'attach:second', 'cleanup:second']);
});
