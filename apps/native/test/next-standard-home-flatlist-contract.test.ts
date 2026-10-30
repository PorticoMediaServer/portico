import assert from 'node:assert/strict';
import test from 'node:test';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

/**
 * The standard-home loader replaces react-native's list with a host boundary, so the
 * boundary itself is the thing under test here. The installed list answers an
 * imperative scrollToIndex from the props of the host it currently renders, counts
 * `getItemCount(data)` or `data.length`, asks `getItemLayout(data, index)` for the
 * cell, and moves the real scroll view along its own axis. This regression mounts
 * the boundary on its own and rerenders it so a closure over the first render's
 * props cannot pass: index 3 exists only in the committed data, and its offset only
 * in the committed layout.
 */

test('the standard-home FlatList boundary answers scrollToIndex from the committed host props on its own axis', async () => {
  const app = await standardHomeApp({mountUI: false});
  const host = await fabricHost();
  const {FlatList, View} = await import('react-native');
  const tree = (data: readonly string[], base: number, horizontal: boolean) =>
    React.createElement(FlatList, {
      testID: 'harness-flatlist-contract',
      data,
      horizontal,
      style: {width: 40, height: 60},
      keyExtractor: (value: string) => value,
      getItemLayout: (_data: unknown, index: number) => ({length: 10, offset: base + index * 10, index}),
      renderItem: ({item}: {item: string}) => React.createElement(View, {testID: `harness-flatlist-row-${item}`}),
    });
  const list = () =>
    app.hosts().find((entry: any) => entry.props?.testID === 'harness-flatlist-contract');
  try {
    await host.render(tree(['a', 'b'], 100, false));
    await host.settle();
    assert.ok(list(), 'the boundary mounted the real FlatList element');
    assert.ok(
      app.hosts().some((entry: any) => entry.props?.testID === 'harness-flatlist-row-a'),
      'the boundary draws the data it was given through renderItem',
    );

    // Rerender with both rows and layout replaced. Index 3 exists only in the new
    // data, so stale props would throw out of range or use the old offset 130.
    await host.render(tree(['a', 'b', 'c', 'd'], 200, false));
    await host.settle();
    list().host.scrollToIndex({index: 3, animated: false});
    assert.deepEqual(
      {...list().host.contentOffset},
      {x: 0, y: 230},
      'the committed layout offset and data answer the vertical command',
    );
    assert.ok(
      app.hosts().some((entry: any) => entry.props?.testID === 'harness-flatlist-row-d'),
      'the rerendered row is drawn from the committed data',
    );

    // FlatList scrollToOffset writes x for a horizontal list.
    await host.render(tree(['a', 'b', 'c', 'd'], 50, true));
    await host.settle();
    list().host.scrollToIndex({index: 2, animated: false});
    assert.deepEqual(
      {...list().host.contentOffset},
      {x: 70, y: 230},
      'the horizontal list moves its own axis from the same committed layout',
    );
    list().host.scrollToIndex({index: 2, viewPosition: 1, viewOffset: 3});
    assert.deepEqual({...list().host.contentOffset}, {x: 37, y: 230}, 'horizontal view positioning uses the drawn width and exact caller offset');
  } finally {
    try {
      await host.unmount();
    } finally {
      try {
        await app.unmount();
      } finally {
        app.disposeLoader();
      }
    }
  }
});
