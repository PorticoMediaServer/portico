import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
test('the phone Libraries overview keeps real summary order, count facts, art and exact order hint', async () => {
  const host = hooks(), calls: unknown[] = [], theme = createTheme({width: 390, isTV: false});
  const {NativeLibraryOverview} = await screenModule(new URL('../src-next/screens/library/LibraryOverview.tsx', import.meta.url), {
    react: host.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 44, bottom: 34})}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'}, '../../kit/cards': {LibraryCard: 'LibraryCard'}, '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/states': {StateView: 'StateView'},
    '../../kit/browse/presentation': {browseType: () => ({})}, '../../kit/theme': {useTheme: () => theme},
  });
  const libraries = [
    {id: 'actual-tv', name: 'Actual TV', kind: 'tv', summary: {counts: [{kind: 'show', count: 24, atLeast: false}, {kind: 'episode', count: 96, atLeast: true}], coverUrls: ['/real/tv'], latestArtwork: '/real/tv-wash'}},
    {id: 'actual-music', name: 'Actual music', kind: 'music', summary: {counts: [{kind: 'song', count: 40, atLeast: false}], coverUrls: ['/real/album'], latestArtwork: ''}},
  ];
  const tree = host.render(() => NativeLibraryOverview({directory: {subscribe() {}, getSnapshot: () => ({phase: 'ready', libraries})},
    activation: {subscribe() {}, getSnapshot: () => ({visible: true, intent: 'enter', revision: 1})},
    runtime: {artwork: (path: string) => ({uri: path, cacheKey: path}), ambient: (path: string) => ({uri: path, cacheKey: path})},
    openLibrary: (id: string) => calls.push(id), editPins: () => calls.push('pins')}));
  const cards = nodes(tree).filter(node => node.type === 'LibraryCard');
  assert.deepEqual(cards.map(card => card.props.title), ['Actual TV', 'Actual music']);
  assert.deepEqual(cards.map(card => card.props.shape), ['poster', 'square']);
  assert.equal(cards[0].props.width, 358); assert.equal(cards[0].props.height, 108);
  assert.deepEqual(cards[0].props.caption.map((part: any) => part.text), ['24 shows', '96+ episodes']);
  assert.equal(cards[0].props.covers[0].uri, '/real/tv'); assert.equal(cards[0].props.ambient.uri, '/real/tv-wash');
  assert.equal(cards[1].props.ambient, undefined, 'no invented wash when actual art is absent');
  cards[0].props.onNavigate(); nodes(tree).find(node => node.props.testID === 'libraries-edit-pins').props.onPress();
  assert.deepEqual(calls, ['actual-tv', 'pins']);
  assert.ok(nodes(tree).some(node => node.type === 'Text' && node.props.children.includes(defaultI18n.t('next.browse.native.libraryOrderHint'))));
});
