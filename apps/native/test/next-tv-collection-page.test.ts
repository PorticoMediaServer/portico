import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {screenModule} from './helpers/screen-harness.mjs';

// The TV collection page (pages/tv/collection/CollectionPage.tsx) is TitleFrame filled from `useCollection` (its own test
// holds the data). This holds what the TV page adds (tv-detail frame 11): the lede, Play all / Shuffle / More, and the
// films as one row under the count, how many are watched and the sort; a state under it. Seen on the Android TV emulator.

const t = defaultI18n.t;
type Node = Readonly<{type: unknown; props: Record<string, unknown> & {children: unknown[]}}>;
const react = {default: {Fragment: 'Fragment', createElement: (type: unknown, props: Record<string, unknown> | null, ...children: unknown[]): Node => ({type, props: {...props, children}})},
  useEffect: () => {}, useRef: () => ({current: null})};

async function page(view: Record<string, unknown>) {
  const {CollectionPage} = await screenModule(new URL('../src-next/pages/tv/collection/CollectionPage.tsx', import.meta.url), {
    react, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/overlays': {Menu: 'Menu'}, '../../../kit/controls': {Chip: 'Chip'},
    '../../../kit/artwork': {AmbientArt: 'AmbientArt'}, '../../../kit/frames': {TitleFrame: 'TitleFrame', PanelBlock: 'PanelBlock', PosterWindowRow: 'PosterWindowRow', StateRow: 'StateRow'},
    '../../../kit/layout': {HeroMetadata: 'HeroMetadata'}, '../../shared/collection/useCollection': {useCollection: () => view},
  });
  let registered = 0;
  const node = CollectionPage({state: {}, focusScope: 'collection:x', runtime: {registerTVPageBack: () => {registered++;}}, onBack: () => {}}) as Node;
  assert.equal(registered, 0, 'CollectionPage is a non-shell route and must not register a Home TVPageBack handler');
  const frame = (node.props.children as Node[])[0]!;
  return {frame, parts: frame.props.children.flat().filter(Boolean) as Node[]};
}
const menu = {title: '', items: [], open: false, onOpenChange: () => {}};
const view = (over: Record<string, unknown> = {}) => ({title: 'Toy Story', kicker: 'Collection', facts: [], heading: '4 films', lede: 'Four films on your server.',
  watched: '2 of 4 watched', sort: {label: 'Collection order', open: () => {}}, row: {testID: 'collection-row'}, sortMenu: menu, pageMenu: menu, play: () => {}, shuffle: () => {}, ...over});

test('frame 11: the lede, Play all with Shuffle and More, and one row under the count, the watched tally and the sort', async () => {
  const {frame, parts} = await page(view());
  assert.equal(frame.props.synopsis, 'Four films on your server.');
  assert.equal((frame.props.primary as {label: string}).label, t('next.cards.playAll'));
  assert.deepEqual((frame.props.actions as {testID: string}[]).map(action => action.testID), ['collection-shuffle', 'collection-menu']);
  assert.deepEqual(parts.map(part => part.type), ['PosterWindowRow']);
  assert.deepEqual([parts[0]!.props.title, parts[0]!.props.count, (parts[0]!.props.tools as Node).props.testID], ['4 films', '2 of 4 watched', 'collection-sort']);
  assert.equal(frame.props.shortHead, true, 'the row shows whole under the head on arrival');
});

test('a collection with no films (the hook gives no row) draws its state alone; a failed later page keeps the row with the state under it', async () => {
  const empty = await page(view({state: {kind: 'empty'}, row: undefined}));
  assert.deepEqual(empty.parts.map(part => part.type), ['PanelBlock']);
  const later = await page(view({state: {kind: 'error'}}));
  assert.deepEqual(later.parts.map(part => part.type), ['PosterWindowRow', 'PanelBlock']);
});
