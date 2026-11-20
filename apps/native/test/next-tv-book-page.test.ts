import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {screenModule} from './helpers/screen-harness.mjs';

// The TV book page (pages/tv/book/BookPage.tsx) is TitleFrame filled from `useBook` (its own test holds the data). This holds
// what the TV page adds (collections frame 43): the square cover, the series over the title and the author under it, Resume
// with its progress, Start over and ⋯, and the chapters beside the cover with their count after the heading, the next page
// read when focus reaches the last. Seen on the Android TV emulator.

const t = defaultI18n.t;
type Node = Readonly<{type: unknown; props: Record<string, unknown> & {children: unknown[]}}>;
const react = {default: {Fragment: 'Fragment', createElement: (type: unknown, props: Record<string, unknown> | null, ...children: unknown[]): Node => ({type, props: {...props, children}})}};

let current: Record<string, unknown> = {};
const load = screenModule(new URL('../src-next/pages/tv/book/BookPage.tsx', import.meta.url), {
  react, '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/artwork': {AmbientArt: 'AmbientArt', Artwork: 'Artwork', missingArt: () => 'missing'},
  '../../../kit/frames': {StateRow: 'StateRow', TitleFrame: 'TitleFrame'}, '../../../kit/layout': {HeroMetadata: 'HeroMetadata'},
  '../../../kit/music/TrackWindow': {TrackWindow: 'TrackWindow'}, '../../shared/book/useBook': {useBook: () => current},
});
let nearEnd = 0;
const view = (over: Record<string, unknown> = {}) => ({title: 'Dune', author: 'Frank Herbert', facts: [],
  kicker: {label: 'Dune, book 1', onPress: () => {}}, byline: {text: 'Frank Herbert', onPress: () => {}},
  primary: {label: 'Resume chapter 6', progress: .3, remaining: '5 h left', onPress: () => {}}, startOver: () => {}, more: () => {},
  chapters: {title: 'Chapters', count: '17', rows: [{id: 'c1'}, {id: 'c2'}], onPlay: () => {}}, onNearEnd: () => {nearEnd++;}, ...over});
async function page(over: Record<string, unknown> = {}) {
  current = view(over);
  const {BookPage} = await load;
  const frame = BookPage({controller: {}, entry: {id: 'dune'}, runtime: {}, actions: {}}) as Node;
  return {frame, beside: ((frame.props.beside as Node).props.children as unknown[]).flat().filter(Boolean) as Node[]};
}

test('frame 43: the square cover, the series over the title, the author under it, Resume with its progress, Start over and ⋯', async () => {
  const {frame} = await page();
  assert.equal((frame.props.cover as {shape: string}).shape, 'square');
  assert.equal((frame.props.kicker as {label: string}).label, 'Dune, book 1');
  assert.equal((frame.props.byline as {text: string}).text, 'Frank Herbert');
  const primary = frame.props.primary as {label: string; progress: number; remaining: string};
  assert.deepEqual([primary.label, primary.progress, primary.remaining], ['Resume chapter 6', .3, '5 h left']);
  assert.deepEqual((frame.props.actions as {testID: string; label: string}[]).map(action => [action.testID, action.label]),
    [['book-start-over', t('action.startOver')], ['book-menu', t('card.more', {title: 'Dune'})]]);
  const fresh = await page({primary: {label: t('entry.play'), onPress: () => {}}});
  assert.deepEqual((fresh.frame.props.actions as {testID: string}[]).map(action => action.testID), ['book-menu'], 'an unstarted book: no Start over');
});

test('the chapters beside the cover, the count after the heading; the next page is read when focus reaches the last chapter', async () => {
  const {beside} = await page();
  const list = beside.find(node => node.type === 'TrackWindow')!;
  assert.deepEqual([list.props.inset, list.props.variant, list.props.heading], [false, 'chapters', {title: 'Chapters', count: '17'}]);
  nearEnd = 0;
  (list.props.onFocusRow as (id: string) => void)('c1'); assert.equal(nearEnd, 0);
  (list.props.onFocusRow as (id: string) => void)('c2'); assert.equal(nearEnd, 1, 'the last chapter takes focus: the next page');
  const failed = await page({state: {kind: 'error'}, chapters: undefined, continuation: undefined});
  assert.deepEqual(failed.beside.map(node => node.type), ['StateRow']);
});
