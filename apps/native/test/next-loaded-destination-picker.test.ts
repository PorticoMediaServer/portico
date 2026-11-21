import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('the destination picker draws a 2 by 2 mosaic for a row with several covers on touch, one cover on TV, and Add writes through the open page', async () => {
  for (const isTV of [false, true]) {
    const host = hooks(), chosen: unknown[] = [];
    const token = {}, page = {kind: 'playlist', token, nextCursor: '', rows: [
      {id: 'p1', name: 'Road trip', count: 3, canAdd: true, artwork: [{posterUrl: 'a'}, {posterUrl: 'b'}, {backdropUrl: 'c'}]},
      {id: 'p2', name: 'Solo', count: 1, canAdd: true, alreadyIn: 1, artwork: [{posterUrl: 'z'}]}]};
    const snapshot = {picker: page, busy: false, completed: 0, playlist: {}, collection: {}, collectionOutcome: {added: [], unchanged: [], failed: []}, collectionReceipts: [], pickerPrevious: false};
    const controller = {subscribe: () => () => {}, getSnapshot: () => snapshot, snapshot: () => snapshot, choose: async (...args: unknown[]) => {chosen.push(args);}};
    const {useLoadedDestinationPicker} = await screenModule(new URL('../src-next/pages/shared/selection/useLoadedDestinationPicker.ts', import.meta.url), {react: host.react,
      '../../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: ''})}});
    const {NativeDestinationPicker} = await screenModule(new URL('../src-next/kit/selection/LoadedDestinationPicker.tsx', import.meta.url), {react: host.react,
      'react-native': {View: 'View'}, './Picker': {PlaylistPicker: 'PlaylistPicker', CollectionPicker: 'CollectionPicker'}, '../theme': {useTheme: () => ({isTV})},
      '../../pages/shared/selection/useLoadedDestinationPicker': {useLoadedDestinationPicker}});
    const tree = host.render(() => NativeDestinationPicker({controller, kind: 'playlist', open: true, selectionLabel: '2 titles', count: 2, artwork: (path?: string) => path ? `art:${path}` : null, onOpenChange() {}}));
    assert.equal(tree.type, 'PlaylistPicker');
    const [first, second] = tree.props.entries;
    assert.deepEqual([first.id, first.title, first.size, second.alreadyIn], ['p1', 'Road trip', 3, 1]);
    if (isTV) assert.equal(first.artwork, 'art:a', 'TV keeps one cover');
    else assert.equal([first.artwork.props.children].flat(2).length, 4, 'touch draws the 2 by 2 cells');
    assert.equal(second.artwork, 'art:z', 'one cover stays one cover');
    tree.props.onAdd('p2'); assert.deepEqual(chosen, [['playlist', 'p2', token]]);
  }
});
