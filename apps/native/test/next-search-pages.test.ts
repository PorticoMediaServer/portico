import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import {browseType} from '../src-next/kit/browse/presentation';
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

test('the shared upright tablet Top result can play the actual result without opening its page', async () => {
  const h = hooks(), calls: unknown[] = [], film = {id: 'film', title: 'A playable film', kind: 'movie', playback: {itemId: 'film'}};
  const frameMetrics = {form: 'medium', width: 740, cardInset: 12, cardPadding: 14, headingGap: 14};
  const {TopResult} = await screenModule(new URL('../src-next/kit/search/Search.tsx', import.meta.url), {
    react: {...h.react, useId: () => 'gradient'}, 'react-native': {View: 'View', Text: 'Text'}, 'react-native-svg': {},
    '../artwork': {Artwork: 'Artwork'}, '../controls/Button': {Button: 'Button'}, '../controls/Chip': {Chip: 'Chip'},
    '../controls/Focusable': {Focusable: 'Focusable'}, '../Icon': {Icon: 'Icon'}, '../theme': {useTheme: () => createTheme({isTV: false, width: 820, height: 1180})},
    '../frames/metrics': {useFrameMetrics: () => frameMetrics}, '../browse/presentation': {browseType},
    '../cards/primitives': {CardFrame: 'CardFrame'}, '../selection': {},
  });
  const {SearchPage} = await screenModule(new URL('../src-next/pages/touch/search/SearchPage.tsx', import.meta.url), {
    react: h.react, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/frames/metrics': {useFrameMetrics: () => frameMetrics},
    '../../../../../../packages/client-core/src/presentation/search-model': {searchGroupLabel: () => 'search.chip.movies', topResultKindLabel: () => 'search.chip.movies'},
    '../../../kit/artwork': {}, '../../../kit/cards/primitives': {Parts: 'Parts'}, '../../../kit/controls': {TextField: 'TextField'},
    '../../../kit/frames': {SearchFrame: 'SearchFrame'}, '../../../kit/overlays': {}, '../../../kit/search': {TopResult},
    '../../../kit/search/SearchResults': {}, '../../../kit/states': {}, '../../../screens/search/genre-tiles': {}, '../../../screens/search/model': {},
    '../../shared/search/useSearch': {useSearch: () => ({
      t: defaultI18n.t, active: {visible: true}, state: {text: film.title, recent: [], group: 'all', search: {phase: 'ready'}}, typed: true,
      groups: [], landing: {phase: 'idle'}, top: {entry: film, group: 'movies'},
    })},
  });
  const done = async () => {};
  const tree = h.render(() => SearchPage({runtime: {artwork: () => undefined, registerScrollToTop: () => () => {}},
    destination: {refreshHistory: done}, explore: {load: done}, actions: {play: (...args: unknown[]) => calls.push(args), open: () => calls.push('open')}}));
  const elements = (value: any): any[] => Array.isArray(value) ? value.flatMap(elements) : value?.props ? [value, ...elements(value.props.children)] : [];
  const top = elements(tree).find(node => node.type === TopResult);
  const card = elements(h.render(() => TopResult(top.props))).find(node => node.type === 'CardFrame');
  const contents = card.props.children.find((node: unknown) => typeof node === 'function')({});
  const play = elements(contents).find(node => node.props.testID === 'search-top-play');
  assert.equal(play.props.iconOnly, undefined);
  assert.equal(play.props.children[0], defaultI18n.t('entry.play'));
  play.props.onPress();
  assert.deepEqual(calls, [[film, 'movies']]);
});
