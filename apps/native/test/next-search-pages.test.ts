import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../../packages/i18n/src';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('phone Search keeps the query field and lets a viewer return from an empty group to All', async () => {
  const h = hooks(), calls: string[] = [];
  const artwork = () => undefined;
  const state = {text: 'Scarlet', group: 'movies', search: {phase: 'ready'}, groups: [], recent: []};
  const {SearchPage} = await screenModule(new URL('../src-next/pages/touch/search/SearchPage.tsx', import.meta.url), {
    react: h.react, '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../kit/frames/metrics': {useFrameMetrics: () => ({form: 'phone'})},
    '../../../../../../packages/client-core/src/presentation/search-model': {searchGroupLabel: () => 'search.chip.movies'},
    '../../../kit/artwork': {Artwork: 'Artwork'}, '../../../kit/cards/primitives': {Parts: 'Parts'},
    '../../../kit/controls': {Button: 'Button', Chip: 'Chip', TextField: 'TextField'},
    '../../../kit/frames': {SearchFrame: 'SearchFrame'}, '../../../kit/overlays': {Banner: 'Banner'},
    '../../../kit/search': {SearchHistory: 'History', TopResult: 'TopResult'}, '../../../kit/search/SearchResults': {},
    '../../../kit/states': {StateView: 'State'}, '../../../screens/search/genre-tiles': {nativeSearchGenreTiles: () => []},
    '../../../screens/search/model': {},
    '../../shared/search/useSearch': {useSearch: () => ({
      t: defaultI18n.t, active: {visible: true}, state, typed: true, emptyIn: 'movies', groups: [], landing: {phase: 'idle'},
    })},
  });
  const done = async () => {};
  const frame = h.render(() => SearchPage({
    runtime: {artwork, registerScrollToTop: () => () => {}}, explore: {load: done},
    destination: {refreshHistory: done, setText: (value: string) => calls.push(value), selectGroup: (group: string) => calls.push(group)},
  }));
  assert.equal(frame.props.field.props.value, 'Scarlet');
  assert.equal(frame.props.none.testID, 'search-none-in');
  frame.props.none.action.props.onPress();
  frame.props.field.props.onChangeText('Pride');
  frame.props.cancel.props.onPress();
  assert.deepEqual(calls, ['all', 'Pride', '']);
});
